package web

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminSubscriptionList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	perPage := 25
	filter := store.SubscriptionFilter{
		Limit:  perPage + 1,
		Offset: (page - 1) * perPage,
	}

	if statusFilter != "" {
		s := domain.SubscriptionStatus(statusFilter)
		filter.Status = &s
	}

	var subscriptions []domain.Subscription

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		subscriptions, txErr = d.SubscriptionService.ListSubscriptions(ctx, tx, filter)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(subscriptions) > perPage
	if hasMore {
		subscriptions = subscriptions[:perPage]
	}

	props := admin.SubscriptionListProps{
		Subscriptions: subscriptions,
		StatusFilter:  statusFilter,
		Page:          page,
		PerPage:       perPage,
		HasMore:       hasMore,
	}

	if IsHTMX(r) {
		admin.SubscriptionListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.SubscriptionList(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminSubscriptionShow(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var sub *domain.Subscription
	var plan *domain.SubscriptionPlan
	var customer *domain.Customer
	var orders []domain.SubscriptionOrder

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		sub, txErr = d.SubscriptionService.GetSubscription(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		plan, txErr = d.SubscriptionService.GetPlan(ctx, tx, sub.PlanID)
		if txErr != nil {
			return txErr
		}
		customer, txErr = d.CustomerService.GetCustomer(ctx, tx, sub.CustomerID)
		if txErr != nil {
			return txErr
		}
		orders, txErr = d.SubscriptionService.ListSubscriptionOrders(ctx, tx, id)
		return txErr
	})
	if err != nil {
		if errors.Is(err, app.ErrSubscriptionNotFound) {
			http.NotFound(w, r)
			return
		}
		Error(w, r, err)
		return
	}

	props := admin.SubscriptionShowProps{
		Subscription: sub,
		Plan:         plan,
		Customer:     customer,
		Orders:       orders,
	}

	if IsHTMX(r) {
		admin.SubscriptionShowContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.SubscriptionShow(props).Render(ctx, w) //nolint:errcheck
}

func (d *Deps) handleAdminSubscriptionPause(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.PauseSubscription(ctx, tx, id, nil, devActor())
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String(), http.StatusSeeOther)
}

func (d *Deps) handleAdminSubscriptionResume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.ResumeSubscription(ctx, tx, id, devActor())
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String(), http.StatusSeeOther)
}

func (d *Deps) handleAdminSubscriptionCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.CancelSubscription(ctx, tx, id, devActor())
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String(), http.StatusSeeOther)
}
