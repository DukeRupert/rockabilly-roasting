package storefront

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
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
// no test here can discover them. TestDisclosedHostsMatchTheCSPAudit binds the
// keys to that file, so an origin added there fails here.
//
// The values are weaker. Which company operates a host is a fact about the
// world, so nothing here can prove an attribution right — but most hosts carry
// their operator in the domain, and TestDisclosedVendorsMatchTheirDomains uses
// that to catch the attributions that are plainly wrong. The two that do not
// carry it are exempted by name there.
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
//
// It is compared for set equality with what the extractor actually finds, not
// used as a floor.
//
// It is one of several lists here that decide how much of something else gets
// checked, and every one of them has at some point been shrinkable for free —
// each found a round apart, and each time the enumeration of "the others" was
// wrong. So the rule, rather than the roll-call: a list that gates a loop must
// be bound to a source outside itself. The exception is a table of cases that
// are themselves the assertions, like TestExtractorReach, where shrinking it
// deletes tests rather than weakening the ones that remain.
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
	found := make([]string, 0, len(fetched))
	for host := range fetched {
		found = append(found, host)
	}
	// Set equality, not a floor. A floor closed the empty case and left the
	// shrunk one: cutting this list to a single entry checked one sixth of what
	// its comment promises, with everything green.
	assert.ElementsMatch(t, loadedByThePrivacyPage, found,
		"what this page's markup loads no longer matches the recorded set — either the extractor has gone blind to some tags, or the page gained or lost a host and this list needs the same edit")

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

	require.NotEmpty(t, vendorsOnThePage, "the vendor checklist is empty, so the shape guard below checks nothing")
	for entry := range vendorsOnThePage {
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
	// Anchored to fetchingAttr, not to loadingAttr. Writing the four out here
	// was shrinkable; deriving them from loadingAttr was worse, since the list
	// then mirrored the thing under test — swapping in three attributes that do
	// not exist kept the arity and passed, leaving srcset, href and poster
	// unguarded with nothing to say so.
	//
	// fetchingAttr is the one of the pair with an outside oracle:
	// TestExtractorReach exercises script src, link href, source srcset and
	// video poster individually. The two must cover the same attributes — a tag
	// that can fetch is a tag whose removal can hide a fetch — so equality
	// borrows that anchor.
	loading, fetching := alternation(t, loadingAttr), alternation(t, fetchingAttr)
	require.Equal(t, fetching, loading,
		"loadingAttr and fetchingAttr disagree about which attributes fetch: the ld+json guard refuses %v while the floor counts %v, so whichever is narrower is a hole", loading, fetching)

	for _, attr := range loading {
		t.Run(attr, func(t *testing.T) {
			html := `<script ` + attr + `="https://tracker.example.com/px.js" data-x=' type="application/ld+json"'>0</script>`
			_, err := scannable(html)
			assert.Error(t, err,
				"a tag claiming ld+json while loading via %s was removed, taking its host with it", attr)
		})
	}
}

// vendorFor's suffix branch is exercised from both sides. Only the negative
// case catches the worst failure: strings.HasSuffix(host, "") is true for every
// host, so an emptied sentryIngest makes vendorFor claim every host is
// disclosed and the undisclosed-host gate — the property this file exists for —
// silently stops firing. A reviewer shipped a live tracker past it that way.
func TestVendorForSuffixBranch(t *testing.T) {
	vendor, known := vendorFor("o123456.ingest.sentry.io")
	assert.True(t, known, "the Sentry ingest suffix is no longer recognised")
	assert.Equal(t, "Sentry", vendor)

	_, known = vendorFor("tracker.evilcorp.example")
	assert.False(t, known,
		"an unknown host was reported as disclosed — if sentryIngest is empty, every host matches the suffix and nothing is ever undisclosed")
}

// cspOrigin pulls the backticked origins out of the CSP audit's allowlist table.
var cspOrigin = regexp.MustCompile("`([a-z0-9*][a-z0-9.*-]*\\.[a-z]{2,})`")

