package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

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

	q := r.URL.Query()
	search := strings.TrimSpace(q.Get("q"))

	page := 1
	if p, err := strconv.Atoi(q.Get("page")); err == nil && p > 0 {
		page = p
	}

	statusFilter := normalizeSubscriptionStatus(q.Get("status"))
	planFilter := normalizeUUIDParam(q.Get("plan"))
	productFilter := normalizeUUIDParam(q.Get("product"))
	dueRange := normalizeSubscriptionDue(q.Get("due"))
	dueFrom, dueTo := subscriptionDueBounds(dueRange, q.Get("from"), q.Get("to"), d.MerchantTZ, time.Now())
	sort := normalizeSubscriptionSort(q.Get("sort"))

	perPage := 25
	filter := store.SubscriptionFilter{
		CustomerQuery: search,
		Sort:          sort,
		NextOrderFrom: dueFrom,
		NextOrderTo:   dueTo,
		Limit:         perPage + 1,
		Offset:        (page - 1) * perPage,
	}
	applySubscriptionFilters(statusFilter, planFilter, productFilter, &filter)

	var subscriptions []domain.Subscription
	var enriched []admin.EnrichedSubscription
	var suggestions []domain.Customer
	var plans []domain.SubscriptionPlan
	var products []admin.SubscriptionProductOption
	var statusCounts map[domain.SubscriptionStatus]int
	var totalCount int

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		subscriptions, txErr = d.SubscriptionService.ListSubscriptions(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		totalCount, txErr = d.SubscriptionService.CountSubscriptions(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		// Pill counts vary only the status dimension, so each number is exactly
		// what clicking that pill would show under the filters already applied.
		statusCounts, txErr = d.SubscriptionService.SubscriptionStatusCounts(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		plans, txErr = d.SubscriptionService.ListPlans(ctx, tx)
		if txErr != nil {
			return txErr
		}
		subscribed, txErr := d.SubscriptionService.ListSubscribedProducts(ctx, tx)
		if txErr != nil {
			return txErr
		}
		products = make([]admin.SubscriptionProductOption, len(subscribed))
		for i, p := range subscribed {
			products[i] = admin.SubscriptionProductOption{ID: p.ID.String(), Title: p.Title, Count: p.Count}
		}

		planNames := map[uuid.UUID]string{}
		// Keyed by variant, since that's what a subscription points at; two
		// variants of the same coffee resolve to the same title.
		variantTitles := map[uuid.UUID]string{}
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
			if _, ok := variantTitles[sub.VariantID]; !ok {
				title := ""
				if variant, vErr := d.CatalogService.GetVariant(ctx, tx, sub.VariantID); vErr == nil {
					if prod, prErr := d.CatalogService.GetProduct(ctx, tx, variant.ProductID); prErr == nil {
						title = prod.Title
					}
				}
				variantTitles[sub.VariantID] = title
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
				ProductTitle:  variantTitles[sub.VariantID],
			}
		}

		// Only reach for fuzzy matches when an actual search term found nothing
		// — otherwise the operator is just paging an empty filter combination.
		// Suggestions are customers: the common miss on this page is a
		// half-remembered name, and every customer links to their subscriptions.
		if len(enriched) == 0 && search != "" {
			suggestions, txErr = d.CustomerService.SuggestCustomers(ctx, tx, search, 5)
			if txErr != nil {
				return txErr
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

	counts := map[string]int{}
	all := 0
	for status, n := range statusCounts {
		counts[string(status)] = n
		all += n
	}
	counts[""] = all

	name, role := staffNameRole(r)
	props := admin.SubscriptionListProps{
		Subscriptions: enriched,
		Suggestions:   suggestions,
		Plans:         plans,
		Products:      products,
		StatusFilter:  statusFilter,
		PlanFilter:    planFilter,
		ProductFilter: productFilter,
		Due:           dueRange,
		From:          minRawDate(q.Get("from"), dueRange),
		To:            minRawDate(q.Get("to"), dueRange),
		SearchQuery:   search,
		Sort:          string(sort),
		Counts:        counts,
		TotalCount:    totalCount,
		Page:          page,
		PerPage:       perPage,
		HasMore:       hasMore,
		MerchantTZ:    d.MerchantTZ,
		Now:           time.Now(),
		StaffName:     name,
		StaffRole:     role,
	}

	if IsHTMX(r) {
		admin.SubscriptionListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.SubscriptionList(props).Render(ctx, w) //nolint:errcheck
}

// normalizeSubscriptionStatus clamps ?status= to the subscription statuses we
// know. Anything else falls back to "all" rather than erroring — a stale or
// hand-edited URL should show the operator a list, not a 400.
func normalizeSubscriptionStatus(v string) string {
	switch v {
	case "active", "paused", "past_due", "cancelled", "expired":
		return v
	default:
		return ""
	}
}

// normalizeSubscriptionSort clamps ?sort= to the closed sort enum, so no
// caller can reach a raw identifier into the ORDER BY.
func normalizeSubscriptionSort(v string) store.SubscriptionSort {
	switch store.SubscriptionSort(v) {
	case store.SubscriptionSortCreatedAsc,
		store.SubscriptionSortNextOrderAsc,
		store.SubscriptionSortNextOrderDesc,
		store.SubscriptionSortCustomerAsc,
		store.SubscriptionSortCustomerDesc:
		return store.SubscriptionSort(v)
	default:
		return store.SubscriptionSortCreatedDesc
	}
}

// normalizeSubscriptionDue clamps ?due= to a known next-order preset.
// "custom" means the from/to fields drive the bounds instead.
func normalizeSubscriptionDue(v string) string {
	switch v {
	case "overdue", "today", "7d", "30d", "custom":
		return v
	default:
		return ""
	}
}

// normalizeUUIDParam echoes back a query param only when it parses as a UUID,
// so a malformed id filters nothing instead of erroring the page.
func normalizeUUIDParam(v string) string {
	if _, err := uuid.Parse(v); err != nil {
		return ""
	}
	return v
}

// applySubscriptionFilters translates the normalized query params onto a store
// filter. Kept separate from the handler so the pill counts can re-apply the
// same translation with one dimension varied.
func applySubscriptionFilters(status, plan, product string, f *store.SubscriptionFilter) {
	f.Status = nil
	f.PlanID = nil
	f.ProductID = nil

	if status != "" {
		f.Status = ptrTo(domain.SubscriptionStatus(status))
	}
	if id, err := uuid.Parse(plan); err == nil {
		f.PlanID = &id
	}
	if id, err := uuid.Parse(product); err == nil {
		f.ProductID = &id
	}
}

// subscriptionDueBounds resolves the next-order filter into a [from, to] pair.
//
// Boundaries are computed in the merchant's timezone, not UTC: "due today" has
// to mean the shop's today, or a staffer in Denver looking at a Los Angeles
// shop sees renewals appear and vanish hours early. `now` is a parameter so the
// behaviour is testable without freezing the clock.
//
// Unlike the orders list, the presets here look forward — a subscription's next
// order is a future date. "Overdue" is the exception and the most useful one:
// anything whose next order should already have run.
func subscriptionDueBounds(rangeKey, fromRaw, toRaw string, tz *time.Location, now time.Time) (*time.Time, *time.Time) {
	if tz == nil {
		tz = time.UTC
	}
	local := now.In(tz)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
	endOfDay := func(t time.Time) time.Time {
		return t.AddDate(0, 0, 1).Add(-time.Nanosecond)
	}

	switch rangeKey {
	case "overdue":
		return nil, ptrTo(startOfToday.Add(-time.Nanosecond))
	case "today":
		return ptrTo(startOfToday), ptrTo(endOfDay(startOfToday))
	case "7d":
		return ptrTo(startOfToday), ptrTo(endOfDay(startOfToday.AddDate(0, 0, 6)))
	case "30d":
		return ptrTo(startOfToday), ptrTo(endOfDay(startOfToday.AddDate(0, 0, 29)))
	case "custom":
		var from, to *time.Time
		if t, err := time.ParseInLocation("2006-01-02", fromRaw, tz); err == nil {
			from = ptrTo(t)
		}
		if t, err := time.ParseInLocation("2006-01-02", toRaw, tz); err == nil {
			// The user means the whole of the end day, so bound at its last
			// instant rather than midnight — otherwise "to: today" silently
			// excludes everything due later today.
			to = ptrTo(endOfDay(t))
		}
		return from, to
	}
	return nil, nil
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

		// Option ordering metadata, shared with the catalog/pricing pages, so the
		// variant dropdown reads by size then grind (12oz before 3lb, each grind
		// group in its configured order) instead of the catalog's arbitrary order.
		ordering, ordErr := d.loadProductOptionOrdering(ctx, tx, product.ID)
		if ordErr != nil {
			return ordErr
		}

		currentVariantLabel, txErr = buildVariantLabel(ctx, tx, d, variant.ID, ordering.labels)
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
		keys := make(map[uuid.UUID][]int, len(allVariants))
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
			vovs, vErr := d.CatalogService.ListVariantOptionValues(ctx, tx, sv.ID)
			if vErr != nil {
				return vErr
			}
			keys[sv.ID] = ordering.sortKey(vovs)
			label := ordering.label(vovs) // size-first, e.g. "12oz / Drip"
			if label == "" {
				label = sv.SKU
			}
			siblingVariants = append(siblingVariants, admin.SiblingVariant{
				ID:    sv.ID,
				Label: label,
				Price: price.Amount,
			})
		}
		sortVariantsByKey(siblingVariants, keys, func(sv admin.SiblingVariant) (uuid.UUID, string) {
			return sv.ID, sv.Label
		})
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
		AvailablePlans:  availablePlans,
		Customer:        customer,
		Product:         product,
		Variant:         variant,
		VariantLabel:    currentVariantLabel,
		SiblingVariants: siblingVariants,
		ThumbnailURL:    thumbnailURL,
		UnitPrice:       unitPrice,
		HasUnitPrice:    hasUnitPrice,
		ShippingAddress: shippingAddr,
		Orders:          enrichedOrders,
		Activity:        activity,
		Flash:           r.URL.Query().Get("flash"),
		MerchantTZ:      d.MerchantTZ,
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
