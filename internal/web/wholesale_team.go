package web

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// Team management for wholesale accounts (/wholesale/account/team). These
// routes sit behind requireApprovedWholesale, so every handler can assume an
// approved wholesale account is in context.
//
// v1 has no roles: any signed-in member of the account can invite and remove
// others, the same as the primary sign-in. The customer_users.role column
// exists so that changing this later is a code change rather than a migration.

// --- Team page ---

func (d *Deps) handleWholesaleTeam(w http.ResponseWriter, r *http.Request) {
	d.renderWholesaleTeam(w, r, storefront.WholesaleTeamProps{
		Success: r.URL.Query().Get("success"),
		Error:   r.URL.Query().Get("error"),
	})
}

// renderWholesaleTeam loads the roster and renders the page, carrying over any
// flash message or repopulated invite form in overrides.
func (d *Deps) renderWholesaleTeam(w http.ResponseWriter, r *http.Request, overrides storefront.WholesaleTeamProps) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	var members []domain.CustomerUser
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		members, txErr = d.CustomerUserService.List(ctx, tx, customer.ID)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	props := overrides
	props.Customer = customer
	props.CompanyName = wholesaleCompanyName(customer)
	props.Members = members
	props.CartCount = d.wholesaleCartItemCount(r)
	if actingUser, ok := auth.CustomerUserFromContext(ctx); ok {
		props.ActingUserID = actingUser.ID.String()
	}

	if IsHTMX(r) {
		storefront.WholesaleTeamContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleTeamPage(props).Render(ctx, w) //nolint:errcheck
}

// teamRedirect sends the browser back to the team page with a flash message.
// POSTs redirect rather than re-render so a refresh cannot replay the action.
func teamRedirect(w http.ResponseWriter, r *http.Request, key, msg string) {
	target := "/wholesale/account/team?" + key + "=" + url.QueryEscape(msg)
	if IsHTMX(r) {
		w.Header().Set("HX-Redirect", target)
		return
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

// --- Invite ---

func (d *Deps) handleWholesaleTeamInvite(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	email := r.FormValue("email")
	name := r.FormValue("name")

	receivesNotifications := inviteWantsNotifications(r)

	var user *domain.CustomerUser

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		u, rawToken, txErr := d.CustomerUserService.Invite(
			ctx, tx, customerActor(r), customer.ID, email, name, receivesNotifications,
		)
		if txErr != nil {
			return txErr
		}
		user = u

		// Same transaction as the insert: if the send job cannot be enqueued,
		// the invite itself rolls back rather than stranding a member who never
		// receives a link.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.CustomerUserInviteSendArgs{
			CustomerUserID: u.ID,
			RawToken:       rawToken,
		}, nil)
		return txErr
	})
	if err != nil {
		msg, ok := teamInviteErrorMessage(err)
		if !ok {
			Error(w, r, err)
			return
		}
		// Re-render in place so the typed values are not lost.
		d.renderWholesaleTeam(w, r, storefront.WholesaleTeamProps{
			Error:       msg,
			InviteEmail: email,
			InviteName:  name,
		})
		return
	}

	teamRedirect(w, r, "success", "Invite sent to "+user.Email+".")
}

// inviteWantsNotifications reads the invite form's email-preference checkbox.
//
// New members are subscribed unless the inviter explicitly unticks the box. An
// unchecked checkbox submits no field at all, which is indistinguishable from a
// form that never carried the field — so the hidden `notifications_submitted`
// companion is what tells the two apart. Without it, defaulting to true would
// silently ignore a deliberate untick.
func inviteWantsNotifications(r *http.Request) bool {
	if err := r.ParseForm(); err != nil {
		return true
	}
	if _, submitted := r.Form["notifications_submitted"]; !submitted {
		return true
	}
	return r.FormValue("receives_notifications") == "1"
}

// teamInviteErrorMessage maps the expected validation failures to copy for the
// form. Returns ok=false for anything unexpected, which the caller surfaces as
// a real error rather than silently showing a friendly message.
func teamInviteErrorMessage(err error) (string, bool) {
	switch {
	case errors.Is(err, app.ErrCustomerUserEmailRequired):
		return "Enter an email address.", true
	case errors.Is(err, app.ErrCustomerUserEmailTaken):
		return "That email address already has an account with us. Ask the crew to sort it out.", true
	case errors.Is(err, app.ErrNotWholesaleAccount):
		return "Team members are only available on wholesale accounts.", true
	default:
		return "", false
	}
}

// --- Resend invite / reset password ---

