package shipping

import (
	"encoding/json"
	"fmt"
)

// Shippo tracking status tokens, as delivered in a track_updated webhook's
// tracking_status.status field. The full set per Shippo's tracking API.
const (
	ShippoTrackUnknown    = "UNKNOWN"
	ShippoTrackPreTransit = "PRE_TRANSIT"
	ShippoTrackTransit    = "TRANSIT"
	ShippoTrackDelivered  = "DELIVERED"
	ShippoTrackReturned   = "RETURNED"
	ShippoTrackFailure    = "FAILURE"
)

// ShippoTrackingWebhook is the slice of a Shippo webhook payload we act on.
// Shippo wraps the tracking object under "data" with the event name alongside;
// only the fields we use are declared (json.Unmarshal ignores the rest).
type ShippoTrackingWebhook struct {
	Event string                `json:"event"`
	Test  bool                  `json:"test"`
	Data  ShippoTrackingPayload `json:"data"`
}

// ShippoTrackingPayload is the Tracking object carried by a track_updated event.
type ShippoTrackingPayload struct {
	Carrier        string               `json:"carrier"`
	TrackingNumber string               `json:"tracking_number"`
	TrackingStatus ShippoTrackingStatus `json:"tracking_status"`
}

// ShippoTrackingStatus is the current status block of a tracking object.
type ShippoTrackingStatus struct {
	Status        string `json:"status"`
	StatusDetails string `json:"status_details"`
	StatusDate    string `json:"status_date"`
}

// ParseShippoTrackingWebhook decodes a Shippo webhook body. It does not decide
// whether the event is actionable — callers inspect Event, TrackingNumber and
// TrackingStatus.Status. Returns an error only when the body is not valid JSON.
func ParseShippoTrackingWebhook(body []byte) (*ShippoTrackingWebhook, error) {
	var evt ShippoTrackingWebhook
	if err := json.Unmarshal(body, &evt); err != nil {
		return nil, fmt.Errorf("parse shippo tracking webhook: %w", err)
	}
	return &evt, nil
}
