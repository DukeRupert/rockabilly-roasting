package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/platform/shipping"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newTrackingFulfillmentService builds a FulfillmentService with only the
// dependencies ApplyTrackingStatus uses (shipments store + audit). The rest are
// nil — exercised paths never touch them.
func newTrackingFulfillmentService() *app.FulfillmentService {
	return app.NewFulfillmentService(
		nil, // fulfillment
		store.NewShippingStore(),
		nil, // orders
		nil, // boxPresets
		nil, // customers
		nil, // catalog
		nil, // labelProvider
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// createTrackedShipment inserts a shipment in label_created state with the given
// tracking number and returns it.
func createTrackedShipment(t *testing.T, tx pgx.Tx, trackingNumber string) *domain.Shipment {
	t.Helper()
	customer := testutil.CreateCustomer(t, tx)
	shipAddr := testutil.CreateAddress(t, tx, customer.ID)
	billAddr := testutil.CreateAddress(t, tx, customer.ID)
	order := testutil.CreateOrder(t, tx, customer.ID, shipAddr.ID, billAddr.ID)
	staffID := testutil.CreateStaff(t, tx)

	shipment, err := store.NewShippingStore().CreateShipment(context.Background(), tx, store.CreateShipmentParams{
		OrderID:        order.ID,
		Status:         domain.ShipmentStatusLabelCreated,
		Provider:       "shippo",
		TrackingNumber: trackingNumber,
		CarrierName:    "USPS",
		ServiceName:    "Ground Advantage",
		LabelCostCents: 758,
		LabelCurrency:  "USD",
		WeightOz:       12.5,
		CreatedBy:      staffID,
	})
	require.NoError(t, err)
	return shipment
}

func TestFulfillmentService_ApplyTrackingStatus(t *testing.T) {
	ctx := context.Background()
	svc := newTrackingFulfillmentService()

	t.Run("TRANSIT advances label_created to in_transit", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		sh := createTrackedShipment(t, tx, "TRACK-TRANSIT")

		updated, err := svc.ApplyTrackingStatus(ctx, tx, sh.TrackingNumber, shipping.ShippoTrackTransit)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, domain.ShipmentStatusInTransit, updated.Status)
	})

	t.Run("DELIVERED sets status and delivered_at", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		sh := createTrackedShipment(t, tx, "TRACK-DELIVERED")

		updated, err := svc.ApplyTrackingStatus(ctx, tx, sh.TrackingNumber, shipping.ShippoTrackDelivered)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, domain.ShipmentStatusDelivered, updated.Status)
		require.NotNil(t, updated.DeliveredAt, "delivered_at should be set")
	})

	t.Run("RETURNED maps to exception", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		sh := createTrackedShipment(t, tx, "TRACK-RETURNED")

		updated, err := svc.ApplyTrackingStatus(ctx, tx, sh.TrackingNumber, shipping.ShippoTrackReturned)
		require.NoError(t, err)
		require.NotNil(t, updated)
		assert.Equal(t, domain.ShipmentStatusException, updated.Status)
	})

	t.Run("stale TRANSIT after DELIVERED is ignored (forward-only)", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		sh := createTrackedShipment(t, tx, "TRACK-STALE")

		_, err := svc.ApplyTrackingStatus(ctx, tx, sh.TrackingNumber, shipping.ShippoTrackDelivered)
		require.NoError(t, err)

		// A late in-transit event must not un-deliver the shipment.
		updated, err := svc.ApplyTrackingStatus(ctx, tx, sh.TrackingNumber, shipping.ShippoTrackTransit)
		require.NoError(t, err)
		assert.Nil(t, updated, "no transition should be applied")

		reloaded, err := store.NewShippingStore().GetShipmentByIDAsStaff(ctx, tx, sh.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.ShipmentStatusDelivered, reloaded.Status)
	})

	t.Run("unactionable token (PRE_TRANSIT) is a no-op", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		sh := createTrackedShipment(t, tx, "TRACK-PRE")

		updated, err := svc.ApplyTrackingStatus(ctx, tx, sh.TrackingNumber, shipping.ShippoTrackPreTransit)
		require.NoError(t, err)
		assert.Nil(t, updated)
	})

	t.Run("unknown tracking number returns ErrShipmentNotFound", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		_, err := svc.ApplyTrackingStatus(ctx, tx, "DOES-NOT-EXIST-"+uuid.NewString(), shipping.ShippoTrackDelivered)
		assert.True(t, errors.Is(err, app.ErrShipmentNotFound))
	})
}
