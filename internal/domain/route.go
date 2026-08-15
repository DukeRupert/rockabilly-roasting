package domain

import (
	"time"

	"github.com/google/uuid"
)

// RouteStatus is where a delivery route sits in its short life.
type RouteStatus string

const (
	// RouteStatusDraft — planned but not handed to a driver. Has no share
	// token, so there is no URL that could leak a half-planned stop list.
	RouteStatusDraft RouteStatus = "draft"
	// RouteStatusActive — the driver has it. The share token works.
	RouteStatusActive RouteStatus = "active"
	// RouteStatusCompleted — every stop resolved, or staff ended the run. The
	// share token stops working at this moment.
	RouteStatusCompleted RouteStatus = "completed"
)

// RouteStopStatus is the outcome of one stop.
type RouteStopStatus string

const (
	RouteStopPending   RouteStopStatus = "pending"
	RouteStopDelivered RouteStopStatus = "delivered"
	// RouteStopSkipped — the driver had good reason to pass this stop today.
	// Route-level only: the order is untouched and rolls onto the next run.
	RouteStopSkipped RouteStopStatus = "skipped"
)

// Resolved reports whether a stop still needs the driver's attention.
func (s RouteStopStatus) Resolved() bool {
	return s == RouteStopDelivered || s == RouteStopSkipped
}

// DeliveryRoute is one day's ordered delivery run.
type DeliveryRoute struct {
	ID        uuid.UUID
	RouteDate time.Time
	Status    RouteStatus

	OriginLat     float64
	OriginLng     float64
	OriginAddress string

	TotalDistanceMeters int
	TotalDurationSecs   int
	Roundtrip           bool

	// ShareToken authenticates the driver page. Nil until activation.
	ShareToken *string

	CreatedAt   time.Time
	ActivatedAt *time.Time
	CompletedAt *time.Time
}

// RouteStop is one delivery on a route.
type RouteStop struct {
	ID           uuid.UUID
	RouteID      uuid.UUID
	OrderID      uuid.UUID
	Position     int
	Address      string
	Lat          float64
	Lng          float64
	CustomerName string
	Channel      OrderChannel
	Status       RouteStopStatus
	SkipReason   string
	Notes        string
	DeliveredAt  *time.Time
}

// RouteProgress summarises how far through a run the driver is.
type RouteProgress struct {
	Total     int
	Delivered int
	Skipped   int
}

// Remaining is how many stops still need the driver.
func (p RouteProgress) Remaining() int {
	return p.Total - p.Delivered - p.Skipped
}

// Complete reports whether every stop has been resolved one way or another.
// A route with skipped stops still completes — a skip is a decision, not an
// omission.
func (p RouteProgress) Complete() bool {
	return p.Total > 0 && p.Remaining() == 0
}

// Progress computes progress over a stop list.
func Progress(stops []RouteStop) RouteProgress {
	p := RouteProgress{Total: len(stops)}
	for _, s := range stops {
		switch s.Status {
		case RouteStopDelivered:
			p.Delivered++
		case RouteStopSkipped:
			p.Skipped++
		}
	}
	return p
}
