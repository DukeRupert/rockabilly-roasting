package admin

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A filter that can only ever return nothing is worse than no filter — it
// invites staff to click it and conclude the feature is broken. So the pickup
// pill appears only for shops that actually offer pickup.
func TestOrderFulfillmentPills_PickupOnlyWhenEnabled(t *testing.T) {
	values := func(pills []adminListPill) []string {
		out := make([]string, 0, len(pills))
		for _, p := range pills {
			out = append(out, p.Value)
		}
		return out
	}

	assert.Equal(t,
		[]string{"", "unfulfilled", "fulfilled", "delivered"},
		values(orderFulfillmentPillsFor(false)))

	// Inserted in the order the states actually occur, not appended at the end.
	assert.Equal(t,
		[]string{"", "unfulfilled", "fulfilled", "ready_for_pickup", "delivered"},
		values(orderFulfillmentPillsFor(true)))

	// The shared slice must not be mutated by building the pickup variant.
	orderFulfillmentPillsFor(true)
	assert.Equal(t,
		[]string{"", "unfulfilled", "fulfilled", "delivered"},
		values(orderFulfillmentPills))
}
