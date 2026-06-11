package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
	"github.com/dukerupert/hiri/internal/ui/admin/charts"
)

func (d *Deps) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var props admin.DashboardProps
	props.MerchantTZ = d.MerchantTZ

	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error

		// "Today" is the merchant's local calendar day, not UTC — otherwise late-day
		// orders look like "yesterday" once UTC rolls over.
		now := time.Now().In(d.MerchantTZ)
		todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, d.MerchantTZ)

		// Today's order count. ExcludeUnconfirmed drops PI-created-but-not-paid
		// orders (status=pending AND payment_status=awaiting) so dashboard
		// metrics reflect actual sales, not in-flight intents.
		props.TodayOrderCount, txErr = d.OrderService.CountOrders(ctx, tx, store.OrderFilter{
			PlacedFrom:         &todayStart,
			ExcludeUnconfirmed: true,
		})
		if txErr != nil {
			return txErr
		}

		// Today's revenue
		props.TodayRevenue, txErr = d.OrderService.SumOrderRevenue(ctx, tx, store.OrderFilter{
			PlacedFrom:         &todayStart,
			ExcludeUnconfirmed: true,
		})
		if txErr != nil {
			return txErr
		}

		// Order pipeline, split by sales channel. Each channel's Pack
		// (unfulfilled) and Ship (fulfilled) work is merged into one queue —
		// the dashboard now organizes by retail vs wholesale, not by stage.
		// The two channels stay separate so neither's work hides in the other's.
		props.RetailCount, props.RetailRows, txErr = d.buildChannelPipeline(ctx, tx, domain.OrderChannelRetail)
		if txErr != nil {
			return txErr
		}
		props.WholesaleCount, props.WholesaleRows, txErr = d.buildChannelPipeline(ctx, tx, domain.OrderChannelWholesale)
		if txErr != nil {
			return txErr
		}

		// Orders on hold (explicit manual-review state)
		onHoldStatus := domain.OrderStatusOnHold
		props.OnHold, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			Status: &onHoldStatus,
			Limit:  10,
		})
		if txErr != nil {
			return txErr
		}
		props.OnHoldCount = len(props.OnHold)

		// Failed payments — scan recent orders, cheap at 50-row cap
		recent, txErr := d.OrderService.ListOrders(ctx, tx, store.OrderFilter{Limit: 50})
		if txErr != nil {
			return txErr
		}
		for _, o := range recent {
			if o.PaymentStatus == domain.PaymentStatusFailed &&
				o.Status != domain.OrderStatusCancelled &&
				o.Status != domain.OrderStatusRefunded {
				props.FailedPayments = append(props.FailedPayments, o)
			}
		}
		props.FailedPaymentCount = len(props.FailedPayments)

		// Active subscriptions
		props.ActiveSubCount, txErr = d.SubscriptionService.CountSubscriptionsByStatus(ctx, tx, domain.SubscriptionStatusActive)
		if txErr != nil {
			return txErr
		}

		// Past-due subscriptions — unacknowledged only, so a sub a staffer has
		// already triaged drops off the Urgent band until its next failed charge.
		props.PastDueSubCount, txErr = d.SubscriptionService.CountUnacknowledgedPastDue(ctx, tx)
		if txErr != nil {
			return txErr
		}
		props.PastDueSubs, txErr = d.buildPastDueRows(ctx, tx)
		if txErr != nil {
			return txErr
		}

		// Pending wholesale applications
		wholesaleStatus := domain.WholesaleStatusPending
		wholesaleType := domain.AccountTypeWholesale
		props.PendingWholesale, txErr = d.CustomerService.CountCustomers(ctx, tx, store.CustomerFilter{
			AccountType:     &wholesaleType,
			WholesaleStatus: &wholesaleStatus,
		})
		if txErr != nil {
			return txErr
		}

		// Revenue trend — period from ?days= (7/30/90), defaults to 30
		props.Revenue, txErr = d.buildRevenueProps(ctx, tx, revenueDays(r), todayStart)
		if txErr != nil {
			return txErr
		}

		// Active-subscriptions trend — same 7/30/90 window as revenue, plus a
		// net-change / total mode from ?mode=
		props.Subscriptions, txErr = d.buildSubscriptionsProps(ctx, tx, revenueDays(r), subscriptionsMode(r), todayStart)
		if txErr != nil {
			return txErr
		}

		// Top products — last 30 days
		topStart := todayStart.AddDate(0, 0, -29)
		topEnd := todayStart.AddDate(0, 0, 1)
		props.TopBy = topSellersSort(r)
		topRows, txErr := d.OrderService.TopProducts(ctx, tx, topStart, topEnd, store.TopProductsSort(props.TopBy), 5)
		if txErr != nil {
			return txErr
		}
		props.TopProducts = buildTopProducts(topRows, props.TopBy)

		return nil
	})
	if err != nil {
		Error(w, r, err)
		return
	}

	name, role := staffNameRole(r)
	props.StaffName = name
	props.StaffRole = role

	if IsHTMX(r) {
		admin.DashboardContent(props).Render(ctx, w) //nolint:errcheck
		return
	}
	admin.Dashboard(props).Render(ctx, w) //nolint:errcheck
}

