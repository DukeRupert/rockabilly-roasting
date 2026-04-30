package app

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrackingURL(t *testing.T) {
	tests := []struct {
		name           string
		carrier        string
		trackingNumber string
		wantHostFrag   string // empty means: result must be empty
	}{
		{"usps direct", "USPS", "9400111202555842761523", "usps.com"},
		{"usps service variant", "USPS Ground Advantage", "9400111202555842761523", "usps.com"},
		{"ups", "UPS", "1Z999AA10123456784", "ups.com"},
		{"fedex", "FedEx", "123456789012", "fedex.com"},
		{"dhl", "DHL Express", "1234567890", "dhl.com"},
		{"unknown carrier", "OnTrac", "123", ""},
		{"missing tracking", "USPS", "  ", ""},
		{"missing carrier", "", "9400111202555842761523", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := trackingURL(tt.carrier, tt.trackingNumber)
			if tt.wantHostFrag == "" {
				assert.Equal(t, "", got)
				return
			}
			assert.Truef(t, strings.Contains(got, tt.wantHostFrag),
				"want url containing %q, got %q", tt.wantHostFrag, got)
			assert.Truef(t, strings.Contains(got, strings.TrimSpace(tt.trackingNumber)),
				"want url containing tracking number %q, got %q", tt.trackingNumber, got)
		})
	}
}
