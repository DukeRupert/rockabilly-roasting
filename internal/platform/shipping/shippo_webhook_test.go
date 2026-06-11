package shipping_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/shipping"
)

func TestParseShippoTrackingWebhook(t *testing.T) {
	t.Run("track_updated payload", func(t *testing.T) {
		body := []byte(`{
			"event": "track_updated",
			"test": false,
			"data": {
				"carrier": "usps",
				"tracking_number": "9400111899223811234567",
				"tracking_status": {
					"status": "DELIVERED",
					"status_details": "Your shipment has been delivered.",
					"status_date": "2026-06-10T12:00:00Z"
				}
			}
		}`)
		evt, err := shipping.ParseShippoTrackingWebhook(body)
		require.NoError(t, err)
		assert.Equal(t, "track_updated", evt.Event)
		assert.Equal(t, "9400111899223811234567", evt.Data.TrackingNumber)
		assert.Equal(t, "usps", evt.Data.Carrier)
		assert.Equal(t, shipping.ShippoTrackDelivered, evt.Data.TrackingStatus.Status)
	})

	t.Run("non-tracking event decodes without error", func(t *testing.T) {
		evt, err := shipping.ParseShippoTrackingWebhook([]byte(`{"event":"transaction_created","data":{}}`))
		require.NoError(t, err)
		assert.Equal(t, "transaction_created", evt.Event)
		assert.Empty(t, evt.Data.TrackingNumber)
	})

	t.Run("invalid JSON errors", func(t *testing.T) {
		_, err := shipping.ParseShippoTrackingWebhook([]byte(`not json`))
		require.Error(t, err)
	})
}