// buildRevenueTrend fills `days` contiguous days starting at trendStart, normalizes
// magnitudes against the max, and populates Sub on every bar so the hover
// tooltip can show the exact value. Returned slice is always exactly `days`
// elements ordered oldest → today.
func buildRevenueTrend(rows []domain.DailyRevenue, trendStart time.Time, tz *time.Location, days int) []charts.ChartPoint {
	byDay := make(map[string]int, len(rows))
	for _, r := range rows {
		byDay[r.Date.Format("2006-01-02")] = r.Cents
	}
	maxCents := 0
	for _, c := range byDay {
		if c > maxCents {
			maxCents = c
		}
	}
	out := make([]charts.ChartPoint, days)
	for i := 0; i < days; i++ {
		day := trendStart.AddDate(0, 0, i).In(tz)
		key := day.Format("2006-01-02")
		cents := byDay[key]
		mag := 0.0
		if maxCents > 0 {
			mag = float64(cents) / float64(maxCents)
		}
		isToday := i == days-1
		label := day.Format("Jan 2")
		if isToday {
			label = "Today"
		}
		out[i] = charts.ChartPoint{
			Label:     label,
			Sub:       compactDollars(cents),
			Magnitude: mag,
			Highlight: isToday,
		}
	}
	return out
}

// revenueDays normalizes the ?days= query param to one of 7/30/90, defaulting
// to 30. Lives here so the dashboard and the fragment endpoint agree on
// validation.
func revenueDays(r *http.Request) int {
	switch r.URL.Query().Get("days") {
	case "7":
		return 7
	case "90":
		return 90
	default:
		return 30
	}
}

// subscriptionsMode reads the subscriptions chart mode from ?mode=. It defaults
// to "delta" (per-day net change) since day-to-day movement is usually the
// signal; "total" shows the running active base as a level.
func subscriptionsMode(r *http.Request) string {
	if r.URL.Query().Get("mode") == "total" {
		return "total"
	}
	return "delta"
}

// buildRevenueProps fetches the daily trend plus the prior-window total so the
// card can render the period-over-period delta. The prior window is the same
// length as the current one, ending the day before trendStart.
func (d *Deps) buildRevenueProps(ctx context.Context, tx pgx.Tx, days int, todayStart time.Time) (admin.RevenueProps, error) {
	trendStart := todayStart.AddDate(0, 0, -(days - 1))
	trendEnd := todayStart.AddDate(0, 0, 1) // exclusive upper bound
	daily, err := d.OrderService.RevenueByDay(ctx, tx, trendStart, trendEnd, d.MerchantTZ)
	if err != nil {
		return admin.RevenueProps{}, err
	}

	currentTotal := 0
	for _, r := range daily {
		currentTotal += r.Cents
	}

	priorStart := trendStart.AddDate(0, 0, -days)
	priorEnd := trendStart // exclusive — equal to trendStart
	priorTotal, err := d.OrderService.SumOrderRevenue(ctx, tx, store.OrderFilter{
		PlacedFrom:               &priorStart,
		PlacedTo:                 &priorEnd,
		ExcludeUnconfirmed:       true,
		ExcludeCancelledRefunded: true,
	})
	if err != nil {
		return admin.RevenueProps{}, err
	}

	// Subscription revenue subset of the current window — drives the
	// "X% subscription · Y% one-time" mix line on the card. Filter set
	// mirrors RevenueByDay so the percentage adds up against the chart total.
	onlySub := true
	subRevenue, err := d.OrderService.SumOrderRevenue(ctx, tx, store.OrderFilter{
		PlacedFrom:               &trendStart,
		PlacedTo:                 &trendEnd,
		ExcludeUnconfirmed:       true,
		ExcludeCancelledRefunded: true,
		OnlySubscription:         &onlySub,
	})
	if err != nil {
		return admin.RevenueProps{}, err
	}

	return admin.RevenueProps{
		Days:                days,
		Trend:               buildRevenueTrend(daily, trendStart, d.MerchantTZ, days),
		CurrentTotal:        currentTotal,
		PriorTotal:          priorTotal,
		SubscriptionRevenue: subRevenue,
	}, nil
}

