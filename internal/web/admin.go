package web

import (
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/ui/admin"
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
