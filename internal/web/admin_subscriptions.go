package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
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
		MerchantTZ:    d.MerchantTZ,
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
	var availablePlans []domain.SubscriptionPlan
	var customer *domain.Customer
	var product *domain.Product
	var variant *domain.Variant
	var shippingAddr *domain.Address
	var thumbnailURL string
	var unitPrice int
	var hasUnitPrice bool
	var enrichedOrders []admin.EnrichedSubOrder
	var activity []domain.AuditEntry
	var siblingVariants []admin.SiblingVariant
	var currentVariantLabel string

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
		availablePlans, txErr = d.SubscriptionService.ListActivePlans(ctx, tx)
		if txErr != nil {
			return txErr
		}
		// Ensure the current plan appears in the selector even if it's been deactivated.
		foundCurrent := false
		for _, p := range availablePlans {
			if p.ID == plan.ID {
				foundCurrent = true
				break
			}
		}
		if !foundCurrent {
			availablePlans = append(availablePlans, *plan)
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

		// Build option label map for the product so we can label sibling variants
		// the same way the order page does (e.g., "Whole Bean / 12oz").
		labels := map[uuid.UUID]string{}
		opts, oErr := d.CatalogService.ListProductOptions(ctx, tx, product.ID)
		if oErr != nil {
			return oErr
		}
		for _, opt := range opts {
			vals, vlErr := d.CatalogService.ListProductOptionValues(ctx, tx, opt.ID)
			if vlErr != nil {
				return vlErr
			}
			for _, val := range vals {
				labels[val.ID] = val.Value
			}
		}
		currentVariantLabel, txErr = buildVariantLabel(ctx, tx, d, variant.ID, labels)
		if txErr != nil {
			return txErr
		}

		// Sibling variants on the same product, including different price tiers
		// (e.g. 3lb → 12oz). Each option shows its current USD price so staff see
		// what the customer's future renewals will be charged. Archived variants
		// are skipped. Always include the current variant so the select shows the
		// active selection.
		allVariants, avErr := d.CatalogService.ListVariants(ctx, tx, product.ID)
		if avErr != nil {
			return avErr
		}
		for _, sv := range allVariants {
			if sv.ArchivedAt != nil && sv.ID != variant.ID {
				continue
			}
			price, pErr := d.PricingService.GetBasePrice(ctx, tx, sv.ID, "USD")
			if pErr != nil {
				if errors.Is(pErr, app.ErrPriceNotFound) {
					continue
				}
				return pErr
			}
			label, lblErr := buildVariantLabel(ctx, tx, d, sv.ID, labels)
			if lblErr != nil {
				return lblErr
			}
			if label == "" {
				label = sv.SKU
			}
			siblingVariants = append(siblingVariants, admin.SiblingVariant{
				ID:    sv.ID,
				Label: label,
				Price: price.Amount,
			})
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
		Subscription:        sub,
		Plan:                plan,
		AvailablePlans:      availablePlans,
		Customer:            customer,
		Product:             product,
		Variant:             variant,
		VariantLabel:        currentVariantLabel,
		SiblingVariants:     siblingVariants,
		ThumbnailURL:        thumbnailURL,
		UnitPrice:           unitPrice,
		HasUnitPrice:        hasUnitPrice,
		ShippingAddress:     shippingAddr,
		Orders:              enrichedOrders,
		Activity:            activity,
		Flash:               r.URL.Query().Get("flash"),
		MerchantTZ:          d.MerchantTZ,
		StaffName:           name,
		StaffRole:           role,
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

// handleAdminSubscriptionDunningAck clears a past-due subscription from the
// dashboard's Urgent band. Invoked from the dashboard row, so it redirects back
// there; htmx (hx-boost) follows the redirect and re-renders with the row gone
// and the urgent count decremented.
func (d *Deps) handleAdminSubscriptionDunningAck(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.SubscriptionService.AcknowledgeDunning(ctx, tx, id, staffActor(r))
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/", http.StatusSeeOther)
}

// handleAdminSubscriptionRetry triggers an immediate renewal charge on a
// past-due subscription — the staff counterpart to the customer's retry, for
// when a customer calls in after sorting their card. Enqueues the renewal job;
// the worker runs the same path the scheduler uses.
func (d *Deps) handleAdminSubscriptionRetry(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	var flash string
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.GetSubscriptionAsStaff(ctx, tx, id)
		if txErr != nil {
			return txErr
		}
		if sub.Status != domain.SubscriptionStatusPastDue {
			flash = "Subscription+is+not+past+due"
			return nil
		}
		// ByArgs unique on the subscription ID prevents stacking duplicate
		// charge attempts (double-click, or a race with the scheduler).
		if _, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionRenewalArgs{
			SubscriptionID: sub.ID,
		}, &river.InsertOpts{UniqueOpts: river.UniqueOpts{ByArgs: true}}); txErr != nil {
			return txErr
		}
		flash = "Renewal+charge+queued"
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash="+flash, http.StatusSeeOther)
}

// handleAdminSubscriptionGrandfatherShipping toggles a subscription's
// free-renewal-shipping exception. The form's "enabled" field carries the
// desired state ("true"/"false") so the action is idempotent regardless of
// double-submits — it sets an absolute value rather than flipping.
func (d *Deps) handleAdminSubscriptionGrandfatherShipping(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	enabled := r.FormValue("enabled") == "true"

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.SetShippingGrandfathered(ctx, tx, id, enabled, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	flash := "Free-shipping+exception+removed"
	if enabled {
		flash = "Free-shipping+exception+applied"
	}
	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash="+flash, http.StatusSeeOther)
}

func (d *Deps) handleAdminSubscriptionCancel(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		sub, txErr := d.SubscriptionService.CancelSubscription(ctx, tx, id, staffActor(r))
		if txErr != nil {
			return txErr
		}
		if _, txErr := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionCancelledArgs{
			SubscriptionID: sub.ID,
			CustomerID:     sub.CustomerID,
		}, nil); txErr != nil {
			return txErr
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Subscription+cancelled", http.StatusSeeOther)
}

func (d *Deps) handleAdminSubscriptionPlanUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	newPlanID, err := uuid.Parse(r.FormValue("plan_id"))
	if err != nil {
		Error(w, r, app.ErrSubscriptionPlanNotFound)
		return
	}

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.ChangePlan(ctx, tx, id, newPlanID, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Plan+updated", http.StatusSeeOther)
}

func (d *Deps) handleAdminSubscriptionVariantUpdate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}

	if err := r.ParseForm(); err != nil {
		Error(w, r, err)
		return
	}
	newVariantID, err := uuid.Parse(r.FormValue("variant_id"))
	if err != nil {
		Error(w, r, app.ErrVariantNotFound)
		return
	}
	allowPriceChange := r.FormValue("allow_price_change") == "true"

	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		_, txErr := d.SubscriptionService.ChangeVariant(ctx, tx, id, newVariantID, allowPriceChange, staffActor(r))
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	http.Redirect(w, r, "/admin/subscriptions/"+id.String()+"?flash=Variant+updated", http.StatusSeeOther)
}
