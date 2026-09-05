package storefront

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Six rounds of review on this page went the same way: a correction landed, and
// the correction described the code in a sentence nothing enforced. The comments
// here try to state only what is checked, and to name what is not.

// anchorHref matches the href of a link. Only the href is removed, not the
// link: a URL a reader may click is not a host the browser fetches, and the
// policy prose links to intuit.com and google.com by name — but everything
// between <a> and </a> is ordinary markup that can load whatever it likes.
//
// An earlier version deleted the whole element, which meant 16% of the page
// went unscanned and a tracking pixel inside a link was invisible. The header
// logo is an <img> inside an <a> today, so that was not a hypothetical shape.
//
// Double-quoted hrefs only, like fetchingAttr below. A single-quoted or
// unquoted link target is reported as a named host — noisy rather than blind,
// and templ always double-quotes.
var anchorHref = regexp.MustCompile(`(?is)(<a\b[^>]*?)\shref\s*=\s*"[^"]*"`)

// jsonLD matches a structured-data block, removed before scanning: it declares
// facts about the shop — social profiles, the schema.org vocabulary, our own
// URL — and causes no request.
//
// Removing the block rather than exempting its hosts by name is deliberate:
// exempting names globally made an Instagram embed or a Meta pixel invisible
// anywhere on the page. But the removal is itself a hole if it can be aimed,
// so the type attribute is matched exactly rather than as a substring — a
// tracker at .../application/ld+json/px.js used to delete its own tag — and
// what it removes must parse as JSON, so an unclosed block cannot swallow the
// script tag that follows it.
//
// A regex still cannot tell an attribute from an attribute's value, so a tag
// carrying data-x=' type="application/ld+json"' would match. Structured data
// never carries a fetching attribute and a tag worth hiding always does, so
// such tags are refused below rather than removed. Not reachable through templ, which
// escapes quotes and never single-quotes an attribute — closed because the
// same shape one layer up was reachable.
var jsonLD = regexp.MustCompile(`(?is)<script[^>]*\stype\s*=\s*"application/ld\+json"[^>]*>(.*?)</script>`)

// loadingAttr detects any fetching attribute on a tag claiming to be
// structured data. The same four fetchingAttr recognises, deliberately: a
// guard that covered src alone would let the identical trick through on
// srcset, poster or href, which is the mistake this file keeps making one
// name to the right.
var loadingAttr = regexp.MustCompile(`(?is)\s(?:src|srcset|href|poster)\s*=`)

// urlHost pulls hosts out of written URLs, from any attribute and from element
// bodies, accepting protocol-relative as well as https://.
//
// It finds a host only where one is written with a // prefix. A host assembled
// at runtime — `s.src = "https://" + domain` — is not found, and cannot be by
// this approach: hostnames and JavaScript property chains are the same shape,
// so scanning for bare dotted tokens matches el.style.opacity and
// toast.classList.add. Inline scripts are covered for written URLs only.
var urlHost = regexp.MustCompile(`(?i)(?:https:)?//([a-z0-9][a-z0-9.-]*\.[a-z]{2,})`)

// ourOrigin is the shop's own domain. Contacting us is not a third-party
// disclosure.
//
// Every occurrence on the page today is inside the structured-data block,
// which is removed before this applies, so the exemption is currently inert —
// it exists for the canonical URL and og:image the layout will emit once a
// page passes CanonicalURL, which the fixture below stands in for. The apex
// only:
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

// scannable strips the two things that are not fetches: link targets, and
// structured data. Everything else the page contains is scanned.
func scannable(page string) (string, error) {
	out := anchorHref.ReplaceAllString(page, "$1")
	for _, block := range jsonLD.FindAllStringSubmatch(out, -1) {
		opening := block[0][:strings.Index(block[0], ">")+1]
		if loadingAttr.MatchString(opening) {
			return "", errors.New("a script tagged application/ld+json also carries a fetching attribute, which structured data never does — refusing to remove it, since doing so would hide whatever it loads")
		}
		if !json.Valid([]byte(strings.TrimSpace(block[1]))) {
			return "", errors.New("a script tagged application/ld+json does not contain valid JSON, so removing it before scanning would delete an unknown amount of real markup. Either the block is unclosed and has swallowed the tags after it, or its JSON is wrapped in a CDATA or comment guard that this check does not unwrap")
		}
		out = strings.Replace(out, block[0], "", 1)
	}
	return out, nil
}

