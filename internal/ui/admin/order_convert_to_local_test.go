package admin

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

// canConvertToLocal decides whether the order page offers the way back off the
// carrier channel. It has to agree with app.ConvertShippedOrderToLocal, because
// a button that submits into a refusal is worse than no button — and until this
// pair existed, the mail-out conversion was a one-way door that could only be
// undone with a hand-written UPDATE against production.

func convertibleOrder(method domain.ShippingMethod) *domain.Order {
	return &domain.Order{
		Status:            domain.OrderStatusConfirmed,
		FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
		ShippingMethod:    method,
	}
}

func TestCanConvertToLocal_ShippedOrderInTheShop(t *testing.T) {
	order := convertibleOrder(domain.ShippingMethodShipped)
	assert.True(t, canConvertToLocal(order, nil))

	// Fulfilled but not yet gone is still in the shop.
	order.FulfillmentStatus = domain.FulfillmentStatusFulfilled
	assert.True(t, canConvertToLocal(order, nil))
}

// A nil method is what imported and pre-local-channel orders carry, and those
// are the ones most likely to be filed on the wrong channel.
// Migration 086 made shipping_method NOT NULL, so the "nil means shipped"
// special case this gate used to carry is gone. The zero value is the only way
// an unset method can now reach here, and it is not the carrier channel — so it
// must not be offered the conversion, the same as any other non-shipped method.
func TestCanConvertToLocal_ZeroValueIsNotTheCarrierChannel(t *testing.T) {
	assert.False(t, canConvertToLocal(convertibleOrder(""), nil))
}

// The local orders get the swap + mail-out controls instead; offering both sets
// at once would put four buttons on one card.
func TestCanConvertToLocal_LocalOrdersAreNotOffered(t *testing.T) {
	assert.False(t, canConvertToLocal(convertibleOrder(domain.ShippingMethodLocalDelivery), nil))
	assert.False(t, canConvertToLocal(convertibleOrder(domain.ShippingMethodPickup), nil))
}

func TestCanConvertToLocal_OrderThatLeftTheShop(t *testing.T) {
	order := convertibleOrder(domain.ShippingMethodShipped)
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
		order := convertibleOrder(domain.ShippingMethodShipped)
		order.Status = st
		assert.False(t, canConvertToLocal(order, nil), "status %s", st)
	}
}

// Postage that is bought and unrefunded is the one hard blocker: converting
// would put the order on the van while the label stays spendable. BlocksRebuy
// is the shared rule, so the gate tracks it rather than restating it.
func TestCanConvertToLocal_LiveLabelBlocks(t *testing.T) {
	order := convertibleOrder(domain.ShippingMethodShipped)

	live := []domain.Shipment{{RefundStatus: domain.RefundStatusNone}}
	assert.False(t, canConvertToLocal(order, live))

	// A rejected refund means the label was found to be used — still live.
	rejected := []domain.Shipment{{RefundStatus: domain.RefundStatusFailed}}
	assert.False(t, canConvertToLocal(order, rejected))
}

