package domain

import (
	"net/url"
	"strings"
)

// TrackingURL returns the carrier's public tracking page for a tracking
// number, or an empty string if we can't recognize the carrier. Carrier
// names are normalized loosely (USPS, "USPS Ground Advantage", "U.S.P.S."
// all match the USPS branch). Shared by the shipment confirmation email and
// the admin order page so customers and staff land on the same carrier page.
func TrackingURL(carrier, trackingNumber string) string {
	t := strings.TrimSpace(trackingNumber)
	if t == "" {
		return ""
	}
	c := strings.ToLower(strings.TrimSpace(carrier))
	switch {
	case strings.Contains(c, "usps"):
		return "https://tools.usps.com/go/TrackConfirmAction?qtc_tLabels1=" + url.QueryEscape(t)
	case strings.Contains(c, "ups"):
		return "https://www.ups.com/track?tracknum=" + url.QueryEscape(t)
	case strings.Contains(c, "fedex"):
		return "https://www.fedex.com/fedextrack/?trknbr=" + url.QueryEscape(t)
	case strings.Contains(c, "dhl"):
		return "https://www.dhl.com/en/express/tracking.html?AWB=" + url.QueryEscape(t)
	default:
		return ""
	}
}
