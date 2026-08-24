package app_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// newLabelFulfillmentService wires the stores PrepareLabelRequest reads from.
// No label provider — preparing the request never calls the carrier.
func newLabelFulfillmentService() *app.FulfillmentService {
	return app.NewFulfillmentService(
		store.NewFulfillmentStore(),
		store.NewShippingStore(),
		store.NewOrderStore(nil),
		store.NewBoxPresetStore(),
		store.NewCustomerStore(),
		store.NewCatalogStore(),
		nil, // labelProvider
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	)
}

// setAddressLine2 sets (or clears, with nil) the secondary address line. There
// is no fixture option for it and this is the column under test.
func setAddressLine2(t *testing.T, tx pgx.Tx, addressID uuid.UUID, line2 *string) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE addresses SET line2 = $2 WHERE id = $1`, addressID, line2)
	require.NoError(t, err)
}

// setOriginStreet2 sets the roastery's own secondary line on the singleton
// shipping config row.
func setOriginStreet2(t *testing.T, tx pgx.Tx, street2 string) {
	t.Helper()
	_, err := tx.Exec(context.Background(),
		`UPDATE shipping_config SET origin_street1 = '101 W Kennewick Ave', origin_street2 = $1,
		     origin_city = 'Kennewick', origin_state = 'WA', origin_zip = '99336'`, street2)
	require.NoError(t, err)
}

// shippableOrder builds the minimum an order needs to be labelled: a box
// preset that fits, one physical line item with a known weight, and a
// shipping address. Returns the order id and the address id.
func shippableOrder(t *testing.T, tx pgx.Tx) (orderID, addressID uuid.UUID) {
	t.Helper()
	ctx := context.Background()

	_, err := tx.Exec(ctx,
		`INSERT INTO box_presets (id, name, length_in, width_in, height_in, max_weight_oz)
		 VALUES ($1, 'Small', 10, 8, 4, 160)`, uuid.New())
	require.NoError(t, err)

	cust := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, cust.ID)
	grams := 454
	_, variantID := oneLbVariant(t, tx, "Rebel Blend", &grams)
	order := testutil.CreateOrder(t, tx, cust.ID, addr.ID, addr.ID)
	addLineItem(t, tx, order.ID, variantID, 1)

	return order.ID, addr.ID
}

// TestPrepareLabelRequest_Line2 pins the secondary address line onto the
// outgoing label request. Apartment numbers were silently dropped here for the
// life of the product, so anything that needed a unit number to be deliverable
// came back returned-to-sender.
func TestPrepareLabelRequest_Line2(t *testing.T) {
	ctx := context.Background()
	svc := newLabelFulfillmentService()

	apt := "Apt 10"
	blank := "   "

	tests := []struct {
		name     string
		line2    *string
		origin2  string
		wantTo   string
		wantFrom string
	}{
		{name: "apartment number reaches the label", line2: &apt, origin2: "Suite 200", wantTo: "Apt 10", wantFrom: "Suite 200"},
		{name: "null line2 stays empty", line2: nil, origin2: "", wantTo: "", wantFrom: ""},
		{name: "whitespace-only is treated as empty", line2: &blank, origin2: "  ", wantTo: "", wantFrom: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			orderID, addressID := shippableOrder(t, tx)
			setAddressLine2(t, tx, addressID, tc.line2)
			setOriginStreet2(t, tx, tc.origin2)

			req, err := svc.PrepareLabelRequest(ctx, tx, orderID, "usps_ground_advantage")
			require.NoError(t, err)

			assert.Equal(t, tc.wantTo, req.ToStreet2)
			assert.Equal(t, tc.wantFrom, req.FromStreet2)
			// The primary line is untouched either way.
			assert.NotEmpty(t, req.ToStreet1)
			assert.Equal(t, "101 W Kennewick Ave", req.FromStreet1)
		})
	}
}
