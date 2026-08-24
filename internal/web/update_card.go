package web

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// Updating a card from a dunning email is split across GET and POST on one URL
// for the same reason switching to pickup and undoing a skip are (see
// switch_to_pickup.go): inbox scanners fetch every link in an incoming message.
// A GET that created a portal session would burn one on every corporate mail
// filter that touched the message. GET renders a confirmation and changes
// nothing; the button on it POSTs.
//
// Both halves are public and token-authenticated — the whole point is that a
// customer whose card just failed can fix it without first remembering a
// password. Three things keep that from being a hole:
//
//   - The token is an HMAC naming one subscription and one purpose. It is not a
//     session and signs nobody in.
//   - The Stripe session is created through CreatePaymentMethodUpdateSession,
//     which Stripe scopes to the card form alone. The holder never reaches
//     billing history or invoices.
//   - The subscription must actually be past_due. A link that leaks after the
//     card is fixed opens nothing.

// handleUpdateCardPage renders the confirmation. Read-only.
func (d *Deps) handleUpdateCardPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	subID, err := d.verifyUpdateCardToken(token)
	if err != nil {
		d.renderUpdateCardProblem(w, r, err)
		return
	}

	var props storefront.UpdateCardProps
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.GetSubscriptionAsStaff(r.Context(), tx, subID)
		if txErr != nil {
			return txErr
		}
		if sub.Status != domain.SubscriptionStatusPastDue {
			return errNotPastDue
		}
		props = d.updateCardProps(r, tx, sub)
		props.Token = token
		return nil
	})
	if err != nil {
		d.renderUpdateCardProblem(w, r, err)
		return
	}

	storefront.UpdateCardConfirmPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleUpdateCard mints the scoped Stripe session and redirects to it.
//
// Nothing in our database changes here — the customer's new card lands on
// Stripe's side, and the next renewal attempt picks it up via
// pickRenewalPaymentMethod.
//
// The hard-decline latch is not cleared here, and does not need to be: the
// renewal path compares the card on file against the one that died and releases
// the latch itself when they differ. Clearing it on this request instead would
// be wrong twice over — the customer may abandon the Stripe form without adding
// anything, and a card added is not yet a card that works.
func (d *Deps) handleUpdateCard(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("t")
	if token == "" {
		token = r.URL.Query().Get("t")
	}
	subID, err := d.verifyUpdateCardToken(token)
	if err != nil {
		d.renderUpdateCardProblem(w, r, err)
		return
	}

	var stripeCustomerID string
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.GetSubscriptionAsStaff(r.Context(), tx, subID)
		if txErr != nil {
			return txErr
		}
		if sub.Status != domain.SubscriptionStatusPastDue {
			return errNotPastDue
		}
		customer, txErr := d.CustomerService.GetCustomer(r.Context(), tx, sub.CustomerID)
		if txErr != nil {
			return txErr
		}
		if customer.StripeCustomerID == nil {
			// A subscription that has been charged always has one, so this is
			// defensive — but a nil deref here would be a 500 on a page whose
			// whole job is to rescue a failing subscription.
			return errPortalUnavailable
		}
		stripeCustomerID = *customer.StripeCustomerID
		return nil
	})
	if err != nil {
		d.renderUpdateCardProblem(w, r, err)
		return
	}

	url, err := d.PaymentProvider.CreatePaymentMethodUpdateSession(
		r.Context(), stripeCustomerID, d.BaseURL+"/account/subscriptions")
	if err != nil {
		d.Logger.Error("create payment method update session",
			"subscription_id", subID, "error", err)
		d.renderUpdateCardProblem(w, r, errPortalUnavailable)
		return
	}

	http.Redirect(w, r, url, http.StatusSeeOther)
}

// Sentinel outcomes local to this flow. They are not app-layer errors because
// nothing outside these handlers can produce or consume them.
var (
	errNotPastDue        = errors.New("subscription is not past due")
	errPortalUnavailable = errors.New("payment portal unavailable")
)

// updateCardProps fills in the copy the page needs. Product and plan lookups
// are best-effort: a missing product name must not cost the customer their way
// to the card form.
func (d *Deps) updateCardProps(r *http.Request, tx pgx.Tx, sub *domain.Subscription) storefront.UpdateCardProps {
	props := storefront.UpdateCardProps{
		ProductName: "your subscription",
		HardDecline: sub.DunningHardDeclined(),
	}
	// Only promise a closeout date once the ladder is actually running. A
	// subscription marked past_due by the payment-failed webhook has no recorded
	// attempt yet, and DunningExpiresAt falls back to next_order_at for those —
	// which is the next charge date, not the day it closes. Rendering that under
	// "Closes out on" would tell the customer their subscription dies about ten
	// days earlier than it does.
	if sub.DunningAttempt() > 0 {
		props.EndsOn = app.DunningExpiresAt(sub).In(d.MerchantTZ).Format("January 2, 2006")
	}
	if variant, err := d.CatalogService.GetVariant(r.Context(), tx, sub.VariantID); err == nil {
		if product, err := d.CatalogService.GetProduct(r.Context(), tx, variant.ProductID); err == nil {
			props.ProductName = product.Title
		}
	}
	if plan, err := d.SubscriptionService.GetPlan(r.Context(), tx, sub.PlanID); err == nil {
		props.PlanName = plan.Name
	}
	return props
}

// verifyUpdateCardToken checks the emailed link's signature, purpose, and expiry
// against a single clock read.
func (d *Deps) verifyUpdateCardToken(token string) (uuid.UUID, error) {
	if d.OrderActionSigner == nil {
		return uuid.Nil, auth.ErrInvalidOrderActionToken
	}
	return d.OrderActionSigner.Verify(token, auth.OrderActionUpdateCard, time.Now())
}

// renderUpdateCardProblem maps every failure to a page that tells the customer
// what to do next.
//
// A bad signature and an unknown subscription are deliberately collapsed into
// one generic response so a probe cannot use this endpoint to discover which
// subscription IDs exist. Expiry, "already sorted", and "our end is down" are
// reported honestly: all three are states the holder of a genuine link can
// reach through no fault of their own.
func (d *Deps) renderUpdateCardProblem(w http.ResponseWriter, r *http.Request, err error) {
	props := storefront.UpdateCardProps{}

	switch {
	case errors.Is(err, auth.ErrOrderActionTokenExpired):
		props.Reason = storefront.UpdateCardReasonExpired
		w.WriteHeader(http.StatusGone)
	case errors.Is(err, errNotPastDue):
		props.Reason = storefront.UpdateCardReasonNotPastDue
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, errPortalUnavailable):
		props.Reason = storefront.UpdateCardReasonUnavailable
		w.WriteHeader(http.StatusServiceUnavailable)
	default:
		props.Reason = storefront.UpdateCardReasonInvalid
		w.WriteHeader(http.StatusBadRequest)
	}

	storefront.UpdateCardProblemPage(props).Render(r.Context(), w) //nolint:errcheck
}