// handleAdminRevenue renders just the revenue card for an htmx swap when the
// user toggles the 7/30/90-day period selector.
func (d *Deps) handleAdminRevenue(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days := revenueDays(r)

	now := time.Now().In(d.MerchantTZ)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, d.MerchantTZ)

	var rp admin.RevenueProps
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		rp, txErr = d.buildRevenueProps(ctx, tx, days, todayStart)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	admin.RevenueCard(rp).Render(ctx, w) //nolint:errcheck
}

// buildSubscriptionsProps assembles the active-subscriptions trend card: the
// live base at the window start, the per-day net deltas across the window, and
// the resulting daily levels. PriorActive is the window-start base; the card
// renders growth against it. CurrentActive is the final (today's) level.
func (d *Deps) buildSubscriptionsProps(ctx context.Context, tx pgx.Tx, days int, mode string, todayStart time.Time) (admin.SubscriptionsProps, error) {
	trendStart := todayStart.AddDate(0, 0, -(days - 1))
	trendEnd := todayStart.AddDate(0, 0, 1) // exclusive upper bound

	baseline, err := d.SubscriptionService.ActiveSubscriptionsAsOf(ctx, tx, trendStart)
	if err != nil {
		return admin.SubscriptionsProps{}, err
	}
	deltas, err := d.SubscriptionService.ActiveSubscriptionDeltasByDay(ctx, tx, trendStart, trendEnd, d.MerchantTZ)
	if err != nil {
		return admin.SubscriptionsProps{}, err
	}

	// buildSubscriptionTrend gives us the running levels and today's count; in
	// delta mode we keep the count for the headline but swap the chart series for
	// the per-day net change.
	trend, currentActive := buildSubscriptionTrend(deltas, baseline, trendStart, d.MerchantTZ, days)
	if mode == "delta" {
		trend = buildSubscriptionDeltaTrend(deltas, trendStart, d.MerchantTZ, days)
	}

	return admin.SubscriptionsProps{
		Days:          days,
		Mode:          mode,
		Trend:         trend,
		CurrentActive: currentActive,
		PriorActive:   baseline,
	}, nil
}

// buildSubscriptionTrend reconstructs the active subscription base for each of
// `days` contiguous days ending today. Unlike revenue's zero-fill, a day with
// no delta holds the prior level (carry-forward), because this is a running
// count, not a per-day total: it's seeded with baseline (the base at
// trendStart) and walks forward applying each day's net change. Bars normalize
// against the peak level. Returns the points plus the final (today's) count.
func buildSubscriptionTrend(deltas []domain.SubscriptionDelta, baseline int, trendStart time.Time, tz *time.Location, days int) ([]charts.ChartPoint, int) {
	byDay := make(map[string]int, len(deltas))
	for _, dlt := range deltas {
		byDay[dlt.Date.Format("2006-01-02")] = dlt.Net
	}

	levels := make([]int, days)
	running := baseline
	for i := 0; i < days; i++ {
		day := trendStart.AddDate(0, 0, i).In(tz)
		running += byDay[day.Format("2006-01-02")]
		levels[i] = running
	}

	maxLevel := 0
	for _, v := range levels {
		if v > maxLevel {
			maxLevel = v
		}
	}

	out := make([]charts.ChartPoint, days)
	for i := 0; i < days; i++ {
		mag := 0.0
		if maxLevel > 0 {
			mag = float64(levels[i]) / float64(maxLevel)
		}
		day := trendStart.AddDate(0, 0, i).In(tz)
		label := day.Format("Jan 2")
		if i == days-1 {
			label = "Today"
		}
		out[i] = charts.ChartPoint{
			Label:     label,
			Sub:       fmt.Sprintf("%d", levels[i]),
			Magnitude: mag,
			Highlight: i == days-1,
		}
	}
	if days == 0 {
		return out, baseline
	}
	return out, levels[days-1]
}