// hostsWritten returns every host written as a URL in the page, outside links
// and outside the structured-data block — attributes and element bodies alike.
func hostsWritten(t *testing.T, page string) map[string]bool {
	t.Helper()
	scanned, err := scannable(page)
	require.NoError(t, err)
	hosts := map[string]bool{}
	for _, m := range urlHost.FindAllStringSubmatch(scanned, -1) {
		if host := strings.ToLower(m[1]); host != ourOrigin {
			hosts[host] = true
		}
	}
	return hosts
}

// fetchingAttr matches double-quoted src/srcset/href/poster values — enough to
// recognise the loads this layout actually makes, not an exhaustive list of
// ways a browser can be told to fetch (object data, imagesrcset and a meta
// refresh are all absent). Restricted to these rather than to attributes in
// general so that a host named in a meta content= cannot satisfy the floor:
// otherwise the privacy page could disarm its own check by naming a vendor
// host in a sentence.
var fetchingAttr = regexp.MustCompile(`(?is)\b(?:src|srcset|href|poster)\s*=\s*"([^"]*)"`)

// hostsFetchedByMarkup is the hosts this page's markup actually loads from.
// The floor uses it, so a host merely mentioned cannot stand in for one the
// page stopped loading.
func hostsFetchedByMarkup(t *testing.T, page string) map[string]bool {
	t.Helper()
	scanned, err := scannable(page)
	require.NoError(t, err)
	hosts := map[string]bool{}
	for _, attr := range fetchingAttr.FindAllStringSubmatch(scanned, -1) {
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
	// The disclosure has to be made by the policy, not by the page. Checking
	// the whole document meant the layout's own "Sentry browser SDK (loader)"
	// comment satisfied the Sentry requirement: deleting the Sentry bullet
	// from the policy left this test green. Loader chrome names its vendor for
	// every vendor loaded that way, which is most of them.
	policy := renderPrivacy(t)

	fetched := hostsFetchedByMarkup(t, page)
	for _, want := range loadedByThePrivacyPage {
		assert.True(t, fetched[want],
			"%s is loaded by this page's markup but the extractor did not find it — it has gone blind to some tags, which is how a new host would slip past", want)
	}

	for host := range hostsWritten(t, page) {
		vendor, known := vendorFor(host)
		if !assert.True(t, known, "the page names %s, which is not in disclosedAs — decide whether it belongs on the privacy policy, then record the decision here", host) {
			continue
		}
		assert.Contains(t, policy, vendor,
			"the page loads from %s but the policy never names %s", host, vendor)
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
			"the extracted vendor list is missing %s — either that vendor was renamed or removed, or the list's markup changed shape and this helper is reading the wrong span. Either way the count below it is meaningless until someone looks", entry)
	}
	return list
}

// The policy states how many of its vendors are Google. A stated count beats a
// vague phrase only if something checks it.
//
// It counts the token in the vendor list, which is how many Google services are
// named there — the thing the sentence is about. Two of those names moving into
// one bullet does not change the count and does not make the sentence false.
func TestGoogleCountMatchesTheProse(t *testing.T) {
	html := renderPrivacy(t)

	const claimed = "Google appears three times"
	require.Contains(t, html, claimed, "the sentence this test pins has been reworded; update both together")

	got := strings.Count(vendorList(t, html), "Google")
	assert.Equal(t, 3, got,
		"the policy says %q but the vendor list names Google %d times", claimed, got)
}

