package storefront

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// Rendering the components and reading the markup, because a templ mistake
// compiles: a component call written inline after other content on the same line
// is emitted as literal text rather than invoked, and nothing but the output
// shows it.

func renderPortal(t *testing.T, v QuickOrderVariant) string {
	t.Helper()
	var buf bytes.Buffer
	props := WholesalePortalProps{
		CompanyName: "Test Cafe",
		Products: []QuickOrderProduct{{
			ID:       uuid.New(),
			Title:    "Ethiopia",
			Options:  []string{"Size"},
			Variants: []QuickOrderVariant{v},
		}},
	}
	require.NoError(t, WholesalePortalContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func renderCheckout(t *testing.T, props WholesaleCheckoutProps) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, WholesaleCheckoutContent(props).Render(context.Background(), &buf))
	return buf.String()
}

// rungClass returns the classes on a rendered ladder rung. The ladder is markup
// now, not a run of text, so assertions have to read it as markup — a contains
// check on "12+ $10.00 · 24+ $9.50" would pass vacuously forever.
func rungClass(html, minQty string) (string, bool) {
	m := regexp.MustCompile(`<span class="([^"]*)" data-rung="` + minQty + `">`).FindStringSubmatch(html)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// assertRungEmphasis checks a rung is rendered, and whether it carries the
// weight of the rung the buyer has earned.
func assertRungEmphasis(t *testing.T, html, minQty string, wantActive bool) {
	t.Helper()
	cls, ok := rungClass(html, minQty)
	if !assert.True(t, ok, "rung %s+ was not rendered", minQty) {
		return
	}
	assert.Equal(t, wantActive, strings.Contains(cls, "font-semibold"),
		"rung %s+ emphasis; classes were %q", minQty, cls)
}

// assertNoLadder asserts the ladder yielded its slot to something outranking it.
func assertNoLadder(t *testing.T, html string) {
	t.Helper()
	assert.NotContains(t, html, "data-rung=", "ladder must yield the slot, not sit beside it")
}

// assertNoUninvokedComponents guards the failure mode that shipped in the admin
// typeahead: templ rendering "@component(args)" to the page as text.
func assertNoUninvokedComponents(t *testing.T, html string) {
	t.Helper()
	assert.NotContains(t, html, "@ladderBreaks", "component call rendered as literal text")
	for _, frag := range []string{"@upgradeLine", "@ladderHint", "@dropLine", "@TierSummary"} {
		assert.NotContains(t, html, frag, "component call rendered as literal text")
	}
}

func TestPortalRendersLadder(t *testing.T) {
	html := renderPortal(t, QuickOrderVariant{
		ID:        uuid.New(),
		SKU:       "ETH-12O-DRI",
		UnitPrice: 1100,
		Ladder:    testLadder(),
	})

	assertNoUninvokedComponents(t, html)
	assert.Contains(t, html, `data-ladder="[[1,1100],[12,1000],[24,950]]"`, "sheet needs the whole ladder to reprice as the buyer types")
	assertRungEmphasis(t, html, "12", false)
	assertRungEmphasis(t, html, "24", false)
	assert.Contains(t, html, "data-unit-price", "unit price cell must be addressable for live requoting")
	// The sheet shows the ladder and nothing else: no nudge slot, and no
	// thresholds published for arithmetic it no longer does.
	assert.NotContains(t, html, "data-nudge-note")
	assert.NotContains(t, html, "data-nudge-pct")
	assert.NotContains(t, html, "data-nudge-floor")
	assert.NotContains(t, html, "data-price=", "the fixed per-row price is gone; quantity decides it now")
}

func TestPortalFlatPriceStaysQuiet(t *testing.T) {
	flat := domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: 1100}})
	html := renderPortal(t, QuickOrderVariant{ID: uuid.New(), SKU: "X", UnitPrice: 1100, Ladder: flat})

	assertNoUninvokedComponents(t, html)
	assert.Contains(t, html, `data-ladder="[[1,1100]]"`)
	assertNoLadder(t, html)
}

func TestCheckoutRendersUpgradeNudge(t *testing.T) {
	html := renderCheckout(t, WholesaleCheckoutProps{
		CompanyName: "Test Cafe",
		Items: []WholesaleCheckoutItem{{
			ItemID:      uuid.New(),
			VariantID:   uuid.New(),
			ProductName: "Ethiopia",
			SKU:         "ETH-12O-DRI",
			Quantity:    23,
			UnitPrice:   1000,
			LineTotal:   23000,
			Ladder:      testLadder(),
		}},
		Subtotal: 23000,
	})

	assertNoUninvokedComponents(t, html)
	assert.Contains(t, html, "Add 1 more and pay $2.00 less", "one unit short of a break is the nudge worth showing")
	// Marked in amber, worded in ink.
	assert.Contains(t, html, `<span class="shrink-0 text-candle-deep" aria-hidden="true">◆</span>`)
	// One note per line: the nudge already names the rung, so repeating the
	// whole ladder beside it restates rather than informs.
	assertNoLadder(t, html)
}