func TestCanConvertToLocal_RefundedLabelUnblocks(t *testing.T) {
	order := convertibleOrder(domain.ShippingMethodShipped)
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

// shouldOfferConvertToLocal gates the Shipping card itself, not just the
// buttons in it. A plain retail mail-out is written with shipping_method NULL
// and carries no dates, no PO and no subscription, so hasShippingDetails alone
// renders no card — which hid this feature from most of the orders it exists
// for until the call site started ORing the two.
func TestShouldOfferConvertToLocal_PlainMailOutIsOffered(t *testing.T) {
	props := OrderShowProps{
		Order:                convertibleOrder(domain.ShippingMethodShipped),
		LocalDeliveryEnabled: true,
	}
	assert.True(t, shouldOfferConvertToLocal(props))
}

func TestShouldOfferConvertToLocal_NeedsAChannelToSendItTo(t *testing.T) {
	props := OrderShowProps{Order: convertibleOrder(domain.ShippingMethodShipped)}
	assert.False(t, shouldOfferConvertToLocal(props),
		"no local channel enabled: offering the conversion would strand the order")

	props.LocalPickupEnabled = true
	assert.True(t, shouldOfferConvertToLocal(props))
}

func TestShouldOfferConvertToLocal_TracksTheEligibilityGate(t *testing.T) {
	props := OrderShowProps{
		Order:                convertibleOrder(domain.ShippingMethodShipped),
		LocalDeliveryEnabled: true,
		Shipments:            []domain.Shipment{{RefundStatus: domain.RefundStatusNone}},
	}
	assert.False(t, shouldOfferConvertToLocal(props), "a live label still blocks")
}

// The pickup variants of the copy helpers had no test, so their branches could
// be deleted without the suite noticing.
func TestConvertToLocalCopy_PickupVariants(t *testing.T) {
	assert.Equal(t, "Convert this order to local pickup?",
		convertToLocalConfirmTitle(domain.ShippingMethodPickup))
	assert.Equal(t, "Convert this order to local delivery?",
		convertToLocalConfirmTitle(domain.ShippingMethodLocalDelivery))

	pickupHint := convertToLocalHint(domain.ShippingMethodPickup, false)
	assert.Contains(t, pickupHint, "pickup shelf")
	assert.NotContains(t, pickupHint, "next run")

	deliveryHint := convertToLocalHint(domain.ShippingMethodLocalDelivery, false)
	assert.Contains(t, deliveryHint, "next run")

	// The out-of-zone suffix must reach the hint on both targets.
	assert.Contains(t, convertToLocalHint(domain.ShippingMethodPickup, true),
		"outside the local zone")
	assert.Contains(t, convertToLocalHint(domain.ShippingMethodLocalDelivery, true),
		"outside the local zone")
}

func TestConvertToLocalConfirmPoints_PickupSaysItIsNotOnARun(t *testing.T) {
	points := convertToLocalConfirmPoints(domain.ShippingMethodPickup, false)
	assert.Contains(t, points[0], "will not appear on a delivery run")

	delivery := convertToLocalConfirmPoints(domain.ShippingMethodLocalDelivery, false)
	assert.Contains(t, delivery[0], "joins the delivery queue")

	// "Not notified" is the point that stops staff assuming the customer knows.
	for _, ps := range [][]string{points, delivery} {
		assert.Contains(t, ps, "The customer is not notified of the change.")
	}
}

// The gate functions agreeing is not the same as the control reaching the page.
// This shape — a plain retail mail-out with nothing else the card displays — is
// the one the control was invisible on before migration 086, when such an order
// carried a NULL method and the card was gated on having something to display.
// Render the whole page rather than trusting the helpers.
func TestOrderShowContent_PlainMailOutGetsTheConvertControl(t *testing.T) {
	order := &domain.Order{
		ID:                uuid.New(),
		Status:            domain.OrderStatusConfirmed,
		FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
		CurrencyCode:      "usd",
		ShippingMethod:    domain.ShippingMethodShipped,
	}
	props := OrderShowProps{
		Order:                order,
		MerchantTZ:           denver,
		LocalDeliveryEnabled: true,
	}

	var sb strings.Builder
	require := assert.New(t)
	require.NoError(OrderShowContent(props).Render(context.Background(), &sb))
	require.Contains(sb.String(), "convert-to-local",
		"a plain mail-out must be offered the way back")
	require.Contains(sb.String(), "Convert to local delivery")
}

// The counterpart: with no local channel enabled there is nothing to convert to,
// so the buttons stay away — but the card itself still renders, because every
// order now carries a method worth showing. Before migration 086 this shape had
// no method at all and the card was suppressed to avoid an empty titled box;
// that box is no longer reachable.
func TestOrderShowContent_NoLocalChannelStillShowsTheCardWithoutButtons(t *testing.T) {
	order := &domain.Order{
		ID:                uuid.New(),
		Status:            domain.OrderStatusConfirmed,
		FulfillmentStatus: domain.FulfillmentStatusUnfulfilled,
		CurrencyCode:      "usd",
		ShippingMethod:    domain.ShippingMethodShipped,
	}
	props := OrderShowProps{Order: order, MerchantTZ: denver}

	var sb strings.Builder
	assert.NoError(t, OrderShowContent(props).Render(context.Background(), &sb))
	assert.NotContains(t, sb.String(), "convert-to-local",
		"no local channel enabled: nothing to convert to")
	assert.Contains(t, sb.String(), ">Shipping<",
		"the card still renders — the method is always worth showing")
}
