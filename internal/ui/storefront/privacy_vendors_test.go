package storefront

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The privacy page names the third parties customer data reaches. That list is
// a compliance statement rather than decoration — Intuit's app review reads it,
// and the last time it drifted it named a shipping vendor the shop had stopped
// using and omitted the one it had moved to, for months, with nothing failing.
//
// So this test does not check that the page renders. It checks that each vendor
// the code actually calls is named on it, and it is meant to be edited in the
// same commit as any change to who those vendors are. A provider swapped in
// cmd/server/main.go with this list left alone should fail here.
func renderPrivacy(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, PrivacyContent(PrivacyProps{}).Render(context.Background(), &buf))
	return buf.String()
}

func TestPrivacyNamesEveryVendorWeSendDataTo(t *testing.T) {
	html := renderPrivacy(t)

	// Each entry pairs the vendor with the code that hands it customer data,
	// so a failure says where to go and look rather than only what is missing.
	vendors := map[string]string{
		"Stripe":            "platform/payments/stripe.go — card processing at checkout",
		"Shippo":            "platform/shipping/shippo.go — labels and tracking",
		"Postmark":          "platform/email/postmark.go — order and account mail",
		"Intuit QuickBooks": "platform/quickbooks — wholesale customers and invoices",
		"Broadwave":         "platform/newsletter — footer signups",
		"Google Analytics":  "layouts/storefront.templ — the GA4 tag",
		"Google Geocoding":  "platform/geocode/google.go — local-delivery addresses",
		"Cloudflare":        "media delivery, and platform/turnstile on the wholesale form",
		"Sentry":            "platform/sentry — error reports",
	}
	for vendor, where := range vendors {
		assert.Contains(t, html, vendor,
			"privacy page does not name %s (%s) — if the shop still uses it, say so on the page", vendor, where)
	}
}

func TestPrivacyDoesNotNameRetiredVendors(t *testing.T) {
	html := renderPrivacy(t)

	// EasyPost stayed on the page long after Shippo replaced it. Naming a
	// vendor that no longer touches customer data is its own kind of wrong:
	// it tells a reader their address went somewhere it did not.
	assert.NotContains(t, html, "EasyPost",
		"Shippo replaced EasyPost as the label provider; the privacy page should not still name it")
}

// The QuickBooks section enumerates what wholesale billing sends Intuit, and an
// enumeration is only worth having if it is complete. qbCustomerRequest carries
// GivenName, FamilyName and PrimaryPhone alongside the company name — an earlier
// draft of this page listed the company name and email, called that the whole of
// it, and was wrong.
func TestPrivacyQuickBooksSectionCoversThePersonalFields(t *testing.T) {
	html := renderPrivacy(t)

	for _, claim := range []string{
		"phone",         // PrimaryPhone.FreeFormNumber
		"business name", // DisplayName
		"check number",  // PaymentRefNum on CreatePayment
	} {
		assert.Contains(t, html, claim,
			"the QuickBooks disclosure should account for %q — see platform/quickbooks/customers.go and payments.go", claim)
	}
}