func TestCheckoutFallsBackToLadderWhenNoNudge(t *testing.T) {
	html := renderCheckout(t, WholesaleCheckoutProps{
		Items: []WholesaleCheckoutItem{{
			ItemID: uuid.New(), ProductName: "Ethiopia", SKU: "E",
			Quantity: 2, UnitPrice: 1100, LineTotal: 2200, Ladder: testLadder(),
		}},
	})

	assertNoUninvokedComponents(t, html)
	assert.NotContains(t, html, "◆", "the marker belongs to the nudge, not the price list")
	assert.Contains(t, html, "12+ $10.00", "far from a break, show what exists")
	assert.Contains(t, html, "24+ $9.50")
	assert.NotContains(t, html, "Add ")

	// At 2 the buyer has earned nothing, so no rung carries weight.
	assertRungEmphasis(t, html, "12", false)
	assertRungEmphasis(t, html, "24", false)
}

func TestCheckoutMarksTheEarnedRung(t *testing.T) {
	// The ladder should say "you have this one", not merely list what exists.
	for _, tc := range []struct {
		qty            int
		twelve, twenty bool
	}{
		{qty: 11, twelve: false, twenty: false},
		{qty: 12, twelve: true, twenty: false},
		{qty: 23, twelve: true, twenty: false},
		{qty: 40, twelve: false, twenty: true},
	} {
		html := renderCheckout(t, WholesaleCheckoutProps{
			Items: []WholesaleCheckoutItem{{
				ItemID: uuid.New(), ProductName: "Ethiopia", SKU: "E",
				Quantity: tc.qty, UnitPrice: testLadder().UnitPriceAt(tc.qty), Ladder: testLadder(),
			}},
		})
		// Quantities near a break show the nudge instead, which is the ranking
		// working as intended; only assert emphasis where the ladder rendered.
		if _, ok := rungClass(html, "12"); !ok {
			continue
		}
		assertRungEmphasis(t, html, "12", tc.twelve)
		assertRungEmphasis(t, html, "24", tc.twenty)
	}
}

func TestCheckoutTopRungSaysNothing(t *testing.T) {
	html := renderCheckout(t, WholesaleCheckoutProps{
		Items: []WholesaleCheckoutItem{{
			ItemID: uuid.New(), ProductName: "Ethiopia", SKU: "E",
			Quantity: 40, UnitPrice: 950, LineTotal: 38000, Ladder: testLadder(),
		}},
	})

	assertNoUninvokedComponents(t, html)
	assert.NotContains(t, html, "Add ", "a buyer on the best price has nothing to be nudged toward")
}

func TestCheckoutRendersDropNotice(t *testing.T) {
	drop, ok := testLadder().Drop(24, 23)
	require.True(t, ok)

	html := renderCheckout(t, WholesaleCheckoutProps{
		Items: []WholesaleCheckoutItem{{
			ItemID: uuid.New(), ProductName: "Ethiopia", SKU: "E",
			Quantity: 23, UnitPrice: 1000, LineTotal: 23000,
			Ladder: testLadder(), Drop: &drop,
		}},
	})

	assertNoUninvokedComponents(t, html)
	assert.Contains(t, html, "Now $10.00 each — add 1 back to get $9.50 again.")
	assert.Contains(t, html, `role="status"`, "advisory, not an alert — nothing was blocked")

	// Rendered on the line it happened to, so position identifies it. A cart
	// routinely holds two lines of one coffee in different grinds, where naming
	// the product would point at both.
	assert.NotContains(t, html, "Ethiopia — price went up")

	// A drop outranks the nudge and the ladder, absorbing the nudge rather than
	// discarding it — the way back is in the same sentence.
	assert.NotContains(t, html, "Add 1 more", "the drop owns the slot this render")
	assertNoLadder(t, html)
}

func TestCheckoutWithoutDropRendersNoNotice(t *testing.T) {
	html := renderCheckout(t, WholesaleCheckoutProps{
		Items: []WholesaleCheckoutItem{{
			ItemID: uuid.New(), ProductName: "Ethiopia", SKU: "E",
			Quantity: 12, UnitPrice: 1000, LineTotal: 12000, Ladder: testLadder(),
		}},
	})
	assert.NotContains(t, html, "price went up")
}

// portalScript returns the sheet's x-data expression, so the parity test can run
// the very script the page ships rather than a copy of it.
func portalScript(t *testing.T) string {
	t.Helper()
	html := renderPortal(t, QuickOrderVariant{ID: uuid.New(), SKU: "S", UnitPrice: 1100, Ladder: testLadder()})

	const marker = `x-data="`
	i := strings.Index(html, marker)
	require.GreaterOrEqual(t, i, 0, "sheet must carry an x-data block")
	rest := html[i+len(marker):]
	j := strings.Index(rest, `"`)
	require.Greater(t, j, 0)

	script := rest[:j]
	// templ escapes the attribute; undo it to get executable JS back.
	r := strings.NewReplacer("&#34;", `"`, "&amp;", "&", "&lt;", "<", "&gt;", ">", "&#39;", "'", "&#x27;", "'")
	return r.Replace(script)
}