// buildSubscriptionDeltaTrend renders the per-day net change in the active base
// over `days` ending today: positive on net sign-ups, negative on net churn. A
// day with no movement is zero. Magnitudes are normalized symmetrically against
// the largest absolute swing in the window, so the zero line sits mid-chart and
// gains/losses read at a glance; Sub carries the signed count for the tooltip.
func buildSubscriptionDeltaTrend(deltas []domain.SubscriptionDelta, trendStart time.Time, tz *time.Location, days int) []charts.ChartPoint {
	byDay := make(map[string]int, len(deltas))
	for _, dlt := range deltas {
		byDay[dlt.Date.Format("2006-01-02")] = dlt.Net
	}

	nets := make([]int, days)
	maxAbs := 0
	for i := 0; i < days; i++ {
		day := trendStart.AddDate(0, 0, i).In(tz)
		n := byDay[day.Format("2006-01-02")]
		nets[i] = n
		a := n
		if a < 0 {
			a = -a
		}
		if a > maxAbs {
			maxAbs = a
		}
	}

	out := make([]charts.ChartPoint, days)
	for i := 0; i < days; i++ {
		mag := 0.0
		if maxAbs > 0 {
			mag = float64(nets[i]) / float64(maxAbs)
		}
		day := trendStart.AddDate(0, 0, i).In(tz)
		label := day.Format("Jan 2")
		if i == days-1 {
			label = "Today"
		}
		sub := "0"
		if nets[i] != 0 {
			sub = fmt.Sprintf("%+d", nets[i])
		}
		out[i] = charts.ChartPoint{
			Label:     label,
			Sub:       sub,
			Magnitude: mag,
			Highlight: i == days-1,
		}
	}
	return out
}

// handleAdminSubscriptionsTrend renders just the subscriptions card for an htmx
// swap when the user toggles the 7/30/90-day period selector.
func (d *Deps) handleAdminSubscriptionsTrend(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	days := revenueDays(r)
	mode := subscriptionsMode(r)

	now := time.Now().In(d.MerchantTZ)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, d.MerchantTZ)

	var sp admin.SubscriptionsProps
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		sp, txErr = d.buildSubscriptionsProps(ctx, tx, days, mode, todayStart)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	admin.SubscriptionsCard(sp).Render(ctx, w) //nolint:errcheck
}

// buildTopProducts converts ProductSales rows to chart data. Bars are
// normalized against the leading row's value for the active metric. The
// secondary label always shows revenue so both metrics keep dollar context.
func buildTopProducts(rows []domain.ProductSales, by string) []charts.LabeledValue {
	if len(rows) == 0 {
		return nil
	}
	pickValue := func(r domain.ProductSales) (float64, string) {
		if by == "weight" {
			return float64(r.WeightGrams), formatPounds(r.WeightGrams)
		}
		return float64(r.Units), fmt.Sprintf("%d units", r.Units)
	}
	headValue, _ := pickValue(rows[0]) // rows already sorted desc by chosen metric
	out := make([]charts.LabeledValue, len(rows))
	for i, r := range rows {
		v, label := pickValue(r)
		mag := 0.0
		if headValue > 0 {
			mag = v / headValue
		}
		out[i] = charts.LabeledValue{
			Label:     r.Title,
			Value:     label,
			Sub:       compactDollars(r.Revenue),
			Magnitude: mag,
		}
	}
	return out
}

// topSellersSort normalizes the ?by= query param to one of "units" / "weight",
// defaulting to units. Lives here so both the dashboard and fragment endpoint
// agree on validation.
func topSellersSort(r *http.Request) string {
	switch r.URL.Query().Get("by") {
	case "weight":
		return "weight"
	default:
		return "units"
	}
}

// formatPounds renders a gram total as "X.X lb" (or "X lb" when whole). Coffee
// volumes range from a half-pound bag to multi-pound wholesale orders, so one
// decimal of precision keeps the chart readable across that whole range.
func formatPounds(grams int) string {
	const gramsPerPound = 453.59237
	lbs := float64(grams) / gramsPerPound
	if lbs >= 100 {
		return fmt.Sprintf("%.0f lb", lbs)
	}
	return fmt.Sprintf("%.1f lb", lbs)
}

