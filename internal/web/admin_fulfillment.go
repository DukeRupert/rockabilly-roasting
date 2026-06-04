package web

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// fulfillmentNeedsActionStatuses is the union of fulfillment states that show
// up on the Needs Action tab — anything pre-handoff that still wants staff
// attention. Mirrors store.CountFulfillmentViews' NeedsAction bucket so the
// tab count and the visible rows agree.
var fulfillmentNeedsActionStatuses = []domain.FulfillmentStatus{
	domain.FulfillmentStatusUnfulfilled,
	domain.FulfillmentStatusPartiallyFulfilled,
	domain.FulfillmentStatusFulfilled,
	domain.FulfillmentStatusReadyForPickup,
}

var fulfillmentShippedStatuses = []domain.FulfillmentStatus{
	domain.FulfillmentStatusShipped,
	domain.FulfillmentStatusPartiallyShipped,
}

var fulfillmentDeliveredStatuses = []domain.FulfillmentStatus{
	domain.FulfillmentStatusDelivered,
	domain.FulfillmentStatusPartiallyDelivered,
}

// normalizeFulfillmentView returns the canonical view key for the fulfillment
// queue, defaulting to "needs_action" when unrecognized or empty.
func normalizeFulfillmentView(v string) string {
	switch v {
	case "needs_action", "ready_to_ship", "shipped", "delivered", "all":
		return v
	default:
		return "needs_action"
	}
}

// applyFulfillmentViewFilter mutates the filter to match the chosen view's
// bucket. Every view excludes unconfirmed orders — they don't belong in a
// pack-and-ship queue. The action views also drop cancelled/refunded orders
// (whose fulfillment_status often lingers at 'unfulfilled' from pre-cancel
// state) so staff don't see queue entries they can't act on.
func applyFulfillmentViewFilter(view string, f *store.OrderFilter) {
	f.ExcludeUnconfirmed = true
	switch view {
	case "needs_action":
		f.FulfillmentStatuses = fulfillmentNeedsActionStatuses
		f.ExcludeCancelledRefunded = true
	case "ready_to_ship":
		s := domain.FulfillmentStatusFulfilled
		f.FulfillmentStatus = &s
		f.ExcludeCancelledRefunded = true
	case "shipped":
		f.FulfillmentStatuses = fulfillmentShippedStatuses
	case "delivered":
		f.FulfillmentStatuses = fulfillmentDeliveredStatuses
	case "all":
		// no fulfillment-status restriction
	}
}

// isFulfillmentActionView reports whether the view groups rows by shipping
// method into per-method workspace sections (with bulk action bars) instead
// of rendering a flat table. The action views are the daily working queues
// where batch operations matter.
func isFulfillmentActionView(view string) bool {
	return view == "needs_action" || view == "ready_to_ship"
}

// handleAdminFulfillmentList renders the retail (direct-to-consumer)
// fulfillment queue.
func (d *Deps) handleAdminFulfillmentList(w http.ResponseWriter, r *http.Request) {
	d.renderFulfillmentList(w, r, domain.OrderChannelRetail)
}

// handleAdminWholesaleFulfillmentList renders the wholesale fulfillment queue.
// It shares the retail queue's machinery — same view buckets, grouping, and
// batch actions — but scopes every query to the wholesale channel and links
// back to /admin/wholesale/fulfillment.
func (d *Deps) handleAdminWholesaleFulfillmentList(w http.ResponseWriter, r *http.Request) {
	d.renderFulfillmentList(w, r, domain.OrderChannelWholesale)
}

