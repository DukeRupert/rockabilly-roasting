package storefront

import (
	"bytes"
	"context"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Three rounds of review on this page went the same way: a correction landed,
// and the correction itself asserted something about the code that nothing
// enforced. The vendor checklist next door does not break that cycle, because
// it is a list of names checked against a list of names.
//
// This does. It renders the real storefront page, pulls out every host the
// browser is told to fetch from, and requires each one to be disclosed on the
// privacy page. Add a font, a CDN, a tag manager or an error reporter to the
// layout and this fails until the policy names it — no reviewer needed, and no
// judgement about whether it "counts" as a third party.
//
// Its reach is the layout, which is the half that was actually going wrong
// (Google Fonts and jsDelivr both slipped through). Go-side vendors — Stripe,
// Postmark, Shippo, Intuit — are constructed in run() and cannot be seen from
// here; those stay on the checklist next door, honestly labelled as such.
var externalResource = regexp.MustCompile(`<(?:script|link|img|iframe)[^>]*?(?:src|href)="https://([^/"]+)`)

// ownHosts are ours: the page fetching from them tells a visitor nothing about
// a third party, so they need no disclosure.
var ownHosts = map[string]bool{
	"cdn.rockabillyroasting.shop": true,
	"rockabillyroasting.com":      true,
}

// disclosedAs maps a host the browser contacts to the name the privacy page
// must call it by. A host missing from this map fails the test too — that is
// the point: a new external host is a decision, and the decision is whether to
// disclose it, not whether to add it to a map.
var disclosedAs = map[string]string{
	"fonts.googleapis.com":      "Google Fonts",
	"fonts.gstatic.com":         "Google Fonts",
	"www.googletagmanager.com":  "Google Analytics",
	"cdn.jsdelivr.net":          "jsDelivr",
	"js.sentry-cdn.com":         "Sentry",
	"challenges.cloudflare.com": "Cloudflare",
	"js.stripe.com":             "Stripe",
}

func TestEveryExternalHostTheLayoutLoadsIsDisclosed(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, PrivacyPage(PrivacyProps{}).Render(context.Background(), &buf))
	page := buf.String()

	matches := externalResource.FindAllStringSubmatch(page, -1)
	require.NotEmpty(t, matches, "found no external resources at all — the regex has stopped matching, not the page stopped loading")

	seen := map[string]bool{}
	for _, m := range matches {
		host := m[1]
		if ownHosts[host] || seen[host] {
			continue
		}
		seen[host] = true

		vendor, known := disclosedAs[host]
		if !assert.True(t, known, "the page loads from %s, which is not in disclosedAs — decide whether it belongs on the privacy policy, then record the decision here", host) {
			continue
		}
		assert.Contains(t, page, vendor,
			"the page loads from %s but never names %s in its policy", host, vendor)
	}
}

// The policy says Google appears three times in the vendor list. A sentence
// that counts is worth more than a vague one because it can be checked — but
// only if something checks it, and the commit that wrote it did not. This is
// that check.
func TestGoogleCountMatchesTheProse(t *testing.T) {
	html := renderPrivacy(t)

	const claimed = 3
	got := 0
	for _, vendor := range []string{"Google Analytics", "Google Geocoding", "Google Fonts"} {
		if assert.Contains(t, html, vendor) {
			got++
		}
	}
	require.Equal(t, claimed, got)
	assert.Contains(t, html, "Google appears three times",
		"the prose and the list have to agree on the count; change both or drop the sentence")
}
