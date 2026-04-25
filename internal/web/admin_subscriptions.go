package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	mediapkg "github.com/dukerupert/hiri/internal/platform/media"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

func (d *Deps) handleAdminSubscriptionList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	statusFilter := r.URL.Query().Get("status")
	searchQuery := strings.TrimSpace(r.URL.Query().Get("q"))
	sortParam := r.URL.Query().Get("sort")
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	var sort store.SubscriptionSort
	switch sortParam {
	case "next_order_asc", "next_order_desc", "created_asc", "created_desc":
		sort = store.SubscriptionSort(sortParam)
	default:
		sort = store.SubscriptionSortCreatedDesc
	}

	perPage := 25
	filter := store.SubscriptionFilter{
		CustomerQuery: searchQuery,
		Sort:          sort,
		Limit:         perPage + 1,
		Offset:        (page - 1) * perPage,
	}

	if statusFilter != "" {
		s := domain.SubscriptionStatus(statusFilter)
		filter.Status = &s
	}

	var subscriptions []domain.Subscription
	var enriched []admin.EnrichedSubscription

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		subscriptions, txErr = d.SubscriptionService.ListSubscriptions(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}

		planNames := map[uuid.UUID]string{}
		customerInfo := map[uuid.UUID]struct {
			Name  string
			Email string
		}{}

		for _, sub := range subscriptions {
			if _, ok := planNames[sub.PlanID]; !ok {
				if plan, pErr := d.SubscriptionService.GetPlan(ctx, tx, sub.PlanID); pErr == nil {
					planNames[sub.PlanID] = plan.Name
				} else {
					planNames[sub.PlanID] = ""
				}
			}
			if _, ok := customerInfo[sub.CustomerID]; !ok {
				if cust, cErr := d.CustomerService.GetCustomer(ctx, tx, sub.CustomerID); cErr == nil {
					name := strings.TrimSpace(cust.FirstName + " " + cust.LastName)
					if name == "" && cust.CompanyName != nil {
						name = *cust.CompanyName
					}
					customerInfo[sub.CustomerID] = struct {
						Name  string
						Email string
					}{Name: name, Email: cust.Email}
				}
			}
		}

		enriched = make([]admin.EnrichedSubscription, len(subscriptions))
		for i, sub := range subscriptions {
			info := customerInfo[sub.CustomerID]
			enriched[i] = admin.EnrichedSubscription{
				Subscription:  sub,
				CustomerName:  info.Name,
				CustomerEmail: info.Email,
				PlanName:      planNames[sub.PlanID],
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(enriched) > perPage
	if hasMore {
		enriched = enriched[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.SubscriptionListProps{
		Subscriptions: enriched,
		StatusFilter:  statusFilter,
		SearchQuery:   searchQuery,
		Sort:          string(sort),
		Page:          page,
		PerPage:       perPage,
		HasMore:       hasMore,
		StaffName:     name,
		StaffRole:     role,
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
	var product *domain.Product
	var variant *domain.Variant
	var shippingAddr *domain.Address
	var thumbnailURL string
	var unitPrice int
	var hasUnitPrice bool
	var enrichedOrders []admin.EnrichedSubOrder
	var activity []domain.AuditEntry

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		sub, txErr = d.SubscriptionService.GetSubscriptionAsStaff(ctx, tx, id)
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
		variant, txErr = d.CatalogService.GetVariant(ctx, tx, sub.VariantID)
		if txErr != nil {
			return txErr
		}
		product, txErr = d.CatalogService.GetProduct(ctx, tx, variant.ProductID)
		if txErr != nil {
			return txErr
		}
		if media, mErr := d.CatalogService.ListProductMedia(ctx, tx, variant.ProductID); mErr == nil && len(media) > 0 {
			thumbnailURL = d.MediaConfig.ProductImageURL(media[0].R2Key, mediapkg.VariantCard)
		}
		if price, pErr := d.PricingService.GetBasePrice(ctx, tx, variant.ID, "USD"); pErr == nil {
			unit := price.Amount
			if plan.DiscountPct > 0 {
				unit = unit - (unit*plan.DiscountPct)/100
			}
			unitPrice = unit
			hasUnitPrice = true
		}
		if addr, aErr := d.CustomerService.GetAddressByIDAsStaff(ctx, tx, sub.ShippingAddressID); aErr == nil {
			shippingAddr = addr
		}
		subOrders, txErr := d.SubscriptionService.ListSubscriptionOrders(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		enrichedOrders = make([]admin.EnrichedSubOrder, len(subOrders))
		for i, so := range subOrders {
			enrichedOrders[i] = admin.EnrichedSubOrder{SubscriptionOrder: so}
			if order, oErr := d.OrderService.GetOrderAsStaff(ctx, tx, so.OrderID); oErr == nil {
				enrichedOrders[i].OrderNumber = order.Number
				enrichedOrders[i].OrderTotal = order.Total
				enrichedOrders[i].OrderStatus = order.Status
			}
		}
		activity, txErr = d.AuditQueryService.ListByResource(ctx, tx, "subscription", id)
		if txErr != nil {
			return txErr
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, app.ErrSubscriptionNotFound) {
			http.NotFound(w, r)
			return
		}
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.SubscriptionShowProps{
		Subscription:    sub,
		Plan:            plan,
		Customer:        customer,
		Product:         product,
		Variant:         variant,
		ThumbnailURL:    thumbnailURL,
		UnitPrice:       unitPrice,
		HasUnitPrice:    hasUnitPrice,
		ShippingAddress: shippingAddr,
		Orders:          enrichedOrders,
		Activity:        activity,
		Flash:           r.URL.Query().Get("flash"),
		StaffName:       name,
		StaffRole:       role,
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
		_, txErr := d.SubscriptionService.PauseSubscription(ctx, tx, id, nil, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Subscription+paused", http.StatusSeeOther)
}

func (d *Deps) handleAdminSubscriptionResume(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.ResumeSubscription(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Subscription+resumed", http.StatusSeeOther)
}

func (d *Deps) handleAdminSubscriptionCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.CancelSubscription(ctx, tx, id, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Subscription+cancelled", http.StatusSeeOther)
}
