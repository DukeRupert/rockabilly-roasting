package storefront

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The subscription card is the surface the guides tell customers to trust, and
// it was rendering two values from two different zones: next_order_at as pgx
// hands it over (UTC on the server) and the resume preview in merchant-local
// time. Both printed the same day only because renewals are anchored at 2am,
// where Los Angeles and UTC agree — a setting, not a property.
func TestFormatDateInUsesTheMerchantZone(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)

	// 10pm in Los Angeles is already tomorrow in UTC. A customer reading their
	// own card must see their own day.
	evening := time.Date(2027, 3, 12, 22, 0, 0, 0, la)
	assert.Equal(t, "Mar 12, 2027", formatDateIn(evening.UTC(), la),
		"a UTC-located value must still print the merchant's day")
	assert.Equal(t, "Mar 13, 2027", formatDateIn(evening, time.UTC),
		"and the zone applied is the one passed, not the one the value carries")

	// A missing zone must not take down the page a customer opens to fix their
	// subscription.
	assert.NotPanics(t, func() { formatDateIn(evening, nil) })
	assert.Equal(t, "Mar 13, 2027", formatDateIn(evening, nil))
}
