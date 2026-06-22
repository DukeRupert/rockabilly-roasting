package shipping

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"time"
)

// Default Shippo service token when LabelRequest.ServiceCode is empty.
// USPS Ground Advantage is the cheapest USPS service for small ground parcels
// and the merchant's default for retail coffee orders.
const ShippoDefaultServiceToken = "usps_ground_advantage"

// ShippoAPIBase is the Shippo REST API root. Overridable via NewShippoProviderWithBase
// for tests against a mock server.
const ShippoAPIBase = "https://api.goshippo.com"

// ShippoProvider implements LabelProvider against the Shippo REST API.
// Uses raw net/http — Shippo's surface area for label buying is two endpoints
// (POST /shipments, POST /transactions), not worth a third-party SDK.
type ShippoProvider struct {
	apiKey string
	// defaultLabelFileType is the label_file_type used when a purchase doesn't
	// specify one on the Rate. Empty resolves to "PDF" at buy time. Set via
	// WithDefaultLabelFileType from the SHIPPO_LABEL_FORMAT env var.
	defaultLabelFileType string
	baseURL              string
	client               *http.Client
}

// NewShippoProvider creates a LabelProvider backed by Shippo's REST API.
func NewShippoProvider(apiKey string) *ShippoProvider {
	return &ShippoProvider{
		apiKey:  apiKey,
		baseURL: ShippoAPIBase,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// WithDefaultLabelFileType sets the label_file_type used for purchases that
// don't specify one per-Rate (the admin GetRates+BuyRate flow and the bulk
// label job). Returns the provider for chaining. An empty or unrecognized
// value leaves the default unchanged.
func (p *ShippoProvider) WithDefaultLabelFileType(ft string) *ShippoProvider {
	if ValidLabelFileType(ft) && ft != "" {
		p.defaultLabelFileType = ft
	}
	return p
}

// NewShippoProviderWithBase is for tests; allows pointing at a mock server.
func NewShippoProviderWithBase(apiKey, baseURL string, client *http.Client) *ShippoProvider {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &ShippoProvider{apiKey: apiKey, baseURL: baseURL, client: client}
}

// CreateLabel creates a Shippo shipment (which returns rates inline when
// async=false), selects the rate matching the requested service token, and
// purchases the label via POST /transactions.
//
// If ServiceCode is empty, ShippoDefaultServiceToken is used. If the requested
// service is not in the returned rates, the call fails — we don't silently
// fall back to a different carrier or service.
func (p *ShippoProvider) CreateLabel(ctx context.Context, req LabelRequest) (*LabelResult, error) {
	serviceToken := req.ServiceCode
	if serviceToken == "" {
		serviceToken = ShippoDefaultServiceToken
	}

	shipResp, err := p.createShipment(ctx, req)
	if err != nil {
		return nil, err
	}

	rate, err := pickRate(shipResp.Rates, serviceToken)
	if err != nil {
		return nil, err
	}

	rateCents, err := dollarsStringToCents(rate.Amount)
	if err != nil {
		return nil, fmt.Errorf("shippo: parse rate %q: %w", rate.Amount, err)
	}

	return p.BuyRate(ctx, Rate{
		RateID:        rate.ObjectID,
		CarrierName:   rate.Provider,
		ServiceName:   rate.ServiceLevel.Name,
		ServiceToken:  rate.ServiceLevel.Token,
		AmountCents:   rateCents,
		Currency:      rate.Currency,
		LabelFileType: req.LabelFileType,
	})
}

// GetRates creates a Shippo shipment and returns every quoted rate, cheapest
// first. The returned RateIDs are buyable via BuyRate until Shippo expires
// them (typically minutes). Fails if Shippo returns no rates for the parcel.
func (p *ShippoProvider) GetRates(ctx context.Context, req LabelRequest) ([]Rate, error) {
	shipResp, err := p.createShipment(ctx, req)
	if err != nil {
		return nil, err
	}
	if len(shipResp.Rates) == 0 {
		return nil, fmt.Errorf("shippo: no rates returned: %s", joinMessages(shipResp.Messages))
	}

	rates := make([]Rate, 0, len(shipResp.Rates))
	for _, r := range shipResp.Rates {
		cents, cErr := dollarsStringToCents(r.Amount)
		if cErr != nil {
			return nil, fmt.Errorf("shippo: parse rate %q: %w", r.Amount, cErr)
		}
		rates = append(rates, Rate{
			RateID:          r.ObjectID,
			CarrierName:     r.Provider,
			ServiceName:     r.ServiceLevel.Name,
			ServiceToken:    r.ServiceLevel.Token,
			AmountCents:     cents,
			Currency:        r.Currency,
			EstDeliveryDays: r.EstimatedDays,
		})
	}
	sort.Slice(rates, func(i, j int) bool { return rates[i].AmountCents < rates[j].AmountCents })
	return rates, nil
}

// BuyRate purchases a specific quoted rate via POST /transactions. The carrier,
// service, and cost come from the rate snapshot (the transaction response only
// returns tracking + label URL); tracking and label URL come from Shippo.
func (p *ShippoProvider) BuyRate(ctx context.Context, rate Rate) (*LabelResult, error) {
	// Precedence: per-purchase Rate override, then the provider default
	// (SHIPPO_LABEL_FORMAT), then a hard "PDF" fallback.
	fileType := rate.LabelFileType
	if fileType == "" {
		fileType = p.defaultLabelFileType
	}
	if fileType == "" {
		fileType = "PDF"
	}
	txReq := shippoTransactionReq{
		Rate:          rate.RateID,
		Async:         false,
		LabelFileType: fileType,
	}
	var txResp shippoTransactionResp
	if err := p.post(ctx, "/transactions", txReq, &txResp); err != nil {
		return nil, fmt.Errorf("shippo create transaction: %w", err)
	}

	if txResp.Status != "SUCCESS" {
		return nil, fmt.Errorf("shippo transaction status %q: %s", txResp.Status, joinMessages(txResp.Messages))
	}

	return &LabelResult{
		TrackingNumber: txResp.TrackingNumber,
		LabelURL:       txResp.LabelURL,
		CarrierName:    rate.CarrierName,
		ServiceName:    rate.ServiceName,
		RateCents:      rate.AmountCents,
		Currency:       rate.Currency,
	}, nil
}

// createShipment posts a synchronous (async=false) shipment so the response
// carries the rates inline. Shared by CreateLabel and GetRates.
func (p *ShippoProvider) createShipment(ctx context.Context, req LabelRequest) (*shippoShipmentResp, error) {
	shipReq := shippoShipmentReq{
		AddressFrom: shippoAddress{
			Name:    req.FromName,
			Street1: req.FromStreet1,
			City:    req.FromCity,
			State:   req.FromState,
			Zip:     req.FromZip,
			Country: req.FromCountry,
			Email:   req.FromEmail,
			Phone:   req.FromPhone,
		},
		AddressTo: shippoAddress{
			Name:    req.ToName,
			Street1: req.ToStreet1,
			City:    req.ToCity,
			State:   req.ToState,
			Zip:     req.ToZip,
			Country: req.ToCountry,
			Email:   req.ToEmail,
			Phone:   req.ToPhone,
		},
		Parcels: []shippoParcel{{
			Length:       formatFloat(req.LengthIn),
			Width:        formatFloat(req.WidthIn),
			Height:       formatFloat(req.HeightIn),
			DistanceUnit: "in",
			Weight:       formatFloat(req.WeightOz),
			MassUnit:     "oz",
		}},
		Async:    false,
		Metadata: req.Reference,
	}

	var shipResp shippoShipmentResp
	if err := p.post(ctx, "/shipments", shipReq, &shipResp); err != nil {
		return nil, fmt.Errorf("shippo create shipment: %w", err)
	}
	return &shipResp, nil
}

// SupportedServices returns the common USPS service tokens Rockabilly ships
// with. Other carriers and services configured in the Shippo dashboard
// remain available — callers can pass any servicelevel.token in
// LabelRequest.ServiceCode.
func (p *ShippoProvider) SupportedServices(_ context.Context) ([]string, error) {
	return []string{
		"usps_ground_advantage",
		"usps_priority",
		"usps_priority_express",
	}, nil
}

// post sends a JSON body to the given Shippo endpoint and decodes the response
// into out. Non-2xx responses produce an error including the response body
// (Shippo returns helpful field-level errors in its 400s).
func (p *ShippoProvider) post(ctx context.Context, path string, body, out any) error {
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+path, bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "ShippoToken "+p.apiKey)

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("do: %w", err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("status %d: %s", resp.StatusCode, string(respBytes))
	}

	if err := json.Unmarshal(respBytes, out); err != nil {
		return fmt.Errorf("decode: %w", err)
	}
	return nil
}

// pickRate returns the rate whose servicelevel.token matches serviceToken
// exactly. We deliberately don't fall back to another service — if Ground
// Advantage was requested and isn't available (e.g. weight too high for the
// service, or zone not eligible) the operator should know rather than be
// silently upgraded to Priority.
func pickRate(rates []shippoRate, serviceToken string) (*shippoRate, error) {
	if len(rates) == 0 {
		return nil, errors.New("shippo: no rates returned")
	}
	for i := range rates {
		if rates[i].ServiceLevel.Token == serviceToken {
			return &rates[i], nil
		}
	}
	return nil, fmt.Errorf("shippo: no rate for service %q (available: %s)", serviceToken, availableTokens(rates))
}

func availableTokens(rates []shippoRate) string {
	out := ""
	for i, r := range rates {
		if i > 0 {
			out += ", "
		}
		out += r.ServiceLevel.Token
	}
	return out
}

func joinMessages(msgs []shippoMessage) string {
	out := ""
	for i, m := range msgs {
		if i > 0 {
			out += "; "
		}
		out += m.Text
	}
	return out
}

// formatFloat renders a parcel dimension/weight for Shippo. It rounds to 2
// decimal places first: the raw value can be a long repeating decimal (e.g. a
// grams→ounces conversion yields 13.365314714921473), and Shippo rejects any
// parcel field with more than 10 digits total. Two decimals is ample precision
// for shipping — carriers round up to the ounce anyway. Precision -1 then trims
// trailing zeros so 12.5 stays "12.5" rather than "12.50".
func formatFloat(f float64) string {
	rounded := math.Round(f*100) / 100
	return strconv.FormatFloat(rounded, 'f', -1, 64)
}

// dollarsStringToCents converts "7.58" → 758. Shippo returns rate amounts as
// strings (e.g. "8.45"); using a string→float→cents round-trip avoids
// floating-point widening from JSON numbers.
func dollarsStringToCents(s string) (int, error) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, err
	}
	return int(math.Round(f * 100)), nil
}

