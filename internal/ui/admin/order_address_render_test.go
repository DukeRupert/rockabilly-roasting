package admin

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func addressCardProps(status domain.OrderStatus, fulfillment domain.FulfillmentStatus) OrderShowProps {
	return OrderShowProps{
		Order: &domain.Order{ID: uuid.New(), Status: status, FulfillmentStatus: fulfillment},
		ShippingAddress: &domain.Address{
			FirstName: "Jennifer", LastName: "Waltz", Line1: "1221 W Tamarisk Ave",
			City: "Ridgecrest", State: "CA", PostalCode: "93555",
		},
	}
}

func TestOrderAddressCard_EditFormFieldsMatchHandler(t *testing.T) {
	props := addressCardProps(domain.OrderStatusProcessing, domain.FulfillmentStatusFulfilled)
	props.AddressSubscriptions = 2
	html := renderPage(t, orderAddressCard(props))

	assert.Contains(t, html, "/shipping-address")
	for _, name := range []string{"first_name", "last_name", "company", "line1", "line2", "city", "state", "postal_code", "move_subscriptions"} {
		assert.Contains(t, html, `name="`+name+`"`, name)
	}
	assert.Contains(t, html, "2 subscriptions")
}

func TestOrderAddressCard_NoSubscriptionBoxWithoutSubscriptions(t *testing.T) {
	html := renderPage(t, orderAddressCard(addressCardProps(domain.OrderStatusConfirmed, domain.FulfillmentStatusUnfulfilled)))
	assert.Contains(t, html, `name="postal_code"`)
	assert.NotContains(t, html, "move_subscriptions")
}

func TestOrderAddressCard_LockReasonReplacesForm(t *testing.T) {
	props := addressCardProps(domain.OrderStatusProcessing, domain.FulfillmentStatusFulfilled)
	props.AddressLock = "order already has an active shipping label"
	html := renderPage(t, orderAddressCard(props))

	assert.NotContains(t, html, "/shipping-address")
	assert.Contains(t, html, "Address locked: order already has an active shipping label")
}

func TestOrderAddressCard_NoLockNoteOnShippedOrder(t *testing.T) {
	props := addressCardProps(domain.OrderStatusComplete, domain.FulfillmentStatusDelivered)
	props.AddressLock = "this order's address can no longer be changed"
	html := renderPage(t, orderAddressCard(props))

	assert.NotContains(t, html, "/shipping-address")
	assert.NotContains(t, html, "Address locked")
}
