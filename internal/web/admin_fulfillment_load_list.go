package web

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
)

// loadListRosterCap bounds how many delivery orders one load sheet covers.
// A delivery run is a van, not a warehouse — a queue past this size means
// something upstream is wrong (a stuck fulfillment state, a missed run), and
// a truncated sheet is safer than a 900-row page that times out. Well above
// any real day's route.
const loadListRosterCap = 500

// loadListScope resolves the ?channel= param into a channel filter. nil means
// both channels, and it is the default: the retail and wholesale queues are
// separate because staff pack them separately, but one van makes one run, so
// the load list and the route planned off it fold the channels together unless
// staff deliberately narrow the scope.
//
// An unrecognized value falls back to nil rather than 400 — a mangled URL
// should show more of the run than expected, never less.
func loadListScope(r *http.Request) *domain.OrderChannel {
	switch r.URL.Query().Get("channel") {
	case string(domain.OrderChannelWholesale):
		c := domain.OrderChannelWholesale
		return &c
	case string(domain.OrderChannelRetail):
		c := domain.OrderChannelRetail
		return &c
	}
	return nil
}

// loadListScopeParam is the query-param form of a scope, "" for both. Threaded
// into the templates so the totals fragment and print sheet re-scope the way
// the page did.
func loadListScopeParam(c *domain.OrderChannel) string {
	if c == nil {
		return ""
	}
	return string(*c)
}

// loadListChannelLabel is the human name shown on the printed masthead.
func loadListChannelLabel(c *domain.OrderChannel) string {
	if c == nil {
		return "All channels"
	}
	if *c == domain.OrderChannelWholesale {
		return "Wholesale"
	}
	return "Retail"
}

// parseLoadListSelection reads the checked-order selection out of the query
// string. explicit reports whether the caller submitted the load-list form at
// all: the form always carries scope=selection, so an empty ids list from a
// submitted form means "nothing is checked", while an absent scope means the
// page was opened fresh and everything should default to checked. Without that
// distinction, unchecking the last row would silently show the full load again.
//
// Unparseable ids are dropped rather than erroring — a mangled URL should
// narrow the sheet, never blank the page mid-load-out.
func parseLoadListSelection(q url.Values) (ids []uuid.UUID, explicit bool) {
	explicit = q.Get("scope") == "selection"
	raw := q["ids"]
	ids = make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(s)
		if err != nil {
			continue
		}
		ids = append(ids, id)
	}
	return ids, explicit
}

// loadDeliveryLoadList assembles both halves of the load list: the roster of
// local-delivery orders currently waiting in the queue, and the per-product
// pound rollup over whichever of them are selected.
//
// The roster is always the full delivery queue so staff can re-check a row
// they unchecked; only the rollup narrows to the selection. When nothing is
// selected the aggregate query is skipped entirely — an empty OrderIDs filter
// means "unconstrained" at the store layer, which would show the whole load
// back to a driver who just unchecked everything.
func (d *Deps) loadDeliveryLoadList(
	ctx context.Context,
	tx pgx.Tx,
	channel *domain.OrderChannel,
	selected []uuid.UUID,
	explicit bool,
) ([]admin.LoadListOrder, []domain.DeliveryLoadLine, error) {
	localDelivery := domain.ShippingMethodLocalDelivery

	rosterFilter := store.OrderFilter{
		Channel:                  channel,
		ShippingMethod:           &localDelivery,
		FulfillmentStatuses:      fulfillmentNeedsActionStatuses,
		ExcludeUnconfirmed:       true,
		ExcludeCancelledRefunded: true,
		Limit:                    loadListRosterCap,
	}
	orders, err := d.OrderService.ListOrders(ctx, tx, rosterFilter)
	if err != nil {
		return nil, nil, err
	}

	// A selection only counts if it names an order actually on the roster —
	// stale ids from a bookmarked print URL shouldn't inflate the totals.
	selectedSet := make(map[uuid.UUID]bool, len(selected))
	for _, id := range selected {
		selectedSet[id] = true
	}

	roster := make([]admin.LoadListOrder, 0, len(orders))
	countedIDs := make([]uuid.UUID, 0, len(orders))
	for _, o := range orders {
		row := admin.LoadListOrder{
			OrderRow: admin.OrderRow{
				Order:        o,
				CustomerName: "Guest",
				AccountType:  domain.AccountTypeRetail,
			},
			// Fresh page load: everything on the run is on the van.
			Selected: !explicit || selectedSet[o.ID],
		}
		if o.CustomerID != nil {
			c, cErr := d.CustomerService.GetCustomer(ctx, tx, *o.CustomerID)
			if cErr != nil && !errors.Is(cErr, app.ErrCustomerNotFound) {
				return nil, nil, cErr
			}
			if c != nil {
				row.CustomerName = customerDisplayName(c)
				row.CustomerEmail = c.Email
				row.AccountType = c.AccountType
			}
		}
		if row.Selected {
			countedIDs = append(countedIDs, o.ID)
		}
		roster = append(roster, row)
	}

	// Flag wholesale accounts past terms — same hold signal the main queue
	// shows, so a driver doesn't load coffee for an account on stop-ship.
	// Keyed off each order's own channel rather than the page scope: a combined
	// run carries wholesale stops even though it isn't a wholesale-scoped list,
	// and those are exactly the ones that can be on stop-ship.
	customerIDs := make([]uuid.UUID, 0, len(roster))
	seen := make(map[uuid.UUID]bool, len(roster))
	for i := range roster {
		if roster[i].Order.Channel != domain.OrderChannelWholesale {
			continue
		}
		if cid := roster[i].Order.CustomerID; cid != nil && !seen[*cid] {
			seen[*cid] = true
			customerIDs = append(customerIDs, *cid)
		}
	}
	if len(customerIDs) > 0 {
		pastDue, pdErr := d.OrderService.PastDueCustomerFlags(ctx, tx, customerIDs)
		if pdErr != nil {
			return nil, nil, pdErr
		}
		for i := range roster {
			if cid := roster[i].Order.CustomerID; cid != nil && pastDue[*cid] {
				roster[i].AccountPastDue = true
			}
		}
	}

	if len(countedIDs) == 0 {
		return roster, nil, nil
	}

	loadFilter := rosterFilter
	loadFilter.OrderIDs = countedIDs
	lines, err := d.OrderService.ListDeliveryLoad(ctx, tx, loadFilter)
	if err != nil {
		return nil, nil, err
	}
	return roster, lines, nil
}