// handleAdminTopSellers renders just the top-sellers card for an htmx swap.
// The full dashboard owns the same data + range; this endpoint only re-runs
// the single product-sales query when the user toggles units ↔ pounds.
func (d *Deps) handleAdminTopSellers(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	by := topSellersSort(r)

	now := time.Now().In(d.MerchantTZ)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, d.MerchantTZ)
	from := todayStart.AddDate(0, 0, -29)
	to := todayStart.AddDate(0, 0, 1)

	var rows []domain.ProductSales
	err := store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		rows, txErr = d.OrderService.TopProducts(ctx, tx, from, to, store.TopProductsSort(by), 5)
		return txErr
	})
	if err != nil {
		Error(w, r, err)
		return
	}
	admin.TopSellersCard(buildTopProducts(rows, by), by).Render(ctx, w) //nolint:errcheck
}

// pipelineDisplayLimit caps how many rows each channel queue shows on the
// dashboard. The header's "View all N" link carries staff to the full
// fulfillment queue when there's more than this.
const pipelineDisplayLimit = 8

// buildChannelPipeline assembles one channel's dashboard order queue: the
// accurate NeedsAction total (matching that channel's fulfillment tab) plus up
// to pipelineDisplayLimit enriched rows. Pack (unfulfilled, paid) and Ship
// (fulfilled) stages are merged into a single list — each row carries its
// Stage — and sorted oldest-first so the most-overdue work surfaces at the top.
func (d *Deps) buildChannelPipeline(ctx context.Context, tx pgx.Tx, channel domain.OrderChannel) (int, []admin.PipelineRow, error) {
	views, err := d.OrderService.CountFulfillmentViews(ctx, tx, &channel)
	if err != nil {
		return 0, nil, err
	}

	// Pack stage: unfulfilled orders with payment captured.
	unfulfilled := domain.FulfillmentStatusUnfulfilled
	packOrders, err := d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
		Channel:           &channel,
		FulfillmentStatus: &unfulfilled,
		Limit:             pipelineDisplayLimit,
	})
	if err != nil {
		return 0, nil, err
	}

	// Ship stage: fulfilled orders awaiting a label or dispatch.
	fulfilled := domain.FulfillmentStatusFulfilled
	shipOrders, err := d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
		Channel:           &channel,
		FulfillmentStatus: &fulfilled,
		Limit:             pipelineDisplayLimit,
	})
	if err != nil {
		return 0, nil, err
	}

	type staged struct {
		order domain.Order
		stage string
	}
	var combined []staged
	for _, o := range packOrders {
		if o.PaymentStatus == domain.PaymentStatusCaptured &&
			o.Status != domain.OrderStatusCancelled &&
			o.Status != domain.OrderStatusRefunded {
			combined = append(combined, staged{o, "Pack"})
		}
	}
	for _, o := range shipOrders {
		if o.Status != domain.OrderStatusCancelled && o.Status != domain.OrderStatusRefunded {
			combined = append(combined, staged{o, "Ship"})
		}
	}
	// Oldest first — the most-overdue order rises to the top of the queue.
	sort.Slice(combined, func(i, j int) bool {
		return combined[i].order.PlacedAt.Before(combined[j].order.PlacedAt)
	})
	if len(combined) > pipelineDisplayLimit {
		combined = combined[:pipelineDisplayLimit]
	}

	orders := make([]domain.Order, len(combined))
	for i, s := range combined {
		orders[i] = s.order
	}
	rows, err := d.buildPipelineRows(ctx, tx, orders)
	if err != nil {
		return 0, nil, err
	}
	for i := range rows {
		rows[i].Stage = combined[i].stage
	}
	return views.NeedsAction, rows, nil
}

