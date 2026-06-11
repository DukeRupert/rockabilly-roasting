package shipping_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/shipping"
)

// TestShippoProvider_CreateLabel_HappyPath drives the provider end-to-end
// against a httptest server that returns a fixed shipment+transaction pair.
// Verifies the auth header, request bodies, service-token selection, and the
// LabelResult mapping.
func TestShippoProvider_CreateLabel_HappyPath(t *testing.T) {
	var sawShipmentBody, sawTransactionBody string
	var sawAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/shipments":
			sawShipmentBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"object_id": "ship_1",
				"status": "SUCCESS",
				"rates": [
					{"object_id": "rate_priority", "amount": "12.50", "currency": "USD", "provider": "USPS", "servicelevel": {"name": "Priority Mail", "token": "usps_priority"}},
					{"object_id": "rate_ground",   "amount": "7.58",  "currency": "USD", "provider": "USPS", "servicelevel": {"name": "Ground Advantage", "token": "usps_ground_advantage"}}
				]
			}`)) //nolint:errcheck
		case "/transactions":
			sawTransactionBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"object_id": "tx_1",
				"status": "SUCCESS",
				"tracking_number": "9400111899223811234567",
				"label_url": "https://shippo-delivery-east.s3.amazonaws.com/abc.pdf"
			}`)) //nolint:errcheck
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("test-key", srv.URL, srv.Client())

	result, err := p.CreateLabel(context.Background(), shipping.LabelRequest{
		FromName:    "Rockabilly Roasting",
		FromStreet1: "101 W Kennewick Ave",
		FromCity:    "Kennewick",
		FromState:   "WA",
		FromZip:     "99336",
		FromCountry: "US",
		ToName:      "Alex Customer",
		ToStreet1:   "1 Main St",
		ToCity:      "San Francisco",
		ToState:     "CA",
		ToZip:       "94103",
		ToCountry:   "US",
		WeightOz:    12.5,
		LengthIn:    10,
		WidthIn:     8,
		HeightIn:    4,
		ServiceCode: "usps_ground_advantage",
		Reference:   "RR-1234",
	})
	require.NoError(t, err)

	assert.Equal(t, "ShippoToken test-key", sawAuth)

	// Shipment request: parcel dimensions + units.
	var shipReq map[string]any
	require.NoError(t, json.Unmarshal([]byte(sawShipmentBody), &shipReq))
	assert.Equal(t, false, shipReq["async"], "async should be false for sync rates")
	assert.Equal(t, "RR-1234", shipReq["metadata"])
	parcels := shipReq["parcels"].([]any)
	require.Len(t, parcels, 1)
	parcel := parcels[0].(map[string]any)
	assert.Equal(t, "in", parcel["distance_unit"])
	assert.Equal(t, "oz", parcel["mass_unit"])
	assert.Equal(t, "12.5", parcel["weight"])

	// Transaction request: picks the ground advantage rate by token.
	var txReq map[string]any
	require.NoError(t, json.Unmarshal([]byte(sawTransactionBody), &txReq))
	assert.Equal(t, "rate_ground", txReq["rate"])
	assert.Equal(t, "PDF", txReq["label_file_type"])

	// Result mapping.
	assert.Equal(t, "9400111899223811234567", result.TrackingNumber)
	assert.Equal(t, "USPS", result.CarrierName)
	assert.Equal(t, "Ground Advantage", result.ServiceName)
	assert.Equal(t, 758, result.RateCents)
	assert.Equal(t, "USD", result.Currency)
}