// The register is bound to docs/security/csp-audit.md — a file this branch does
// not own — so a host added there and not here fails, rather than waiting for
// someone to notice. Round 7 found four missing entries by reading that table
// by hand; this is that check, run.
func TestDisclosedHostsMatchTheCSPAudit(t *testing.T) {
	doc, err := os.ReadFile("../../../docs/security/csp-audit.md")
	require.NoError(t, err)

	table := string(doc)
	start := strings.Index(table, "## External origins")
	require.NotEqual(t, -1, start, "the CSP audit no longer has an External origins section")
	end := strings.Index(table[start:], "\n---")
	require.NotEqual(t, -1, end, "cannot find the end of the origins section")

	listed := map[string]bool{}
	for _, m := range cspOrigin.FindAllStringSubmatch(table[start:start+end], -1) {
		listed[strings.ToLower(m[1])] = true
	}

	// Both directions, and neither is a count. A count let seven of the
	// eighteen occurrences vanish while still clearing a floor of eleven —
	// un-backticking two Stripe origins stopped them being checked with the
	// suite green, which is the partial blindness this file keeps rediscovering.
	for host := range disclosedAs {
		assert.True(t, listed[host],
			"disclosedAs has an entry for %s but docs/security/csp-audit.md no longer lists it — either the origin is gone and this entry is stale, or the audit's table has been reformatted and this check is reading less than it looks", host)
	}
	for host := range listed {
		_, known := vendorFor(host)
		assert.True(t, known,
			"docs/security/csp-audit.md lists %s as an origin the browser fetches, but disclosedAs has no entry for it", host)
	}
}

// vendorName pulls the bolded name out of each vendor list entry.
var vendorName = regexp.MustCompile(`(?is)<li[^>]*><strong[^>]*>(.*?)</strong>`)

// The checklist is bound to the page in both directions. Its own test asserts
// every listed vendor is named on the page; without this, dropping an entry
// from the checklist silently stops checking that vendor while the page goes on
// naming it — a reviewer removed Broadwave and the suite stayed green.
func TestEveryVendorOnThePageIsOnTheChecklist(t *testing.T) {
	list := vendorList(t, renderPrivacy(t))

	names := vendorName.FindAllStringSubmatch(list, -1)
	// A count, not a non-emptiness check: an <li> carrying a class attribute
	// used to be skipped, so a styling edit to some entries would quietly
	// shrink what this verifies while the test went on passing.
	require.Len(t, names, len(vendorsOnThePage),
		"found %d vendor names in the list but the checklist has %d — either an entry is unreadable to this regex or the two have drifted apart",
		len(names), len(vendorsOnThePage))

	for _, m := range names {
		name := strings.TrimSpace(m[1])
		_, listed := vendorsOnThePage[name]
		assert.True(t, listed,
			"the policy names %s but the checklist in privacy_vendors_test.go does not, so nothing verifies it belongs there", name)
	}
}

// operatorInDomain exempts the hosts whose registrable domain does not name the
// company that runs them. Every other entry must carry its operator, which
// catches an attribution that is plainly wrong — mis-filing cdn.jsdelivr.net
// under Cloudflare passed every other check in this file, because the policy
// names Cloudflare for its CDN anyway, and the page would have told a reader
// something false.
//
// Not proof: two vendors are exempted, and a wrong value among those is still
// only caught by a person. It forces the deliberate edit, which is the standard
// the rest of this file works to.
var operatorInDomain = map[string]string{
	"fonts.gstatic.com":           "Google serves its font files from gstatic",
	"cdn.rockabillyroasting.shop": "our domain in front of Cloudflare R2",
}

// alternation pulls the (?:a|b|c) members out of a pattern.
func alternation(t *testing.T, re *regexp.Regexp) []string {
	t.Helper()
	m := regexp.MustCompile(`\(\?:([a-z|]+)\)`).FindStringSubmatch(re.String())
	require.Len(t, m, 2, "no readable alternation in %s", re)
	return strings.Split(m[1], "|")
}

func TestDisclosedVendorsMatchTheirDomains(t *testing.T) {
	require.NotEmpty(t, disclosedAs)

	// The exemption map is pinned, because it is the one way to make this
	// check iterate nothing: listing all eleven hosts in it left the
	// jsDelivr-filed-under-Cloudflare mutation green. Adding an exemption
	// should cost a deliberate edit here, which is what every other guard in
	// this file asks for.
	require.Len(t, operatorInDomain, 2,
		"an exemption was added or removed — each one takes a host out of the attribution check below, so change this number in the same commit and say why in the map")
	for host := range operatorInDomain {
		require.Contains(t, disclosedAs, host,
			"%s is exempted from the attribution check but is no longer a disclosed host — a stale exemption is a hole waiting for the name to come back", host)
	}

	for host, vendor := range disclosedAs {
		if why, exempt := operatorInDomain[host]; exempt {
			t.Logf("%s exempt: %s", host, why)
			continue
		}
		// The company's name as the policy writes it: "Google Analytics 4" ->
		// "google", "jsDelivr" -> "jsdelivr". Compared against the host with
		// hyphens dropped, so sentry-cdn matches Sentry and googletagmanager
		// matches Google.
		company := strings.ToLower(strings.Fields(vendor)[0])
		assert.Contains(t, strings.ReplaceAll(host, "-", ""), company,
			"%s is attributed to %q, but %q appears nowhere in the host — if the attribution is right, exempt it in operatorInDomain with the reason", host, vendor, company)
	}
}
