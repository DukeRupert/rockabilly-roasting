package web

import (
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
)

// Whole-box skip and pause from the account page. A box has no id — it is
// derived — so the form names it by its key: the shipping address and the
// renewal instant, exactly as the page rendered them. Ownership is the
// service's customer-scoped read; a key naming another customer's address
// finds nothing and is answered as a per-row action on someone else's
// subscription is, not found.

// parseBoxKey reads a box's key off a submitted form.
func parseBoxKey(r *http.Request) (domain.SubscriptionBoxKey, bool) {
	if err := r.ParseForm(); err != nil {
		return domain.SubscriptionBoxKey{}, false
	}
	addr, err := uuid.Parse(r.FormValue("address_id"))
	if err != nil {
		return domain.SubscriptionBoxKey{}, false
	}
	at, err := time.Parse(time.RFC3339Nano, r.FormValue("next_order_at"))
	if err != nil {
		return domain.SubscriptionBoxKey{}, false
	}
	return domain.SubscriptionBoxKey{ShippingAddressID: addr, NextOrderAt: at}, true
}

// handleAccountSubscriptionBoxSkip skips every subscription in a box to one
// restart day, so they still ship together, and sends each member's usual
// skip email.
func (d *Deps) handleAccountSubscriptionBoxSkip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	key, ok := parseBoxKey(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// By date only — see SubscriptionService.SkipBox — read in the merchant's
	// zone like the per-row date form.
	day, err := time.ParseInLocation("2006-01-02", r.FormValue("resume_on"), d.MerchantTZ)
	if err != nil {
		Error(w, r, app.ErrSkipDateOutOfRange)
		return
	}
	params := app.SkipSubscriptionParams{ResumeOn: &day}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		skipped, txErr := d.SubscriptionService.SkipBox(ctx, tx, customer.ID, key, day, customerActor(r))
		if txErr != nil {
			return txErr
		}
		// One email per member, as a per-row skip sends. Chatty for a big box,
		// and the combined email is deferred on purpose; see the TODO.
		for _, sub := range skipped {
			if txErr := d.enqueueSkipEmail(ctx, tx, sub, params); txErr != nil {
				return txErr
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// handleAccountSubscriptionBoxPause pauses every subscription in a box. No
// email, as a per-row pause sends none.
func (d *Deps) handleAccountSubscriptionBoxPause(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	key, ok := parseBoxKey(r)
	if !ok {
		http.NotFound(w, r)
		return
	}

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.PauseBox(ctx, tx, customer.ID, key, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}
