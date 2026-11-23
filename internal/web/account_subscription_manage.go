package web

import (
	"context"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/auth"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

// Customers changing a subscription, and adding a coffee to a box, from the
// account page. Nothing is charged when they do: each change applies from the
// next renewal, and the customer gets one email saying what changed. See
// SubscriptionService.EditSubscription and AddToBox.

// handleAccountSubscriptionEdit saves the Change form. The form sends every
// field, opened on what the subscription has; the service changes only what
// differs, and an edit that changes nothing sends no email.
func (d *Deps) handleAccountSubscriptionEdit(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	params, err := parseEditSubscriptionForm(r)
	if err != nil {
		Error(w, r, err)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		edit, txErr := d.SubscriptionService.EditSubscription(ctx, tx, id, customer.ID, params, customerActor(r))
		if txErr != nil {
			return txErr
		}
		if !edit.Changed() {
			return nil
		}
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionChangedArgsFrom(edit.Notice()), nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// handleAccountSubscriptionBoxAdd saves the Add a coffee form: a new
// subscription that ships and is charged with the box's next renewal.
func (d *Deps) handleAccountSubscriptionBoxAdd(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	customer, _ := auth.CustomerFromContext(ctx)

	key, ok := parseBoxKey(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	variantID, err := uuid.Parse(r.PostFormValue("variant_id"))
	if err != nil {
		Error(w, r, app.ErrVariantNotFound)
		return
	}
	planID, err := uuid.Parse(r.PostFormValue("plan_id"))
	if err != nil {
		Error(w, r, app.ErrSubscriptionPlanNotFound)
		return
	}
	quantity, err := strconv.Atoi(r.PostFormValue("quantity"))
	if err != nil {
		Error(w, r, app.ErrInvalidQuantity)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.AddToBox(ctx, tx, customer.ID, app.AddToBoxParams{
			Key: key, VariantID: variantID, PlanID: planID, Quantity: quantity,
		}, customerActor(r))
		if txErr != nil {
			return txErr
		}
		_, txErr = d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionChangedArgsFrom(app.AddedNotice(sub)), nil)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	http.Redirect(w, r, "/account/subscriptions", http.StatusSeeOther)
}

// parseEditSubscriptionForm reads the Change form. A field left out is left
// as it is; one that is sent but does not parse is refused.
func parseEditSubscriptionForm(r *http.Request) (app.EditSubscriptionParams, error) {
	var p app.EditSubscriptionParams
	if v := r.PostFormValue("quantity"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return p, app.ErrInvalidQuantity
		}
		p.Quantity = &n
	}
	if v := r.PostFormValue("variant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return p, app.ErrVariantNotFound
		}
		p.VariantID = &id
	}
	if v := r.PostFormValue("plan_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			return p, app.ErrSubscriptionPlanNotFound
		}
		p.PlanID = &id
	}
	return p, nil
}

// subscriptionChoices is what the account page's forms offer: the /subscribe
// picker's coffees and the active plans.
func (d *Deps) subscriptionChoices(ctx context.Context, tx pgx.Tx) ([]storefront.SubscriptionChoice, []domain.SubscriptionPlan, error) {
	variants, plans, err := d.loadSubscribeCatalog(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	choices := make([]storefront.SubscriptionChoice, len(variants))
	for i, v := range variants {
		choices[i] = storefront.SubscriptionChoice{
			VariantID:    v.variantID,
			ProductID:    v.productID,
			ProductTitle: v.ProductTitle,
			VariantLabel: v.VariantLabel,
			BasePrice:    v.BasePrice,
		}
	}
	return choices, plans, nil
}

// hasChangeableSubscription reports whether any subscription shows a Change
// or Add form: an active or paused one.
func hasChangeableSubscription(subs []domain.Subscription) bool {
	for _, s := range subs {
		if s.Status == domain.SubscriptionStatusActive || s.Status == domain.SubscriptionStatusPaused {
			return true
		}
	}
	return false
}
