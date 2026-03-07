package shipping

import (
	"context"
	"fmt"
	"math"
	"strconv"

	easypost "github.com/EasyPost/easypost-go/v5"
)

// EasyPostProvider implements LabelProvider using the EasyPost API.
type EasyPostProvider struct {
	client *easypost.Client
}

// NewEasyPostProvider creates a LabelProvider backed by EasyPost.
func NewEasyPostProvider(apiKey string) *EasyPostProvider {
	return &EasyPostProvider{client: easypost.New(apiKey)}
}

// CreateLabel creates a shipment in EasyPost, selects the rate matching the
// requested ServiceCode (carrier + service), buys the label, and returns the
// result. If ServiceCode is empty, the lowest available rate is used.
func (p *EasyPostProvider) CreateLabel(ctx context.Context, req LabelRequest) (*LabelResult, error) {
	shipment, err := p.client.CreateShipmentWithContext(ctx, &easypost.Shipment{
		FromAddress: &easypost.Address{
			Name:    req.FromName,
			Street1: req.FromStreet1,
			City:    req.FromCity,
			State:   req.FromState,
			Zip:     req.FromZip,
			Country: req.FromCountry,
		},
		ToAddress: &easypost.Address{
			Name:    req.ToName,
			Street1: req.ToStreet1,
			City:    req.ToCity,
			State:   req.ToState,
			Zip:     req.ToZip,
			Country: req.ToCountry,
		},
		Parcel: &easypost.Parcel{
			Weight: req.WeightOz,
			Length: req.LengthIn,
			Width:  req.WidthIn,
			Height: req.HeightIn,
		},
		Reference: req.Reference,
	})
	if err != nil {
		return nil, fmt.Errorf("easypost create shipment: %w", err)
	}

	rate, err := p.selectRate(shipment, req.ServiceCode)
	if err != nil {
		return nil, err
	}

	bought, err := p.client.BuyShipmentWithContext(ctx, shipment.ID, rate, "")
	if err != nil {
		return nil, fmt.Errorf("easypost buy shipment: %w", err)
	}

	labelURL := ""
	if bought.PostageLabel != nil {
		labelURL = bought.PostageLabel.LabelURL
	}

	rateCents, err := dollarsToCents(rate.Rate)
	if err != nil {
		return nil, fmt.Errorf("parse rate %q: %w", rate.Rate, err)
	}

	return &LabelResult{
		TrackingNumber: bought.TrackingCode,
		LabelURL:       labelURL,
		CarrierName:    rate.Carrier,
		ServiceName:    rate.Service,
		RateCents:      rateCents,
		Currency:       rate.Currency,
	}, nil
}

// SupportedServices returns a static list of common EasyPost service codes.
// The actual available services depend on the carrier accounts configured
// in the EasyPost dashboard.
func (p *EasyPostProvider) SupportedServices(_ context.Context) ([]string, error) {
	return []string{
		"First",
		"Priority",
		"Express",
		"ParcelSelect",
		"GroundAdvantage",
	}, nil
}

// selectRate picks a rate matching the requested service code, or falls back
// to the lowest rate if no service code is specified.
func (p *EasyPostProvider) selectRate(shipment *easypost.Shipment, serviceCode string) (*easypost.Rate, error) {
	if serviceCode == "" {
		rate, err := p.client.LowestShipmentRate(shipment)
		if err != nil {
			return nil, fmt.Errorf("easypost lowest rate: %w", err)
		}
		return &rate, nil
	}

	for _, r := range shipment.Rates {
		if r.Service == serviceCode {
			return r, nil
		}
	}

	return nil, fmt.Errorf("easypost: no rate found for service %q", serviceCode)
}

// dollarsToCents converts a dollar string (e.g. "7.58") to cents (758).
func dollarsToCents(dollars string) (int, error) {
	f, err := strconv.ParseFloat(dollars, 64)
	if err != nil {
		return 0, err
	}
	return int(math.Round(f * 100)), nil
}
