package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

// canConvertToLocal decides whether the order page offers the way back off the
// carrier channel. It has to agree with app.ConvertShippedOrderToLocal, because
// a button that submits into a refusal is worse than no button — and until this
// pair existed, the mail-out conversion was a one-way door that could only be
// undone with a hand-written UPDATE against production.

func convertibleOrder(method *domain.ShippingMethod) *domain.Order {
	return &domain.Order{
		Status:            domain.OrderStatusConfirmed,
		FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
		ShippingMethod:    method,
	}
}

func methodPtr(m domain.ShippingMethod) *domain.ShippingMethod { return &m }

func TestCanConvertToLocal_ShippedOrderInTheShop(t *testing.T) {
	order := convertibleOrder(methodPtr(domain.ShippingMethodShipped))
	assert.True(t, canConvertToLocal(order, nil))

	// Fulfilled but not yet gone is still in the shop.
	order.FulfillmentStatus = domain.FulfillmentStatusFulfilled
	assert.True(t, canConvertToLocal(order, nil))
}

// A nil method is what imported and pre-local-channel orders carry, and those
// are the ones most likely to be filed on the wrong channel.
func TestCanConvertToLocal_NilMethodCountsAsShipped(t *testing.T) {
	assert.True(t, canConvertToLocal(convertibleOrder(nil), nil))
}

// The local orders get the swap + mail-out controls instead; offering both sets
// at once would put four buttons on one card.
func TestCanConvertToLocal_LocalOrdersAreNotOffered(t *testing.T) {
	assert.False(t, canConvertToLocal(convertibleOrder(methodPtr(domain.ShippingMethodLocalDelivery)), nil))
	assert.False(t, canConvertToLocal(convertibleOrder(methodPtr(domain.ShippingMethodPickup)), nil))
}

func TestCanConvertToLocal_OrderThatLeftTheShop(t *testing.T) {
	order := convertibleOrder(methodPtr(domain.ShippingMethodShipped))
	for _, st := range []domain.FulfillmentStatus{
		domain.FulfillmentStatusShipped,
		domain.FulfillmentStatusPartiallyShipped,
		domain.FulfillmentStatusDelivered,
		domain.FulfillmentStatusReturned,
	} {
		order.FulfillmentStatus = st
		assert.False(t, canConvertToLocal(order, nil), "status %s", st)
	}
}

func TestCanConvertToLocal_TerminatedOrders(t *testing.T) {
	for _, st := range []domain.OrderStatus{
		domain.OrderStatusCancelled,
		domain.OrderStatusRefunded,
	} {
		order := convertibleOrder(methodPtr(domain.ShippingMethodShipped))
		order.Status = st
		assert.False(t, canConvertToLocal(order, nil), "status %s", st)
	}
}

// Postage that is bought and unrefunded is the one hard blocker: converting
// would put the order on the van while the label stays spendable. BlocksRebuy
// is the shared rule, so the gate tracks it rather than restating it.
func TestCanConvertToLocal_LiveLabelBlocks(t *testing.T) {
	order := convertibleOrder(methodPtr(domain.ShippingMethodShipped))

	live := []domain.Shipment{{RefundStatus: domain.RefundStatusNone}}
	assert.False(t, canConvertToLocal(order, live))

	// A rejected refund means the label was found to be used — still live.
	rejected := []domain.Shipment{{RefundStatus: domain.RefundStatusFailed}}
	assert.False(t, canConvertToLocal(order, rejected))
}

func TestCanConvertToLocal_RefundedLabelUnblocks(t *testing.T) {
	order := convertibleOrder(methodPtr(domain.ShippingMethodShipped))
	for _, rs := range []domain.RefundStatus{
		domain.RefundStatusRequested,
		domain.RefundStatusRefunded,
	} {
		assert.True(t, canConvertToLocal(order, []domain.Shipment{{RefundStatus: rs}}),
			"refund status %s should free the order", rs)
	}
}

// The out-of-zone warning is deliberately a warning and not a lock — staff know
// the route better than the zip table — so it must reach the confirm dialog.
func TestConvertToLocalConfirmPoints_OutOfZoneWarning(t *testing.T) {
	inZone := convertToLocalConfirmPoints(domain.ShippingMethodLocalDelivery, false)
	outOfZone := convertToLocalConfirmPoints(domain.ShippingMethodLocalDelivery, true)

	assert.Len(t, outOfZone, len(inZone)+1)
	assert.Contains(t, outOfZone[len(outOfZone)-1], "outside the configured local zone")

	// The shipping refund is the point that costs real money; it must be on
	// every variant.
	for _, points := range [][]string{inZone, outOfZone} {
		var found bool
		for _, p := range points {
			if assert.ObjectsAreEqual(p, "Any shipping already charged is not refunded — issue that from the refund flow if it is owed.") {
				found = true
			}
		}
		assert.True(t, found, "the shipping-refund caveat must always be shown")
	}
}
