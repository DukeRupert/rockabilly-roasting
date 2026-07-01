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
	// LabelFileType is the Shippo label_file_type to request (e.g. "PDF_4x6",
	// "PNG"). Empty means the provider default ("PDF", an 8.5x11 page). Only the
	// one-shot CreateLabel path honors this; the GetRates+BuyRate path carries
	// the choice on the Rate instead.
	LabelFileType string
}

// LabelResult contains the result of creating a shipping label.
type LabelResult struct {
	TrackingNumber string
	LabelURL       string
	CarrierName    string
	ServiceName    string
	RateCents      int
	Currency       string
	// ProviderTransactionID is the carrier's opaque transaction handle for this
	// purchase (Shippo's transaction object_id). It's persisted so the label can
	// later be refunded. Providers that can't surface it leave it empty.
	ProviderTransactionID string
}

// RefundState is the provider-neutral status of a label refund request. The app
// layer maps it onto domain.RefundStatus — the provider stays domain-free.
type RefundState string

const (
	// RefundPending — the refund is submitted and still resolving (Shippo
	// QUEUED or PENDING). The caller should poll again later.
	RefundPending RefundState = "pending"
	// RefundSuccess — the refund was accepted. Terminal.
	RefundSuccess RefundState = "success"
	// RefundError — the refund was rejected (label used/scanned). Terminal.
	RefundError RefundState = "error"
)

// RefundResult is the outcome of requesting or polling a label refund. RefundID
// is the carrier's refund handle, used to poll for resolution.
type RefundResult struct {
	RefundID string
	State    RefundState
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
	EstDeliveryDays int    // 0 when the carrier returns no estimate
	LabelFileType   string // Shippo label_file_type for purchase; empty = "PDF"
}

// AllowedLabelFileTypes are the Shippo label_file_type values supported by the
// label flow and smoke test. Empty/default resolves to "PDF". PDF_4x6 is the
// 4x6 thermal-label size; PDF is a full 8.5x11 page; PNG is an image.
var AllowedLabelFileTypes = []string{"PDF", "PDF_4x6", "PNG"}

// ValidLabelFileType reports whether v is empty (provider default) or one of
// AllowedLabelFileTypes.
func ValidLabelFileType(v string) bool {
	if v == "" {
		return true
	}
	for _, a := range AllowedLabelFileTypes {
		if a == v {
			return true
		}
	}
	return false
}

// LabelProvider is the interface for shipping label services.
//
// Two purchase paths are supported:
//   - GetRates + BuyRate: fetch all options, let an operator pick one, buy it.
//   - CreateLabel: one-shot — create, pick by service code (cheapest default),
//     and buy. Used by the bulk-label flow where no per-order review happens.
//
// Refunds are asynchronous: RequestRefund submits the request and returns a
// refund handle in a pending state; GetRefund polls that handle until it
// settles to success or error.
type LabelProvider interface {
	GetRates(ctx context.Context, req LabelRequest) ([]Rate, error)
	BuyRate(ctx context.Context, rate Rate) (*LabelResult, error)
	CreateLabel(ctx context.Context, req LabelRequest) (*LabelResult, error)
	SupportedServices(ctx context.Context) ([]string, error)
	// RequestRefund asks the carrier to refund the label identified by its
	// transaction ID. The returned RefundResult carries a RefundID for polling.
	RequestRefund(ctx context.Context, transactionID string) (*RefundResult, error)
	// GetRefund fetches the current state of a previously requested refund.
	GetRefund(ctx context.Context, refundID string) (*RefundResult, error)
}
