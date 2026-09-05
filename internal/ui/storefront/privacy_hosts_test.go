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

// Six rounds of review on this page went the same way: a correction landed, and
// the correction described the code in a sentence nothing enforced. The comments
// here try to state only what is checked, and to name what is not.

// anchorElement matches a link and its text. Links are removed before scanning:
// a URL a reader may click is not a host the browser fetches, and the policy
// prose links to intuit.com and google.com by name.
var anchorElement = regexp.MustCompile(`(?is)<a\b[^>]*>.*?</a>`)

// jsonLD matches the structured-data block, removed before scanning: it
// declares facts about the shop — social profiles, the schema.org vocabulary,
// our own URL — and causes no request.
//
// Removing the block rather than exempting its hosts by name is deliberate.
// Exempting the names globally made an Instagram embed or a Meta pixel
// invisible anywhere on the page, which a reviewer demonstrated with four
// separate tags.
var jsonLD = regexp.MustCompile(`(?is)<script[^>]*application/ld\+json[^>]*>.*?</script>`)

// urlHost pulls hosts out of written URLs, from any attribute and from element
// bodies, accepting protocol-relative as well as https://.
//
// It finds a host only where one is written with a // prefix. A host assembled
// at runtime — `s.src = "https://" + domain` — is not found, and cannot be by
// this approach: hostnames and JavaScript property chains are the same shape,
// so scanning for bare dotted tokens matches el.style.opacity and
// toast.classList.add. Inline scripts are covered for written URLs only.
var urlHost = regexp.MustCompile(`(?i)(?:https:)?//([a-z0-9][a-z0-9.-]*\.[a-z]{2,})`)

// ourOrigin is the shop's own domain, in canonical URLs and meta tags.
// Contacting us is not a third-party disclosure. The apex only:
// cdn.rockabillyroasting.shop is our name in front of Cloudflare R2 and is
// disclosed as Cloudflare.
const ourOrigin = "rockabillyroasting.com"

// disclosedAs is the register of disclosure decisions: for each external host a
// storefront page contacts, the name the privacy policy must call it by.
//
// Sourced from docs/security/csp-audit.md, which enumerates the origins the
// browser actually fetches. Several — the GA pixel host, Stripe's API and
// Elements frames — are reached by scripts rather than written into markup, so
// no test here can discover them; when that audit gains an origin, this map
// needs the same entry by hand.
var disclosedAs = map[string]string{
	"fonts.googleapis.com":        "Google Fonts",
	"fonts.gstatic.com":           "Google Fonts",
	"www.googletagmanager.com":    "Google Analytics",
	"www.google-analytics.com":    "Google Analytics",
	"cdn.jsdelivr.net":            "jsDelivr",
	"js.sentry-cdn.com":           "Sentry",
	"cdn.rockabillyroasting.shop": "Cloudflare",
	"challenges.cloudflare.com":   "Cloudflare",
	"js.stripe.com":               "Stripe",
	"api.stripe.com":              "Stripe",
	"hooks.stripe.com":            "Stripe",
}

// sentryIngest covers the *.sentry.io ingest origins, which are per-project and
// cannot be enumerated.
const sentryIngest = ".sentry.io"

func vendorFor(host string) (string, bool) {
	if strings.HasSuffix(host, sentryIngest) {
		return "Sentry", true
	}
	vendor, ok := disclosedAs[host]
	return vendor, ok
}

func scannable(page string) string {
	return jsonLD.ReplaceAllString(anchorElement.ReplaceAllString(page, ""), "")
}

// hostsWritten returns every host written as a URL in the page, outside links
// and outside the structured-data block — attributes and element bodies alike.
func hostsWritten(page string) map[string]bool {
	hosts := map[string]bool{}
	for _, m := range urlHost.FindAllStringSubmatch(scannable(page), -1) {
		if host := strings.ToLower(m[1]); host != ourOrigin {
			hosts[host] = true
		}
	}
	return hosts
}

