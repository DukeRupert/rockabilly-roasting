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

// LabelProvider is the interface for shipping label services.
type LabelProvider interface {
	CreateLabel(ctx context.Context, req LabelRequest) (*LabelResult, error)
	SupportedServices(ctx context.Context) ([]string, error)
}
