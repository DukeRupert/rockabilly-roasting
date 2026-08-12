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

// loadListChannel resolves the ?channel= param used by the totals fragment and
// the print sheet. Anything other than "wholesale" is retail — the storefront
// queue is the common case and an unrecognized value should land somewhere
// sane rather than 400.
func loadListChannel(r *http.Request) domain.OrderChannel {
	if r.URL.Query().Get("channel") == string(domain.OrderChannelWholesale) {
		return domain.OrderChannelWholesale
	}
	return domain.OrderChannelRetail
}

// loadListChannelLabel is the human name shown on the printed masthead.
func loadListChannelLabel(c domain.OrderChannel) string {
	if c == domain.OrderChannelWholesale {
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
	channel domain.OrderChannel,
	selected []uuid.UUID,
	explicit bool,
) ([]admin.LoadListOrder, []domain.DeliveryLoadLine, error) {
	localDelivery := domain.ShippingMethodLocalDelivery

	rosterFilter := store.OrderFilter{
		Channel:                  &channel,
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
	if channel == domain.OrderChannelWholesale {
		customerIDs := make([]uuid.UUID, 0, len(roster))
		seen := make(map[uuid.UUID]bool, len(roster))
		for i := range roster {
			if cid := roster[i].Order.CustomerID; cid != nil && !seen[*cid] {
				seen[*cid] = true
				customerIDs = append(customerIDs, *cid)
			}
		}
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

// renderFulfillmentLoadList renders the "Load list" tab of a fulfillment
// queue — per-product pound totals across the delivery orders waiting to go
// out, over a roster staff can adjust for outliers.
func (d *Deps) renderFulfillmentLoadList(
	w http.ResponseWriter,
	r *http.Request,
	channel domain.OrderChannel,
	basePath, title string,
) {
	ctx := r.Context()
	selected, explicit := parseLoadListSelection(r.URL.Query())

	var roster []admin.LoadListOrder
	var lines []domain.DeliveryLoadLine
	var counts store.FulfillmentViewCounts

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		counts, txErr = d.OrderService.CountFulfillmentViews(ctx, tx, &channel)
		if txErr != nil {
			return txErr
		}
		roster, lines, txErr = d.loadDeliveryLoadList(ctx, tx, channel, selected, explicit)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props := admin.FulfillmentListProps{
		BasePath: basePath,
		Title:    title,
		View:     "load_list",
		Counts: admin.FulfillmentViewCounts{
			NeedsAction: counts.NeedsAction,
			ReadyToShip: counts.ReadyToShip,
			Shipped:     counts.Shipped,
			Delivered:   counts.Delivered,
			All:         counts.All,
			LoadList:    counts.LoadList,
		},
		MerchantTZ: d.MerchantTZ,
		Now:        time.Now(),
		StaffName:  name,
		StaffRole:  role,
		LoadList: &admin.LoadListProps{
			BasePath:   basePath,
			Channel:    string(channel),
			Lines:      lines,
			Orders:     roster,
			MerchantTZ: d.MerchantTZ,
			Now:        time.Now(),
		},
	}

	if IsHTMX(r) {
		admin.FulfillmentListContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.FulfillmentList(props).Render(ctx, w) //nolint:errcheck
}

// handleAdminFulfillmentLoadListTotals re-renders just the totals panel after
// staff check or uncheck an order. GET /admin/fulfillment/load-list/totals.
// Keeping the arithmetic server-side means the browser never computes a number
// a driver is going to trust.
func (d *Deps) handleAdminFulfillmentLoadListTotals(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	channel := loadListChannel(r)
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
		Channel:    string(channel),
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
	channel := loadListChannel(r)
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
		Printed:    time.Now(),
		MerchantTZ: d.MerchantTZ,
	}).Render(ctx, w)
}
