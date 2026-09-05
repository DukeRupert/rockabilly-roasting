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

// Five rounds of review on this page went the same way: a correction landed,
// and the correction described the code in a sentence nothing enforced. So the
// comments here are kept to what the code does, and the checks read the
// rendered markup rather than a list of names kept alongside it.

// anchorTag matches a link's opening tag. Links are cut out before scanning:
// a URL a reader may click is not a host the browser fetches, and the policy
// prose links to intuit.com and google.com by name.
var anchorTag = regexp.MustCompile(`(?is)<a\b[^>]*>`)

// attrHost pulls hosts out of an attribute string, from any attribute name and
// from protocol-relative URLs as well as https:// ones.
var attrHost = regexp.MustCompile(`(?i)(?:https:)?//([a-z0-9][a-z0-9.-]*\.[a-z]{2,})`)

// disclosedAs is the register of disclosure decisions: for each external host
// any storefront page contacts, the name the privacy policy must call it by.
// It is not scoped to the page under test — challenges.cloudflare.com is loaded
// by the wholesale application form and js.stripe.com by the checkout bundle,
// and both belong on the record whether or not a test here can reach them.
var disclosedAs = map[string]string{
	"fonts.googleapis.com":        "Google Fonts",
	"fonts.gstatic.com":           "Google Fonts",
	"www.googletagmanager.com":    "Google Analytics",
	"cdn.jsdelivr.net":            "jsDelivr",
	"js.sentry-cdn.com":           "Sentry",
	"cdn.rockabillyroasting.shop": "Cloudflare",
	"challenges.cloudflare.com":   "Cloudflare",
	"js.stripe.com":               "Stripe",
}

// hostsThePrivacyPageLoads is what this page is known to pull in today, kept
// separate from disclosedAs so that recording a disclosure for a host another
// page loads cannot break this one. Its purpose is to fail when the extractor
// goes blind — a smaller set means tags stopped matching, not that a vendor
// left.
var hostsThePrivacyPageLoads = []string{
	"fonts.googleapis.com",
	"fonts.gstatic.com",
	"www.googletagmanager.com",
	"cdn.jsdelivr.net",
	"js.sentry-cdn.com",
	"cdn.rockabillyroasting.shop",
}

// namedNotFetched are hosts the markup mentions as data rather than as
// something to fetch. The JSON-LD block lists the shop's social profiles under
// sameAs and cites schema.org by URL; none of it causes a request, so none of
// it is a disclosure question. Everything else that appears outside a link is
// treated as a fetch.
//
// This is a judgement, and it is written down because scanning element bodies
// means judgements like it are unavoidable — the alternative is scanning only
// attributes, which is what let a reviewer put a tracker in an inline script
// and walk it past.
var namedNotFetched = map[string]bool{
	"www.facebook.com":  true, // JSON-LD sameAs
	"www.instagram.com": true, // JSON-LD sameAs
	"schema.org":        true, // JSON-LD vocabulary

	// Our own origin, in canonical URLs and JSON-LD. Reaching us is not a
	// third-party disclosure. Note this is the apex domain only:
	// cdn.rockabillyroasting.shop is our name in front of Cloudflare R2 and is
	// disclosed as Cloudflare, which is why it is in disclosedAs and not here.
	"rockabillyroasting.com": true,
}

// externalHosts returns every host named anywhere in the page except inside a
// link or in namedNotFetched. Attributes and element bodies alike, so a pixel
// appended by an inline <script> or pulled in by an @import inside <style> is
// caught — the layout already carries three inline script blocks, which makes
// that the likeliest way the next undisclosed host arrives.
func externalHosts(page string) map[string]bool {
	hosts := map[string]bool{}
	for _, m := range attrHost.FindAllStringSubmatch(anchorTag.ReplaceAllString(page, ""), -1) {
		host := strings.ToLower(m[1])
		if !namedNotFetched[host] {
			hosts[host] = true
		}
	}
	return hosts
}

func renderPrivacyPage(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, PrivacyPage(PrivacyProps{}).Render(context.Background(), &buf))
	return buf.String()
}

func TestEveryExternalHostTheLayoutLoadsIsDisclosed(t *testing.T) {
	page := renderPrivacyPage(t)
	hosts := externalHosts(page)

	for _, want := range hostsThePrivacyPageLoads {
		assert.True(t, hosts[want],
			"%s is loaded by this page but the extractor did not find it — it has gone blind to some tags, which is how a new host would slip past", want)
	}

	for host := range hosts {
		vendor, known := disclosedAs[host]
		if !assert.True(t, known, "the page loads from %s, which is not in disclosedAs — decide whether it belongs on the privacy policy, then record the decision here", host) {
			continue
		}
		assert.Contains(t, page, vendor,
			"the page loads from %s but never names %s in its policy", host, vendor)
	}
}

// vendorList returns the contents of the third-party <ul>, located by an entry
// only it contains. Cutting the list out is what makes the count below mean
// what the policy says it means: an earlier version matched across list items
// and section boundaries, so it counted runs of markup rather than entries, and
// its passing was arithmetic luck.
func vendorList(t *testing.T, html string) string {
	t.Helper()
	const anchor = "Stripe</strong>"
	at := strings.Index(html, anchor)
	require.NotEqual(t, -1, at, "cannot find the vendor list: no Stripe entry")
	open := strings.LastIndex(html[:at], "<ul")
	require.NotEqual(t, -1, open, "vendor list entry is not inside a <ul>")
	end := strings.Index(html[open:], "</ul>")
	require.NotEqual(t, -1, end, "vendor list <ul> is never closed")
	return html[open : open+end]
}

// The policy states how many of its vendors are Google. A stated count beats a
// vague phrase only if something checks it, and it has to count the same thing
// the sentence does — services named, not list items, so that merging two
// Google entries into one bullet cannot slip past.
func TestGoogleCountMatchesTheProse(t *testing.T) {
	html := renderPrivacy(t)

	const claimed = "Google appears three times"
	require.Contains(t, html, claimed, "the sentence this test pins has been reworded; update both together")

	got := strings.Count(vendorList(t, html), "Google")
	assert.Equal(t, 3, got,
		"the policy says %q but the vendor list names Google %d times", claimed, got)
}