// The reach of BOTH extractors is pinned here, against fixtures rather than
// against the live page, because the live page exercises almost none of it: a
// reviewer reverted hostsWritten to attributes-only — the regression the body
// scan exists to prevent — and the suite stayed green, then did it again to
// fetchingAttr's srcset and poster, which no rendered page contains.
//
// So every case states what each extractor should see. hostsWritten is the
// disclosure check and reads the whole page; hostsFetchedByMarkup is the floor
// and reads only attributes that fetch, which is why the two columns differ for
// anything written in an element body.
func TestExtractorReach(t *testing.T) {
	cases := []struct {
		name             string
		html             string
		host             string
		written, fetched bool
	}{
		{"script src", `<script src="https://a.example.com/t.js"></script>`, "a.example.com", true, true},
		{"link href", `<link rel="stylesheet" href="https://b.example.com/s.css"/>`, "b.example.com", true, true},
		{"source srcset", `<picture><source srcset="https://c.example.com/p.png"/></picture>`, "c.example.com", true, true},
		{"video poster", `<video poster="https://d.example.com/f.jpg"></video>`, "d.example.com", true, true},
		{"protocol-relative", `<script src="//e.example.com/t.js"></script>`, "e.example.com", true, true},
		{"subresource inside a link", `<a href="/x"><img src="https://f.example.com/px.gif"/></a>`, "f.example.com", true, true},
		{"ld+json lookalike in a src", `<script src="https://g.example.com/application/ld+json/px.js"></script>`, "g.example.com", true, true},

		// Written into an element body: the disclosure check sees these, the
		// floor does not, because neither is a resource load the markup makes.
		{"inline style @import", `<style>@import url(https://h.example.com/x.css);</style>`, "h.example.com", true, false},
		{"inline script url literal", `<script>var s="https://i.example.com/t.js";</script>`, "i.example.com", true, false},
		{"prose mention in a meta", `<meta name="n" content="see //j.example.com"/>`, "j.example.com", true, false},

		// Neither: a link target is not a fetch, and structured data declares
		// rather than requests.
		{"link target itself", `<a href="https://k.example.com/page">text</a>`, "k.example.com", false, false},
		{"structured data", `<script type="application/ld+json">{"sameAs":["https://l.example.com/us"]}</script>`, "l.example.com", false, false},
		// Written out rather than built from ourOrigin: a fixture that takes
		// both its markup and its expectation from the constant agrees with
		// itself whatever the constant says, and passes with it pointed at a
		// domain we do not own.
		{"our own origin", `<link rel="canonical" href="https://rockabillyroasting.com/privacy"/>`, "rockabillyroasting.com", false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.written, hostsWritten(t, tc.html)[tc.host], "hostsWritten")
			assert.Equal(t, tc.fetched, hostsFetchedByMarkup(t, tc.html)[tc.host], "hostsFetchedByMarkup")
		})
	}
}

// An unclosed structured-data block must not silently delete the markup after
// it. Reported as malformed JSON rather than passing quietly.
func TestUnclosedStructuredDataIsReported(t *testing.T) {
	html := `<script type="application/ld+json">{"a":1}` +
		`<script src="https://tracker.example.com/px.js"></script>`

	_, err := scannable(html)
	assert.Error(t, err,
		"an unclosed ld+json block swallowed the script tag after it and nothing complained")
}

// A tag claiming to be structured data while also fetching something is
// refused, not removed — otherwise the removal hides whatever it loads. Pinned
// per attribute because the first version of this guard checked src alone, and
// the same trick worked unchanged on the other three.
func TestStructuredDataCannotCarryALoad(t *testing.T) {
	for _, attr := range []string{"src", "srcset", "href", "poster"} {
		t.Run(attr, func(t *testing.T) {
			html := `<script ` + attr + `="https://tracker.example.com/px.js" data-x=' type="application/ld+json"'>0</script>`
			_, err := scannable(html)
			assert.Error(t, err,
				"a tag claiming ld+json while loading via %s was removed, taking its host with it", attr)
		})
	}
}