// renderFulfillmentList is the shared body for both fulfillment queues. The
// channel scopes the listing and the per-tab counts; it also selects the base
// path, page title, and active nav entry so the two queues stay independent.
func (d *Deps) renderFulfillmentList(w http.ResponseWriter, r *http.Request, channel domain.OrderChannel) {
	ctx := r.Context()

	basePath := "/admin/fulfillment"
	title := "Fulfillment"
	if channel == domain.OrderChannelWholesale {
		basePath = "/admin/wholesale/fulfillment"
		title = "Wholesale fulfillment"
	}

	view := normalizeFulfillmentView(r.URL.Query().Get("view"))
	pageStr := r.URL.Query().Get("page")

	page := 1
	if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
		page = p
	}

	// Action views render the whole queue grouped by ship-method, so they ask
	// for a larger window than the post-action flat tables.
	perPage := 25
	if isFulfillmentActionView(view) {
		perPage = 100
	}

	filter := store.OrderFilter{
		Channel: &channel,
		Limit:   perPage + 1,
		Offset:  (page - 1) * perPage,
	}
	applyFulfillmentViewFilter(view, &filter)

	var orders []domain.Order
	var rows []admin.OrderRow
	var totalCount int
	var counts store.FulfillmentViewCounts
	var failedLabelIDs map[uuid.UUID]bool

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		orders, txErr = d.OrderService.ListOrders(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		totalCount, txErr = d.OrderService.CountOrders(ctx, tx, filter)
		if txErr != nil {
			return txErr
		}
		counts, txErr = d.OrderService.CountFulfillmentViews(ctx, tx, &channel)
		if txErr != nil {
			return txErr
		}

		rows = make([]admin.OrderRow, 0, len(orders))
		orderIDs := make([]uuid.UUID, 0, len(orders))
		for _, o := range orders {
			row := admin.OrderRow{
				Order:        o,
				CustomerName: "Guest",
				AccountType:  domain.AccountTypeRetail,
			}
			if o.CustomerID != nil {
				c, cErr := d.CustomerService.GetCustomer(ctx, tx, *o.CustomerID)
				if cErr != nil && !errors.Is(cErr, app.ErrCustomerNotFound) {
					return cErr
				}
				if c != nil {
					row.CustomerName = customerDisplayName(c)
					row.CustomerEmail = c.Email
					row.AccountType = c.AccountType
				}
			}
			rows = append(rows, row)
			orderIDs = append(orderIDs, o.ID)
		}

		failedLabelIDs, txErr = d.FulfillmentService.ListOrdersWithFailedLabelAttempts(ctx, tx, orderIDs)
		if txErr != nil {
			return txErr
		}
		for i := range rows {
			if failedLabelIDs[rows[i].Order.ID] {
				rows[i].LabelFailed = true
			}
		}
		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	hasMore := len(rows) > perPage
	if hasMore {
		rows = rows[:perPage]
	}

	name, role := staffNameRole(r)
	props := admin.FulfillmentListProps{
		BasePath: basePath,
		Title:    title,
		View:     view,
		Counts: admin.FulfillmentViewCounts{
			NeedsAction: counts.NeedsAction,
			ReadyToShip: counts.ReadyToShip,
			Shipped:     counts.Shipped,
			Delivered:   counts.Delivered,
			All:         counts.All,
		},
		TotalCount:  totalCount,
		Page:        page,
		PerPage:     perPage,
		HasMore:     hasMore,
		MerchantTZ:  d.MerchantTZ,
		Now:         time.Now(),
		StaffName:   name,
		StaffRole:   role,
		BatchResult: parseBatchResultQuery(r.URL.Query()),
	}

	if isFulfillmentActionView(view) {
		props.Groups = admin.GroupRowsByShippingMethod(rows)
	} else {
		props.Rows = rows
	}

	if IsHTMX(r) {
		admin.FulfillmentListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.FulfillmentList(props).Render(ctx, w) //nolint:errcheck
}

// parseBatchResultQuery decodes the structured ?batch_result=… params written
// by the bulk-verb handlers' redirect target. Returns nil when the params are
// absent or any required piece is malformed — the banner silently disappears
// rather than rendering half-truths. The fail_ids and fail_reasons lists are
// expected to be the same length; mismatched lists drop down to the shorter
// slice so we never indexed-out-of-range a half-corrupted URL.
func parseBatchResultQuery(q url.Values) *admin.BatchActionResult {
	verb := q.Get("batch_result")
	if verb == "" {
		return nil
	}
	switch verb {
	case "ready-for-pickup", "picked-up", "out-for-delivery":
	default:
		return nil
	}
	ok, err := strconv.Atoi(q.Get("ok"))
	if err != nil || ok < 0 {
		return nil
	}
	failCount, err := strconv.Atoi(q.Get("fail"))
	if err != nil || failCount < 0 {
		return nil
	}

	result := &admin.BatchActionResult{
		Verb:      verb,
		Succeeded: ok,
		Truncated: q.Get("fail_truncated") == "1",
	}

	if failCount == 0 {
		return result
	}
	idParts := strings.Split(q.Get("fail_ids"), ",")
	reasonParts := strings.Split(q.Get("fail_reasons"), ",")
	n := len(idParts)
	if len(reasonParts) < n {
		n = len(reasonParts)
	}
	result.Failures = make([]admin.BatchActionFailure, 0, n)
	for i := 0; i < n; i++ {
		id, parseErr := uuid.Parse(strings.TrimSpace(idParts[i]))
		if parseErr != nil {
			continue
		}
		result.Failures = append(result.Failures, admin.BatchActionFailure{
			OrderID: id,
			Reason:  strings.TrimSpace(reasonParts[i]),
		})
	}
	return result
}