// handleAdminLoadList renders the delivery load list: per-product pound totals
// across the delivery orders waiting to go out, over a roster staff can adjust
// for outliers. GET /admin/fulfillment/load-list.
//
// It is its own page rather than a tab on either fulfillment queue because it
// defaults to both channels — the queues are split by how staff pack, the load
// list is organised around what goes on the van. ?channel=retail|wholesale
// narrows it for the days when only one channel is going out.
func (d *Deps) handleAdminLoadList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	channel := loadListScope(r)
	selected, explicit := parseLoadListSelection(r.URL.Query())

	var roster []admin.LoadListOrder
	var lines []domain.DeliveryLoadLine
	var originAddress string

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		roster, lines, txErr = d.loadDeliveryLoadList(ctx, tx, channel, selected, explicit)
		if txErr != nil {
			return txErr
		}
		// Only to label the "End of run" field's default. A missing origin is
		// a settings problem for PlanRoute to report at plan time, not a reason
		// to fail rendering the load list.
		originAddress, txErr = d.RouteService.OriginAddress(ctx, tx)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.LoadListProps{
		Channel:       loadListScopeParam(channel),
		Lines:         lines,
		Orders:        roster,
		OriginAddress: originAddress,
		MerchantTZ:    d.MerchantTZ,
		Now:           time.Now(),
		StaffName:     name,
		StaffRole:     role,
	}

	if IsHTMX(r) {
		admin.LoadListPageContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.LoadListPage(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminFulfillmentLoadListTotals re-renders just the totals panel after
// staff check or uncheck an order. GET /admin/fulfillment/load-list/totals.
// Keeping the arithmetic server-side means the browser never computes a number
// a driver is going to trust.
func (d *Deps) handleAdminFulfillmentLoadListTotals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	channel := loadListScope(r)
	selected, explicit := parseLoadListSelection(r.URL.Query())

	var roster []admin.LoadListOrder
	var lines []domain.DeliveryLoadLine
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		roster, lines, txErr = d.loadDeliveryLoadList(ctx, tx, channel, selected, explicit)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	admin.LoadListTotals(admin.LoadListProps{ //nolint:errcheck
		Channel:    loadListScopeParam(channel),
		Lines:      lines,
		Orders:     roster,
		MerchantTZ: d.MerchantTZ,
		Now:        time.Now(),
	}).Render(ctx, w)
}

// handleAdminFulfillmentLoadListPrint renders the driver's paper copy.
// GET /admin/fulfillment/load-list/print — opened in a new tab by the tab's
// form submit, which serializes the checked rows as repeated ids params. Fires
// window.print() on load, same as the packing slip and invoice sheets.
func (d *Deps) handleAdminFulfillmentLoadListPrint(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	channel := loadListScope(r)
	selected, explicit := parseLoadListSelection(r.URL.Query())

	var roster []admin.LoadListOrder
	var lines []domain.DeliveryLoadLine
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		roster, lines, txErr = d.loadDeliveryLoadList(ctx, tx, channel, selected, explicit)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	// The printed roster lists only the stops the driver is actually making —
	// unchecked orders are off this run and don't belong on the sheet.
	counted := make([]admin.LoadListOrder, 0, len(roster))
	for _, o := range roster {
		if o.Selected {
			counted = append(counted, o)
		}
	}

	admin.LoadListPrint(admin.LoadListPrintProps{ //nolint:errcheck
		Lines:      lines,
		Orders:     counted,
		ChannelLbl: loadListChannelLabel(channel),
		Combined:   channel == nil,
		Printed:    time.Now(),
		MerchantTZ: d.MerchantTZ,
	}).Render(ctx, w)
}
