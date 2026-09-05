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
// So this test does not check that the page renders. It is a checklist of the
// vendors the shop was known to use when it was written, pinned so that editing
// the page means deliberately editing the list too.
//
// Be clear about what it cannot do: the map below is a literal, so it binds the
// page to this file rather than to the wiring. Swap the label provider in
// cmd/server/main.go and these tests stay green — a reviewer proved exactly that
// by doing it.
//
// The limit applies to every vendor reached from the server: Shippo, Stripe,
// Postmark, Intuit, Broadwave and Google Geocoding are all constructed in run()
// from environment variables a test cannot see, and this checklist is the only
// thing pinning them.
//
// Hosts the rendered page tells the browser to fetch from are enforced for
// real in privacy_hosts_test.go. Which vendors those are is written in that
// file's maps; three attempts to restate the split here were wrong in three
// different ways, so this one points instead of summarising.
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
	for vendor, where := range vendorsOnThePage {
		assert.Contains(t, html, vendor,
			"privacy page does not name %s (%s) — if the shop still uses it, say so on the page", vendor, where)
	}
}

// vendorsOnThePage is shared with privacy_hosts_test.go's vendor-list shape
// guard, so emptying it fails there rather than quietly checking nothing.
var vendorsOnThePage = map[string]string{
	"Stripe":             "platform/payments/stripe.go — card processing at checkout",
	"Shippo":             "platform/shipping/shippo.go — labels and tracking",
	"Postmark":           "platform/email/postmark.go — order and account mail",
	"Intuit QuickBooks":  "platform/quickbooks — wholesale customers and invoices",
	"Broadwave":          "platform/newsletter — footer signups",
	"Google Analytics 4": "layouts/storefront.templ — the GA4 tag",
	"Google Geocoding":   "platform/geocode/google.go — local-delivery addresses",
	"Google Fonts":       "layouts/storefront.templ — the fonts.googleapis.com stylesheet",
	"Cloudflare":         "media delivery, and platform/turnstile on the wholesale form",
	"jsDelivr":           "layouts/storefront.templ — the Alpine.js script tag",
	"Sentry":             "platform/sentry — error reports",
}

func TestPrivacyDoesNotNameRetiredVendors(t *testing.T) {
	html := renderPrivacy(t)

	// EasyPost stayed on the page long after Shippo replaced it, telling readers
	// their address went somewhere it did not.
	//
	// Precisely: cmd/server/main.go still falls back to EasyPost when
	// SHIPPO_API_KEY is unset, so this asserts a fact about production config
	// rather than about the code — every deployed environment sets that key. An
	// environment that did not would make the page wrong with nothing failing
	// here, which is an argument for the fallback going away, not for hedging
	// the policy.
	assert.NotContains(t, html, "EasyPost",
		"Shippo is the label provider everywhere this deploys; the privacy page should not still name EasyPost")
}

// The QuickBooks section enumerates what wholesale billing sends Intuit, and an
// enumeration is only worth having if it is complete. qbCustomerRequest carries
// GivenName, FamilyName and PrimaryPhone alongside the company name — an earlier
// draft of this page listed the company name and email, called that the whole of
// it, and was wrong.
func TestPrivacyQuickBooksSectionCoversThePersonalFields(t *testing.T) {
	html := renderPrivacy(t)

	for _, claim := range []string{
		"phone number",  // PrimaryPhone.FreeFormNumber; the full phrase, since "phone" alone is a substring of other words
		"business name", // DisplayName
		"check number",  // PaymentRefNum on CreatePayment
	} {
		assert.Contains(t, html, claim,
			"the QuickBooks disclosure should account for %q — see platform/quickbooks/customers.go and payments.go", claim)
	}
}
