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

// Undoing a skip from the skip-confirmation email is split across GET and POST
// on one URL for the same reason switching to pickup is (see
// switch_to_pickup.go): mail gateways and inbox scanners fetch every link in an
// incoming message, and a GET that acted would let a customer's IT department
// silently un-skip — and so re-charge — their subscription. GET renders a
// confirmation and changes nothing; the button on it POSTs.

// handleUndoSkipPage renders the confirmation. Read-only.
func (d *Deps) handleUndoSkipPage(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("t")
	subID, err := d.verifyUndoSkipToken(token)
	if err != nil {
		d.renderUndoSkipProblem(w, r, err)
		return
	}

	var props storefront.UndoSkipProps
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.GetSubscriptionAsStaff(r.Context(), tx, subID)
		if txErr != nil {
			return txErr
		}
		// The same guards UndoSkip enforces, checked before drawing a button
		// that would only fail: the subscription must still be sitting on the
		// date its skip set, and the date it would return to must still be
		// ahead of us.
		undo, ok := sub.SkipUndo()
		if !ok || !undo.AppliedNextOrderAt.Equal(sub.NextOrderAt) {
			return app.ErrNoSkipToUndo
		}
		if !undo.NextOrderAt.After(time.Now()) {
			return app.ErrSkipUndoTooLate
		}
		props = d.undoSkipProps(r, tx, sub, undo.NextOrderAt)
		props.Token = token
		return nil
	})
	if err != nil {
		d.renderUndoSkipProblem(w, r, err)
		return
	}

	storefront.UndoSkipConfirmPage(props).Render(r.Context(), w) //nolint:errcheck
}

// handleUndoSkip applies the undo.
func (d *Deps) handleUndoSkip(w http.ResponseWriter, r *http.Request) {
	token := r.FormValue("t")
	if token == "" {
		token = r.URL.Query().Get("t")
	}
	subID, err := d.verifyUndoSkipToken(token)
	if err != nil {
		d.renderUndoSkipProblem(w, r, err)
		return
	}

	var props storefront.UndoSkipProps
	err = store.Tx(r.Context(), d.Pool, func(tx pgx.Tx) error {
		// The customer is the actor: this is their own request, arriving with
		// proof they hold a link the shop sent them. Attributing it to the
		// system would hide a customer-driven change in the audit log.
		actor, txErr := d.subscriptionCustomerActor(r, tx, subID)
		if txErr != nil {
			return txErr
		}
		sub, txErr := d.SubscriptionService.UndoSkip(r.Context(), tx, subID, actor)
		if txErr != nil {
			return txErr
		}
		props = d.undoSkipProps(r, tx, sub, sub.NextOrderAt)
		return nil
	})
	if err != nil {
		d.renderUndoSkipProblem(w, r, err)
		return
	}

	storefront.UndoSkipDonePage(props).Render(r.Context(), w) //nolint:errcheck
}

// undoSkipProps fills in the copy the page needs: what the subscription is,
// the date the skip moved it to, and the date the undo would move it back to.
// Product and plan lookups are best-effort — the dates are the point, and a
// missing product name must not cost the customer their way out.
func (d *Deps) undoSkipProps(r *http.Request, tx pgx.Tx, sub *domain.Subscription, restoreTo time.Time) storefront.UndoSkipProps {
	props := storefront.UndoSkipProps{
		ProductName:    "your subscription",
		SkippedTo:      sub.NextOrderAt.In(d.MerchantTZ).Format("January 2, 2006"),
		RestoredTo:     restoreTo.In(d.MerchantTZ).Format("January 2, 2006"),
		SubscriptionID: sub.ID,
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

// verifyUndoSkipToken checks the emailed link's signature, purpose, and expiry
// against a single clock read.
func (d *Deps) verifyUndoSkipToken(token string) (uuid.UUID, error) {
	if d.OrderActionSigner == nil {
		return uuid.Nil, auth.ErrInvalidOrderActionToken
	}
	return d.OrderActionSigner.Verify(token, auth.OrderActionUndoSkip, time.Now())
}

// subscriptionCustomerActor resolves the subscription's own customer as the
// audit actor. Falls back to an unattributed customer actor when the customer
// row can't be read, which is preferable to failing an undo the customer is
// otherwise entitled to make.
func (d *Deps) subscriptionCustomerActor(r *http.Request, tx pgx.Tx, subID uuid.UUID) (app.Actor, error) {
	sub, err := d.SubscriptionService.GetSubscriptionAsStaff(r.Context(), tx, subID)
	if err != nil {
		return app.Actor{}, err
	}
	actor := app.Actor{Type: domain.AuditActorTypeCustomer, Name: "customer (email link)"}
	customer, err := d.CustomerService.GetCustomer(r.Context(), tx, sub.CustomerID)
	if err != nil {
		return actor, nil //nolint:nilerr // actor attribution is best-effort; the undo itself is authorized by the token
	}
	actor.ID = &customer.ID
	actor.Name = customer.Email
	return actor, nil
}

// renderUndoSkipProblem maps every failure to a page that tells the customer
// what to do next.
//
// A bad signature, an unknown subscription, and a rotated secret are
// deliberately collapsed into one generic response so a probe cannot use this
// endpoint to discover which subscription IDs exist. Expiry, "nothing to undo",
// and "that date has passed" are reported honestly: all three are states the
// holder of a genuine link can reach through no fault of their own.
func (d *Deps) renderUndoSkipProblem(w http.ResponseWriter, r *http.Request, err error) {
	props := storefront.UndoSkipProps{}

	switch {
	case errors.Is(err, auth.ErrOrderActionTokenExpired):
		props.Reason = storefront.UndoSkipReasonExpired
		w.WriteHeader(http.StatusGone)
	case errors.Is(err, app.ErrNoSkipToUndo):
		props.Reason = storefront.UndoSkipReasonNothingToUndo
		w.WriteHeader(http.StatusConflict)
	case errors.Is(err, app.ErrSkipUndoTooLate), errors.Is(err, app.ErrSubscriptionNotSkippable):
		props.Reason = storefront.UndoSkipReasonTooLate
		w.WriteHeader(http.StatusConflict)
	default:
		props.Reason = storefront.UndoSkipReasonInvalid
		w.WriteHeader(http.StatusBadRequest)
	}

	storefront.UndoSkipProblemPage(props).Render(r.Context(), w) //nolint:errcheck
}
