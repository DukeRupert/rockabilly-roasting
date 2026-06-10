package shipping

import "context"

// LabelRequest contains the data needed to create a shipping label.
//
// FromEmail and FromPhone are required by USPS via Shippo (transactions fail
// with "Seller info missing email or phone" when omitted). ToEmail/ToPhone
// are optional but improve carrier-side tracking notifications.
type LabelRequest struct {
	FromName    string
	FromStreet1 string
	FromCity    string
	FromState   string
	FromZip     string
	FromCountry string
	FromEmail   string
	FromPhone   string
	ToName      string
	ToStreet1   string
	ToCity      string
	ToState     string
	ToZip       string
	ToCountry   string
	ToEmail     string
	ToPhone     string
	WeightOz    float64
	LengthIn    float64
	WidthIn     float64
	HeightIn    float64
	ServiceCode string
	Reference   string
}

// LabelResult contains the result of creating a shipping label.
type LabelResult struct {
	TrackingNumber string
	LabelURL       string
	CarrierName    string
	ServiceName    string
	RateCents      int
	Currency       string
}

// Rate is one purchasable shipping option returned by GetRates. RateID and
// ShipmentID are opaque, provider-specific handles — BuyRate uses them to
// purchase this exact quoted rate. The remaining fields are display and
// persistence data carried through to the LabelResult on purchase.
//
// Rates are time-limited: a provider may reject a stale RateID at BuyRate
// time, in which case the caller should re-fetch and let the operator
// re-confirm against fresh prices.
type Rate struct {
	RateID          string
	ShipmentID      string // unused by Shippo (rate IDs are globally buyable); EasyPost needs it
	CarrierName     string
	ServiceName     string
	ServiceToken    string
	AmountCents     int
	Currency        string
	EstDeliveryDays int // 0 when the carrier returns no estimate
}

// LabelProvider is the interface for shipping label services.
//
// Two purchase paths are supported:
//   - GetRates + BuyRate: fetch all options, let an operator pick one, buy it.
//   - CreateLabel: one-shot — create, pick by service code (cheapest default),
//     and buy. Used by the bulk-label flow where no per-order review happens.
type LabelProvider interface {
	GetRates(ctx context.Context, req LabelRequest) ([]Rate, error)
	BuyRate(ctx context.Context, rate Rate) (*LabelResult, error)
	CreateLabel(ctx context.Context, req LabelRequest) (*LabelResult, error)
	SupportedServices(ctx context.Context) ([]string, error)
}