// fetchingAttr matches the attributes that cause the browser to go and get
// something. Restricted to these rather than to attributes in general: a host
// in a meta content= is prose, and letting prose satisfy the floor would let
// the privacy page disarm its own check by naming a vendor host in a sentence.
var fetchingAttr = regexp.MustCompile(`(?is)\b(?:src|srcset|href|poster)\s*=\s*"([^"]*)"`)

// hostsFetchedByMarkup is the hosts this page's markup actually loads from.
// The floor uses it, so a host merely mentioned cannot stand in for one the
// page stopped loading.
func hostsFetchedByMarkup(page string) map[string]bool {
	hosts := map[string]bool{}
	for _, attr := range fetchingAttr.FindAllStringSubmatch(scannable(page), -1) {
		for _, m := range urlHost.FindAllStringSubmatch(attr[1], -1) {
			if host := strings.ToLower(m[1]); host != ourOrigin {
				hosts[host] = true
			}
		}
	}
	return hosts
}

// loadedByThePrivacyPage is what this page's markup pulls in today. Its purpose
// is to fail when the extractor goes blind: a host disappearing from this set
// means tags stopped matching, not that a vendor left.
var loadedByThePrivacyPage = []string{
	"fonts.googleapis.com",
	"fonts.gstatic.com",
	"www.googletagmanager.com",
	"cdn.jsdelivr.net",
	"js.sentry-cdn.com",
	"cdn.rockabillyroasting.shop",
}

func renderPrivacyPage(t *testing.T) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, PrivacyPage(PrivacyProps{}).Render(context.Background(), &buf))
	return buf.String()
}

func TestEveryExternalHostTheLayoutLoadsIsDisclosed(t *testing.T) {
	page := renderPrivacyPage(t)

	fetched := hostsFetchedByMarkup(page)
	for _, want := range loadedByThePrivacyPage {
		assert.True(t, fetched[want],
			"%s is loaded by this page's markup but the extractor did not find it — it has gone blind to some tags, which is how a new host would slip past", want)
	}

	for host := range hostsWritten(page) {
		vendor, known := vendorFor(host)
		if !assert.True(t, known, "the page names %s, which is not in disclosedAs — decide whether it belongs on the privacy policy, then record the decision here", host) {
			continue
		}
		assert.Contains(t, page, vendor,
			"the page names %s but never names %s in its policy", host, vendor)
	}
}

// vendorList returns the contents of the third-party <ul>. It checks the list it
// found still holds the entries it should, so a structural change — a nested
// list, the list split in two — is reported as that rather than as a wrong
// Google count, which is how an earlier version misdiagnosed itself.
func vendorList(t *testing.T, html string) string {
	t.Helper()
	at := strings.Index(html, "Stripe</strong>")
	require.NotEqual(t, -1, at, "cannot find the vendor list: no Stripe entry")
	open := strings.LastIndex(html[:at], "<ul")
	require.NotEqual(t, -1, open, "vendor list entry is not inside a <ul>")
	end := strings.Index(html[open:], "</ul>")
	require.NotEqual(t, -1, end, "vendor list <ul> is never closed")
	list := html[open : open+end]

	for _, entry := range []string{"Stripe", "Shippo", "Postmark", "Intuit QuickBooks", "Broadwave", "Sentry"} {
		require.Contains(t, list, entry,
			"the extracted vendor list is missing %s — the list's markup has changed shape, so this helper is reading the wrong span and any count below it is meaningless", entry)
	}
	return list
}

// The policy states how many of its vendors are Google. A stated count beats a
// vague phrase only if something checks it, and it has to count what the
// sentence counts — services named, not list items, so merging two Google
// entries into one bullet cannot slip past.
func TestGoogleCountMatchesTheProse(t *testing.T) {
	html := renderPrivacy(t)

	const claimed = "Google appears three times"
	require.Contains(t, html, claimed, "the sentence this test pins has been reworded; update both together")

	got := strings.Count(vendorList(t, html), "Google")
	assert.Equal(t, 3, got,
		"the policy says %q but the vendor list names Google %d times", claimed, got)
}
