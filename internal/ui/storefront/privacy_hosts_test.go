package storefront

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Four rounds of review on this page went the same way: a correction landed,
// and the correction stated something about the code that nothing enforced.
// This file is the attempt to stop asserting and start measuring — it renders
// the real page and reads what is actually in the markup.
//
// It covers what the storefront layout loads. Vendors reached from the server —
// Shippo, Stripe, Postmark, Intuit, Broadwave, Google Geocoding — are built in
// run() from environment variables and are invisible here; those are pinned
// only by the checklist in privacy_vendors_test.go, which says so.

// resourceTag matches any element except an anchor. Anchors are excluded
// because a link in prose is something the reader may click, not something the
// browser fetches — quickbooks.intuit.com is named in the policy text and is
// not a resource load.
var resourceTag = regexp.MustCompile(`(?is)<([a-z][a-z0-9]*)\b([^>]*)>`)

// attrHost pulls a host out of any attribute value in a tag: src, href, srcset,
// poster, content, data-whatever. Deliberately attribute-agnostic, and
// deliberately accepting protocol-relative "//host" as well as "https://host",
// because the previous version of this test looked only for src/href with an
// explicit https:// and a reviewer walked a tracker past it twice — once in a
// <source srcset>, once protocol-relative.
var attrHost = regexp.MustCompile(`(?i)(?:https:)?//([a-z0-9][a-z0-9.-]*\.[a-z]{2,})`)

// disclosedAs maps a host the browser contacts to the name the privacy page
// must call it by.
//
// cdn.rockabillyroasting.shop is ours by domain name only: it fronts Cloudflare
// R2 through Image Transformations, so a visitor loading from it is contacting
// Cloudflare, and the page says so. Treating it as "ours" is what let a
// reviewer rename Cloudflare to Fastly on the policy with this test still green.
//
// A host absent from this map fails too. That is the point — a new external
// host is a decision about disclosure, and the failure is what forces someone
// to make it.
var disclosedAs = map[string]string{
	"fonts.googleapis.com":        "Google Fonts",
	"fonts.gstatic.com":           "Google Fonts",
	"www.googletagmanager.com":    "Google Analytics",
	"cdn.jsdelivr.net":            "jsDelivr",
	"js.sentry-cdn.com":           "Sentry",
	"cdn.rockabillyroasting.shop": "Cloudflare",
}

// hostsLoadedBy returns every external host the rendered markup tells the
// browser to fetch from.
func hostsLoadedBy(t *testing.T, page string) map[string]bool {
	t.Helper()
	hosts := map[string]bool{}
	for _, tag := range resourceTag.FindAllStringSubmatch(page, -1) {
		if strings.EqualFold(tag[1], "a") {
			continue
		}
		for _, m := range attrHost.FindAllStringSubmatch(tag[2], -1) {
			hosts[strings.ToLower(m[1])] = true
		}
	}
	return hosts
}

func TestEveryExternalHostTheLayoutLoadsIsDisclosed(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, PrivacyPage(PrivacyProps{}).Render(context.Background(), &buf))
	page := buf.String()

	hosts := hostsLoadedBy(t, page)

	// A floor, not a smoke check. If the regex stops matching some tags the
	// count drops and this fails, where an "is it empty" guard would have
	// passed — partial blindness is the failure mode that matters for a test
	// whose job is to notice something new.
	assert.GreaterOrEqual(t, len(hosts), len(disclosedAs),
		"found only %d external hosts (%v) but %d are known to be loaded — the extractor has gone blind to some tags",
		len(hosts), hosts, len(disclosedAs))

	for host := range hosts {
		vendor, known := disclosedAs[host]
		if !assert.True(t, known, "the page loads from %s, which is not in disclosedAs — decide whether it belongs on the privacy policy, then record the decision here", host) {
			continue
		}
		assert.Contains(t, page, vendor,
			"the page loads from %s but never names %s in its policy", host, vendor)
	}
}

// googleEntry finds the vendor list items naming Google. The policy states a
// count, and a stated count is only worth more than a vague phrase if something
// checks it — the previous version of this test counted a hardcoded list of
// three names, so it could detect a Google being removed and never one being
// added, which is the direction that makes the page false.
var googleEntry = regexp.MustCompile(`(?i)<li>.*?Google.*?</li>`)

func TestGoogleCountMatchesTheProse(t *testing.T) {
	html := renderPrivacy(t)

	found := googleEntry.FindAllString(html, -1)
	assert.Len(t, found, 3,
		"the policy says Google appears three times in the vendor list; the list has %d entries naming Google (%v). Change the sentence and this number together, or drop the sentence.",
		len(found), found)
	assert.Contains(t, html, "Google appears three times")
}