// TestShippoProvider_CreateLabel_LongDecimalWeight verifies a parcel weight
// with a long decimal tail (as produced by the grams→ounces conversion) is
// rounded to 2 decimals before it reaches Shippo. Shippo rejects any parcel
// field with more than 10 digits total, so the raw 13.365314714921473 would
// otherwise 400.
func TestShippoProvider_CreateLabel_LongDecimalWeight(t *testing.T) {
	var sawShipmentBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/shipments":
			sawShipmentBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{
				"object_id": "ship_1",
				"status": "SUCCESS",
				"rates": [
					{"object_id": "rate_ground", "amount": "7.58", "currency": "USD", "provider": "USPS", "servicelevel": {"name": "Ground Advantage", "token": "usps_ground_advantage"}}
				]
			}`)) //nolint:errcheck
		case "/transactions":
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"object_id": "tx_1", "status": "SUCCESS", "tracking_number": "94001", "label_url": "https://example.com/l.pdf"}`)) //nolint:errcheck
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("test-key", srv.URL, srv.Client())

	_, err := p.CreateLabel(context.Background(), shipping.LabelRequest{
		ServiceCode: "usps_ground_advantage",
		WeightOz:    13.365314714921473,
		LengthIn:    10, WidthIn: 8, HeightIn: 4,
	})
	require.NoError(t, err)

	var shipReq map[string]any
	require.NoError(t, json.Unmarshal([]byte(sawShipmentBody), &shipReq))
	parcel := shipReq["parcels"].([]any)[0].(map[string]any)
	assert.Equal(t, "13.37", parcel["weight"], "weight should be rounded to 2 decimals")
}

// TestShippoProvider_CreateLabel_NoMatchingService verifies the provider
// surfaces an error (rather than silently picking another carrier) when the
// requested service token isn't in the returned rates.
func TestShippoProvider_CreateLabel_NoMatchingService(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shipments" {
			t.Fatalf("transaction should not be requested when rate match fails")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"object_id": "ship_1",
			"status": "SUCCESS",
			"rates": [
				{"object_id": "rate_priority", "amount": "12.50", "currency": "USD", "provider": "USPS", "servicelevel": {"name": "Priority Mail", "token": "usps_priority"}}
			]
		}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("test-key", srv.URL, srv.Client())

	_, err := p.CreateLabel(context.Background(), shipping.LabelRequest{
		ServiceCode: "usps_ground_advantage",
		WeightOz:    8,
		LengthIn:    10, WidthIn: 8, HeightIn: 4,
	})
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "no rate for service"))
	assert.True(t, strings.Contains(err.Error(), "usps_priority"), "error should list available services")
}

// TestShippoProvider_DefaultServiceToken verifies the empty-ServiceCode path
// falls back to ShippoDefaultServiceToken (usps_ground_advantage).
func TestShippoProvider_DefaultServiceToken(t *testing.T) {
	var sawTxReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/shipments":
			w.Write([]byte(`{
				"object_id": "ship_1", "status": "SUCCESS",
				"rates": [{"object_id": "rate_ground", "amount": "5.00", "currency": "USD", "provider": "USPS", "servicelevel": {"name": "Ground Advantage", "token": "usps_ground_advantage"}}]
			}`)) //nolint:errcheck
		case "/transactions":
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &sawTxReq) //nolint:errcheck
			w.Write([]byte(`{"object_id": "tx_1", "status": "SUCCESS", "tracking_number": "T1", "label_url": "https://x/y.pdf"}`)) //nolint:errcheck
		}
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("k", srv.URL, srv.Client())

	_, err := p.CreateLabel(context.Background(), shipping.LabelRequest{
		WeightOz: 4, LengthIn: 6, WidthIn: 4, HeightIn: 2,
		// ServiceCode intentionally empty
	})
	require.NoError(t, err)
	assert.Equal(t, "rate_ground", sawTxReq["rate"])
}

