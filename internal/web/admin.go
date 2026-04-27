package web

import (
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

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

		// Today's order count
		props.TodayOrderCount, txErr = d.OrderService.CountOrders(ctx, tx, store.OrderFilter{
			PlacedFrom: &todayStart,
		})
		if txErr != nil {
			return txErr
		}

		// Today's revenue
		props.TodayRevenue, txErr = d.OrderService.SumOrderRevenue(ctx, tx, store.OrderFilter{
			PlacedFrom: &todayStart,
		})
		if txErr != nil {
			return txErr
		}

		// Orders needing fulfillment (paid but unfulfilled)
		toFulfillStatus := domain.FulfillmentStatusUnfulfilled
		props.ToFulfill, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			FulfillmentStatus: &toFulfillStatus,
			Limit:             10,
		})
		if txErr != nil {
			return txErr
		}
		// Filter to only paid orders (payment captured)
		var paidUnfulfilled []domain.Order
		for _, o := range props.ToFulfill {
			if o.PaymentStatus == domain.PaymentStatusCaptured &&
				o.Status != domain.OrderStatusCancelled &&
				o.Status != domain.OrderStatusRefunded {
				paidUnfulfilled = append(paidUnfulfilled, o)
			}
		}
		props.ToFulfill = paidUnfulfilled
		props.ToFulfillCount = len(paidUnfulfilled)

		// Orders ready to ship (fulfilled but not yet shipped)
		toShipStatus := domain.FulfillmentStatusFulfilled
		props.ToShip, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			FulfillmentStatus: &toShipStatus,
			Limit:             10,
		})
		if txErr != nil {
			return txErr
		}
		// Filter out cancelled/refunded
		var readyToShip []domain.Order
		for _, o := range props.ToShip {
			if o.Status != domain.OrderStatusCancelled && o.Status != domain.OrderStatusRefunded {
				readyToShip = append(readyToShip, o)
			}
		}
		props.ToShip = readyToShip
		props.ToShipCount = len(readyToShip)

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

		// Past-due subscriptions
		props.PastDueSubCount, txErr = d.SubscriptionService.CountSubscriptionsByStatus(ctx, tx, domain.SubscriptionStatusPastDue)
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

		// Recent orders (last 10)
		props.RecentOrders, txErr = d.OrderService.ListOrders(ctx, tx, store.OrderFilter{
			Limit: 10,
		})
		if txErr != nil {
			return txErr
		}

		// Revenue trend — last 14 days, oldest → today
		trendStart := todayStart.AddDate(0, 0, -13)
		trendEnd := todayStart.AddDate(0, 0, 1) // exclusive upper bound
		dailyRows, txErr := d.OrderService.RevenueByDay(ctx, tx, trendStart, trendEnd, d.MerchantTZ)
		if txErr != nil {
			return txErr
		}
		props.RevenueTrend = buildRevenueTrend(dailyRows, trendStart, d.MerchantTZ)

		// Top products — last 30 days
		topStart := todayStart.AddDate(0, 0, -29)
		topEnd := trendEnd
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

// buildRevenueTrend fills 14 contiguous days starting at trendStart, normalizes
// magnitudes against the max, and shows a value label on today's bar only.
// Returned slice is always exactly 14 elements ordered oldest → today.
func buildRevenueTrend(rows []domain.DailyRevenue, trendStart time.Time, tz *time.Location) []charts.ChartPoint {
	const days = 14
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
		sub := ""
		if isToday && cents > 0 {
			sub = compactDollars(cents)
		}
		out[i] = charts.ChartPoint{
			Label:     label,
			Sub:       sub,
			Magnitude: mag,
			Highlight: isToday,
		}
	}
	return out
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