// --- Shippo request/response shapes ---
//
// Only the fields we actually use are declared. The full Shippo schema is
// much wider; json.Unmarshal silently ignores anything unmapped here.

type shippoAddress struct {
	Name    string `json:"name"`
	Street1 string `json:"street1"`
	City    string `json:"city"`
	State   string `json:"state"`
	Zip     string `json:"zip"`
	Country string `json:"country"`
	Email   string `json:"email,omitempty"`
	Phone   string `json:"phone,omitempty"`
}

type shippoParcel struct {
	Length       string `json:"length"`
	Width        string `json:"width"`
	Height       string `json:"height"`
	DistanceUnit string `json:"distance_unit"`
	Weight       string `json:"weight"`
	MassUnit     string `json:"mass_unit"`
}

type shippoShipmentReq struct {
	AddressFrom shippoAddress  `json:"address_from"`
	AddressTo   shippoAddress  `json:"address_to"`
	Parcels     []shippoParcel `json:"parcels"`
	Async       bool           `json:"async"`
	Metadata    string         `json:"metadata,omitempty"`
}

type shippoServiceLevel struct {
	Name  string `json:"name"`
	Token string `json:"token"`
}

type shippoRate struct {
	ObjectID      string             `json:"object_id"`
	Amount        string             `json:"amount"`
	Currency      string             `json:"currency"`
	Provider      string             `json:"provider"`
	EstimatedDays int                `json:"estimated_days"`
	ServiceLevel  shippoServiceLevel `json:"servicelevel"`
}

type shippoMessage struct {
	Source string `json:"source"`
	Code   string `json:"code"`
	Text   string `json:"text"`
}

type shippoShipmentResp struct {
	ObjectID string          `json:"object_id"`
	Status   string          `json:"status"`
	Rates    []shippoRate    `json:"rates"`
	Messages []shippoMessage `json:"messages"`
}

type shippoTransactionReq struct {
	Rate          string `json:"rate"`
	Async         bool   `json:"async"`
	LabelFileType string `json:"label_file_type"`
}

type shippoTransactionResp struct {
	ObjectID       string          `json:"object_id"`
	Status         string          `json:"status"`
	TrackingNumber string          `json:"tracking_number"`
	LabelURL       string          `json:"label_url"`
	Messages       []shippoMessage `json:"messages"`
}