func (d *Deps) handleWholesaleTeamResend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		teamRedirect(w, r, "error", "That team member could not be found.")
		return
	}

	var user *domain.CustomerUser

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		u, rawToken, txErr := d.CustomerUserService.ResendInvite(ctx, tx, customerActor(r), id, customer.ID)
		if txErr != nil {
			return txErr
		}
		user = u

		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.CustomerUserInviteSendArgs{
			CustomerUserID: u.ID,
			RawToken:       rawToken,
		}, nil)
		return txErr
	})
	if err != nil {
		if errors.Is(err, app.ErrCustomerUserNotFound) {
			teamRedirect(w, r, "error", "That team member could not be found.")
			return
		}
		Error(w, r, err)
		return
	}

	teamRedirect(w, r, "success", "Fresh link sent to "+user.Email+".")
}

// --- Revoke ---

func (d *Deps) handleWholesaleTeamRevoke(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		teamRedirect(w, r, "error", "That team member could not be found.")
		return
	}

	// Removing yourself revokes your own session inside the transaction, so the
	// cookie must be cleared and the browser sent somewhere public — otherwise
	// the redirect lands on a page the now-dead session cannot load.
	selfRevoke := false
	if actingUser, ok := auth.CustomerUserFromContext(ctx); ok && actingUser.ID == id {
		selfRevoke = true
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerUserService.Revoke(ctx, tx, customerActor(r), id, customer.ID)
	})
	if err != nil {
		if errors.Is(err, app.ErrCustomerUserNotFound) {
			teamRedirect(w, r, "error", "That team member could not be found.")
			return
		}
		Error(w, r, err)
		return
	}

	if selfRevoke {
		clearCustomerCookie(w)
		if IsHTMX(r) {
			w.Header().Set("HX-Redirect", "/")
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}

	teamRedirect(w, r, "success", "Team member removed.")
}

// --- Notification preference ---

func (d *Deps) handleWholesaleTeamNotifications(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		teamRedirect(w, r, "error", "That team member could not be found.")
		return
	}

	enabled := r.FormValue("enabled") == "1"

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.CustomerUserService.SetNotifications(ctx, tx, customerActor(r), id, customer.ID, enabled)
	})
	if err != nil {
		if errors.Is(err, app.ErrCustomerUserNotFound) {
			teamRedirect(w, r, "error", "That team member could not be found.")
			return
		}
		Error(w, r, err)
		return
	}

	if enabled {
		teamRedirect(w, r, "success", "They'll get order email from now on.")
		return
	}
	teamRedirect(w, r, "success", "They'll no longer get order email.")
}

// --- Invite acceptance (public, token from the invite email) ---

func (d *Deps) handleWholesaleInvitePage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}

	// Validate without consuming, so an expired link says so instead of
	// presenting a form that is guaranteed to fail on submit.
	err := store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AuthService.LookupCustomerUserInvite(r.Context(), tx, token)
		return txErr
	})

	props := storefront.WholesaleSetupProps{Token: token, Action: "/wholesale/invite"}
	if err != nil {
		if errors.Is(err, app.ErrCustomerUserInviteInvalid) {
			props.Token = ""
			props.Error = "This invite link has expired or has already been used. Ask whoever invited you to send a fresh one."
		} else {
			Error(w, r, err)
			return
		}
	}

	if IsHTMX(r) {
		storefront.WholesaleSetupContent(props).Render(r.Context(), w) //nolint:errcheck
		return
	}
	storefront.WholesaleSetupPage(props).Render(r.Context(), w) //nolint:errcheck
}

func (d *Deps) handleWholesaleInviteAccept(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	token := r.FormValue("token")
	password := r.FormValue("password")
	passwordConfirm := r.FormValue("password_confirm")

	renderErr := func(msg string) {
		props := storefront.WholesaleSetupProps{Token: token, Action: "/wholesale/invite", Error: msg}
		if IsHTMX(r) {
			storefront.WholesaleSetupContent(props).Render(ctx, w) //nolint:errcheck
			return
		}
		storefront.WholesaleSetupPage(props).Render(ctx, w) //nolint:errcheck
	}

	if token == "" {
		http.Redirect(w, r, "/wholesale/login", http.StatusSeeOther)
		return
	}
	if password != passwordConfirm {
		renderErr("Passwords do not match.")
		return
	}
	if len(password) < 10 {
		renderErr("Password must be at least 10 characters.")
		return
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.AuthService.SetCustomerUserPasswordWithToken(ctx, tx, token, password)
		return txErr
	})
	if err != nil {
		switch {
		case errors.Is(err, app.ErrCustomerUserInviteInvalid):
			renderErr("This invite link has expired or has already been used. Ask whoever invited you to send a fresh one.")
		case errors.Is(err, app.ErrPasswordTooShort):
			renderErr("Password must be at least 10 characters.")
		default:
			Error(w, r, err)
		}
		return
	}

	props := storefront.WholesaleSetupProps{Success: true}
	if IsHTMX(r) {
		storefront.WholesaleSetupContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	storefront.WholesaleSetupPage(props).Render(ctx, w) //nolint:errcheck
}