// buildPipelineRows enriches each order with the summary fields the channel
// queues display: lead item, total quantity, and customer name. Bounded by
// pipelineDisplayLimit, so the per-order lookups need no batching.
func (d *Deps) buildPipelineRows(ctx context.Context, tx pgx.Tx, orders []domain.Order) ([]admin.PipelineRow, error) {
	rows := make([]admin.PipelineRow, len(orders))
	for i, o := range orders {
		row := admin.PipelineRow{Order: o, CustomerName: "Guest"}

		if o.CustomerID != nil {
			c, err := d.CustomerService.GetCustomer(ctx, tx, *o.CustomerID)
			if err != nil && !errors.Is(err, app.ErrCustomerNotFound) {
				return nil, err
			}
			if c != nil {
				row.CustomerName = customerDisplayName(c)
			}
		}

		items, err := d.OrderService.ListLineItems(ctx, tx, o.ID)
		if err != nil {
			return nil, err
		}
		for _, li := range items {
			row.Quantity += li.Quantity
		}
		if len(items) > 0 {
			title, err := d.lookupProductTitle(ctx, tx, items[0].VariantID)
			if err != nil {
				return nil, err
			}
			row.ItemTitle = title
			if len(items) > 1 {
				row.ExtraItems = len(items) - 1
			}
		}

		rows[i] = row
	}
	return rows, nil
}

// buildPastDueRows lists the unacknowledged past-due subscriptions for the
// Urgent band, most-overdue first, and enriches each with the customer name and
// lead product title the row displays. Bounded by pipelineDisplayLimit, so the
// per-row lookups need no batching — same shape as buildPipelineRows.
func (d *Deps) buildPastDueRows(ctx context.Context, tx pgx.Tx) ([]admin.PastDueRow, error) {
	pastDue := domain.SubscriptionStatusPastDue
	subs, err := d.SubscriptionService.ListSubscriptions(ctx, tx, store.SubscriptionFilter{
		Status:                     &pastDue,
		ExcludeDunningAcknowledged: true,
		Sort:                       store.SubscriptionSortNextOrderAsc,
		Limit:                      pipelineDisplayLimit,
	})
	if err != nil {
		return nil, err
	}

	rows := make([]admin.PastDueRow, len(subs))
	for i, sub := range subs {
		row := admin.PastDueRow{Subscription: sub, Quantity: sub.Quantity, CustomerName: "Guest"}

		c, err := d.CustomerService.GetCustomer(ctx, tx, sub.CustomerID)
		if err != nil && !errors.Is(err, app.ErrCustomerNotFound) {
			return nil, err
		}
		if c != nil {
			row.CustomerName = customerDisplayName(c)
		}

		title, err := d.lookupProductTitle(ctx, tx, sub.VariantID)
		if err != nil {
			return nil, err
		}
		row.ProductTitle = title

		rows[i] = row
	}
	return rows, nil
}

// lookupProductTitle resolves a variant ID to its product title. Missing
// variant or product (e.g., deleted catalog entry) yields an empty string —
// the template falls back to "—" so the row still renders.
func (d *Deps) lookupProductTitle(ctx context.Context, tx pgx.Tx, variantID uuid.UUID) (string, error) {
	v, err := d.CatalogService.GetVariant(ctx, tx, variantID)
	if err != nil {
		if errors.Is(err, app.ErrVariantNotFound) {
			return "", nil
		}
		return "", err
	}
	p, err := d.CatalogService.GetProduct(ctx, tx, v.ProductID)
	if err != nil {
		if errors.Is(err, app.ErrProductNotFound) {
			return "", nil
		}
		return "", err
	}
	return p.Title, nil
}

// customerDisplayName picks the most useful label for a customer at a glance:
// company name for wholesale buyers, otherwise "First Last", falling back to
// the email local-part when no name is set.
func customerDisplayName(c *domain.Customer) string {
	if c.AccountType == domain.AccountTypeWholesale && c.CompanyName != nil && *c.CompanyName != "" {
		return *c.CompanyName
	}
	full := strings.TrimSpace(c.FirstName + " " + c.LastName)
	if full != "" {
		return full
	}
	if c.Email != "" {
		if at := strings.IndexByte(c.Email, '@'); at > 0 {
			return c.Email[:at]
		}
		return c.Email
	}
	return "Guest"
}

// compactDollars renders cents as "$1.2k" / "$240" — kept short for chart labels.
func compactDollars(cents int) string {
	dollars := cents / 100
	switch {
	case dollars >= 1000:
		return fmt.Sprintf("$%.1fk", float64(dollars)/1000)
	default:
		return fmt.Sprintf("$%d", dollars)
	}
}
