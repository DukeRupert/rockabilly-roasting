package web

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
)

// parseSkipForm reads a skip request off a submitted form. Two shapes are
// accepted, selected by the skip_mode field: "intervals" carries a shipment
// count, "date" carries a restart day (yyyy-mm-dd) read in the merchant's
// timezone so "September 3rd" means the roastery's September 3rd, not UTC's.
// Range checks live in the service — this only turns strings into values.
func parseSkipForm(r *http.Request, tz *time.Location) (app.SkipSubscriptionParams, error) {
	if err := r.ParseForm(); err != nil {
		return app.SkipSubscriptionParams{}, err
	}

	switch r.FormValue("skip_mode") {
	case "date":
		day, err := time.ParseInLocation("2006-01-02", r.FormValue("resume_on"), tz)
		if err != nil {
			return app.SkipSubscriptionParams{}, app.ErrSkipDateOutOfRange
		}
		return app.SkipSubscriptionParams{ResumeOn: &day}, nil
	case "intervals":
		n, err := strconv.Atoi(r.FormValue("intervals"))
		if err != nil || n < 1 {
			return app.SkipSubscriptionParams{}, app.ErrSkipIntervalsOutOfRange
		}
		return app.SkipSubscriptionParams{Intervals: n}, nil
	default:
		return app.SkipSubscriptionParams{}, app.ErrInvalidSkipRequest
	}
}

// handleAccountSubscriptionSkip lets a customer push their next few shipments
// out without cancelling or pausing — the "I'm travelling / still have beans"
// escape hatch. Ownership is enforced by the customer-scoped read before the
// skip runs.
func (d *Deps) handleAccountSubscriptionSkip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	params, err := parseSkipForm(r, d.MerchantTZ)
	if err != nil {
		Error(w, r, err)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, txErr := d.SubscriptionService.GetSubscriptionByCustomer(ctx, tx, id, customer.ID); txErr != nil {
			return txErr
		}
		sub, txErr := d.SubscriptionService.SkipSubscription(ctx, tx, id, params, customerActor(r))
		if txErr != nil {
			return txErr
		}
		return d.enqueueSkipEmail(ctx, tx, sub, params)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// handleAdminSubscriptionSkip is the staff counterpart, for when a customer
// phones in rather than logging in.
func (d *Deps) handleAdminSubscriptionSkip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	params, err := parseSkipForm(r, d.MerchantTZ)
	if err != nil {
		Error(w, r, err)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.SkipSubscription(ctx, tx, id, params, staffActor(r))
		if txErr != nil {
			return txErr
		}
		return d.enqueueSkipEmail(ctx, tx, sub, params)
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Shipments+skipped", http.StatusSeeOther)
}

// enqueueSkipEmail queues the customer's skip notification in the same
// transaction as the skip itself, so the mail can never describe a schedule
// change that rolled back. Sent for staff-initiated skips too: it is the
// customer's schedule that moved, whoever pressed the button.
func (d *Deps) enqueueSkipEmail(ctx context.Context, tx pgx.Tx, sub *domain.Subscription, params app.SkipSubscriptionParams) error {
	_, err := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionSkippedArgs{
		SubscriptionID: sub.ID,
		CustomerID:     sub.CustomerID,
		SkippedCount:   params.Intervals,
	}, nil)
	return err
}

// handleAccountSubscriptionUndoSkip is the signed-in counterpart to the emailed
// undo link, for a customer who is already on their subscriptions page.
func (d *Deps) handleAccountSubscriptionUndoSkip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		if _, txErr := d.SubscriptionService.GetSubscriptionByCustomer(ctx, tx, id, customer.ID); txErr != nil {
			return txErr
		}
		_, txErr := d.SubscriptionService.UndoSkip(ctx, tx, id, customerActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// handleAdminSubscriptionUndoSkip lets staff take back a skip they just made.
func (d *Deps) handleAdminSubscriptionUndoSkip(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		// Read the skipped-to date before the undo clears it — the customer's
		// notice names both dates.
		before, txErr := d.SubscriptionService.GetSubscriptionAsStaff(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		skippedTo := before.NextOrderAt

		sub, txErr := d.SubscriptionService.UndoSkip(ctx, tx, id, staffActor(r))
		if txErr != nil {
			return txErr
		}
		// An undo pulls the charge date forward, so the customer hears about it
		// — the mirror of always emailing on a staff-initiated skip. A customer
		// who undid it themselves already saw it confirmed on screen, so those
		// paths stay quiet.
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionSkipUndoneArgs{
			SubscriptionID: sub.ID,
			CustomerID:     sub.CustomerID,
			SkippedTo:      skippedTo,
		}, nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Skip+undone", http.StatusSeeOther)
}
