package web

import (
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// Unsubscribe is deliberately split across two methods on the same URL.
//
// GET only renders a confirmation page and changes nothing. Corporate mail
// gateways and inbox scanners (Outlook Safe Links and friends) fetch every
// link in an incoming message, so a GET that acted would let a customer's own
// IT department silently unsubscribe them — the classic way this feature goes
// wrong. The button on that page POSTs.
//
// POST does the work, and accepts the token from either the form or the query
// string. The query-string path is what RFC 8058 one-click needs: Gmail and
// Apple Mail POST the List-Unsubscribe URL directly, with no form involved.
// That POST originates from the mail provider acting on an explicit user
// click, not from a scanner, so acting immediately is correct there.

// handleUnsubscribePage renders the opt-out confirmation. Read-only.
func (d *Deps) handleUnsubscribePage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	target, err := d.UnsubscribeSigner.Verify(token)
	if err != nil {
		d.renderUnsubscribeInvalid(w, r)
		return
	}

	email, err := d.unsubscribeRecipientEmail(r, target)
	if err != nil {
		d.renderUnsubscribeInvalid(w, r)
		return
	}

	storefront.UnsubscribeConfirmPage(storefront.UnsubscribeProps{
		Token: token,
		Email: email,
		Topic: unsubscribeTopic(target),
	}).Render(r.Context(), w) //nolint:errcheck
}

// handleUnsubscribe applies the opt-out. Serves both the confirmation-page
// button and RFC 8058 one-click.
func (d *Deps) handleUnsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("t")
	if token == "" {
		token = r.URL.Query().Get("t")
	}

	target, err := d.UnsubscribeSigner.Verify(token)
	if err != nil {
		d.renderUnsubscribeInvalid(w, r)
		return
	}

	if err := d.applyUnsubscribe(r, target, false); err != nil {
		Error(w, r, err)
		return
	}

	// One-click clients (Gmail/Apple) discard the body and only read the
	// status, so there is no point rendering a page for them.
	if isOneClickUnsubscribe(r) {
		w.WriteHeader(http.StatusOK)
		return
	}

	email, _ := d.unsubscribeRecipientEmail(r, target)
	storefront.UnsubscribeDonePage(storefront.UnsubscribeProps{
		Token: token,
		Email: email,
		Topic: unsubscribeTopic(target),
	}).Render(r.Context(), w) //nolint:errcheck
}

// handleResubscribe undoes an opt-out. Mis-clicks happen, and the alternative
// to an undo button is the customer emailing staff to be put back on.
func (d *Deps) handleResubscribe(w http.ResponseWriter, r *http.Request) {
	target, err := d.UnsubscribeSigner.Verify(r.FormValue("t"))
	if err != nil {
		d.renderUnsubscribeInvalid(w, r)
		return
	}

	if err := d.applyUnsubscribe(r, target, true); err != nil {
		Error(w, r, err)
		return
	}

	email, _ := d.unsubscribeRecipientEmail(r, target)
	storefront.ResubscribeDonePage(storefront.UnsubscribeProps{
		Email: email,
		Topic: unsubscribeTopic(target),
	}).Render(r.Context(), w) //nolint:errcheck
}

// applyUnsubscribe flips the preference for exactly the recipient the token
// names. A teammate's opt-out must never reach into the account-wide flag —
// several people share one mailing, and silencing all of them because one
// clicked is the failure this indirection exists to prevent.
//
// The audience also names the *topic*: announcement tokens write a different
// flag from reminder tokens. Muting shop notices must not silence the weekly
// order reminder, or the reverse — they are separate subscriptions, and a
// recipient who clicks one link has said nothing about the other.
func (d *Deps) applyUnsubscribe(r *http.Request, target auth.UnsubscribeTarget, enabled bool) error {
	return store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		switch target.Audience {
		case auth.UnsubscribeAudienceAnnouncementCustomer,
			auth.UnsubscribeAudienceAnnouncementCustomerUser:
			return d.AnnouncementService.SetAnnouncementsFromEmailLink(r.Context(), tx, target, enabled)
		case auth.UnsubscribeAudienceCustomerUser:
			return d.CustomerUserService.SetNotificationsFromEmailLink(r.Context(), tx, target.ID, enabled)
		default:
			return d.WholesaleService.SetOrderRemindersFromEmailLink(r.Context(), tx, target.ID, enabled)
		}
	})
}

// unsubscribeTopic maps a token's audience to the wording and paths the page
// should use. The token is the only thing that says which subscription the
// reader clicked on, so the page must be driven by it and never by which URL
// happened to serve the request — both paths accept both kinds of token.
func unsubscribeTopic(target auth.UnsubscribeTarget) storefront.UnsubscribeTopic {
	switch target.Audience {
	case auth.UnsubscribeAudienceAnnouncementCustomer,
		auth.UnsubscribeAudienceAnnouncementCustomerUser:
		return storefront.AnnouncementTopic()
	default:
		return storefront.ReminderTopic()
	}
}

// isOneClickUnsubscribe detects the RFC 8058 POST, which carries exactly
// "List-Unsubscribe=One-Click" as its form body.
func isOneClickUnsubscribe(r *http.Request) bool {
	return r.FormValue("List-Unsubscribe") == "One-Click"
}

// unsubscribeRecipientEmail looks up the address to show on the confirmation
// page, so the reader can see exactly which address is being silenced — which
// matters more now that several colleagues receive the same mailing.
func (d *Deps) unsubscribeRecipientEmail(r *http.Request, target auth.UnsubscribeTarget) (string, error) {
	var email string
	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		if target.Audience == auth.UnsubscribeAudienceCustomerUser ||
			target.Audience == auth.UnsubscribeAudienceAnnouncementCustomerUser {
			u, txErr := d.CustomerUserService.GetForEmailLink(r.Context(), tx, target.ID)
			if txErr != nil {
				return txErr
			}
			email = u.Email
			return nil
		}
		c, txErr := d.CustomerService.GetCustomer(r.Context(), tx, target.ID)
		if txErr != nil {
			return txErr
		}
		email = c.Email
		return nil
	})
	return email, err
}

// renderUnsubscribeInvalid handles a token that is malformed, tampered with,
// signed under a rotated secret, or points at a deleted customer. All four look
// identical from outside, so a probe learns nothing about which IDs exist.
func (d *Deps) renderUnsubscribeInvalid(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusBadRequest)
	storefront.UnsubscribeInvalidPage().Render(r.Context(), w) //nolint:errcheck
}