// TestShippoProvider_GetRates verifies GetRates returns every quoted rate,
// sorted cheapest-first, with the estimated-days and token fields mapped
// through — and that it does NOT buy anything (no /transactions call).
func TestShippoProvider_GetRates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/shipments" {
			t.Fatalf("GetRates must not call %s — it only fetches rates", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"object_id": "ship_1",
			"status": "SUCCESS",
			"rates": [
				{"object_id": "rate_priority", "amount": "12.50", "currency": "USD", "provider": "USPS", "estimated_days": 2, "servicelevel": {"name": "Priority Mail", "token": "usps_priority"}},
				{"object_id": "rate_express",  "amount": "28.95", "currency": "USD", "provider": "USPS", "estimated_days": 1, "servicelevel": {"name": "Priority Express", "token": "usps_priority_express"}},
				{"object_id": "rate_ground",   "amount": "7.58",  "currency": "USD", "provider": "USPS", "estimated_days": 4, "servicelevel": {"name": "Ground Advantage", "token": "usps_ground_advantage"}}
			]
		}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("test-key", srv.URL, srv.Client())

	rates, err := p.GetRates(context.Background(), shipping.LabelRequest{
		WeightOz: 12, LengthIn: 10, WidthIn: 8, HeightIn: 4,
	})
	require.NoError(t, err)
	require.Len(t, rates, 3)

	// Cheapest first.
	assert.Equal(t, "rate_ground", rates[0].RateID)
	assert.Equal(t, 758, rates[0].AmountCents)
	assert.Equal(t, "Ground Advantage", rates[0].ServiceName)
	assert.Equal(t, "usps_ground_advantage", rates[0].ServiceToken)
	assert.Equal(t, 4, rates[0].EstDeliveryDays)
	assert.Equal(t, "USD", rates[0].Currency)

	assert.Equal(t, "rate_priority", rates[1].RateID)
	assert.Equal(t, 1250, rates[1].AmountCents)

	assert.Equal(t, "rate_express", rates[2].RateID)
	assert.Equal(t, 2895, rates[2].AmountCents)
}

// TestShippoProvider_GetRates_NoRates surfaces an error (with any Shippo
// messages) when the shipment comes back with no rates.
func TestShippoProvider_GetRates_NoRates(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object_id": "ship_1", "status": "SUCCESS", "rates": [], "messages": [{"text": "Address is undeliverable"}]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("k", srv.URL, srv.Client())

	_, err := p.GetRates(context.Background(), shipping.LabelRequest{WeightOz: 4, LengthIn: 6, WidthIn: 4, HeightIn: 2})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no rates returned")
	assert.Contains(t, err.Error(), "Address is undeliverable")
}

// TestShippoProvider_BuyRate buys a single quoted rate by ID and verifies the
// transaction request and the LabelResult — whose carrier/service/cost come
// from the rate snapshot, not the transaction response.
func TestShippoProvider_BuyRate(t *testing.T) {
	var sawTxReq map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transactions" {
			t.Fatalf("BuyRate must only call /transactions, got %s", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &sawTxReq) //nolint:errcheck
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object_id": "tx_1", "status": "SUCCESS", "tracking_number": "9400111899223811234567", "label_url": "https://shippo/abc.pdf"}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("k", srv.URL, srv.Client())

	result, err := p.BuyRate(context.Background(), shipping.Rate{
		RateID:       "rate_ground",
		CarrierName:  "USPS",
		ServiceName:  "Ground Advantage",
		ServiceToken: "usps_ground_advantage",
		AmountCents:  758,
		Currency:     "USD",
	})
	require.NoError(t, err)

	assert.Equal(t, "rate_ground", sawTxReq["rate"])
	assert.Equal(t, "PDF", sawTxReq["label_file_type"])

	assert.Equal(t, "9400111899223811234567", result.TrackingNumber)
	assert.Equal(t, "https://shippo/abc.pdf", result.LabelURL)
	assert.Equal(t, "USPS", result.CarrierName)
	assert.Equal(t, "Ground Advantage", result.ServiceName)
	assert.Equal(t, 758, result.RateCents)
	assert.Equal(t, "USD", result.Currency)
}

// TestShippoProvider_BuyRate_TransactionError surfaces a non-SUCCESS
// transaction status (the common "rate expired" failure mode) as an error so
// the caller can re-fetch and re-confirm.
func TestShippoProvider_BuyRate_TransactionError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object_id": "tx_1", "status": "ERROR", "messages": [{"text": "rate is no longer available"}]}`)) //nolint:errcheck
	}))
	defer srv.Close()

	p := shipping.NewShippoProviderWithBase("k", srv.URL, srv.Client())

	_, err := p.BuyRate(context.Background(), shipping.Rate{RateID: "rate_stale", AmountCents: 758, Currency: "USD"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "rate is no longer available")
}
