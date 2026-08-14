package storefront

import (
	"bytes"
	"context"
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

func renderToString(t *testing.T, c interface{ Render(context.Context, *bytes.Buffer) error }) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, c.Render(context.Background(), &buf))
	return buf.String()
}

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
	assert.Contains(t, html, "12+ $10.00 · 24+ $9.50", "breaks are readable without typing a quantity")
	assert.Contains(t, html, "data-unit-price", "unit price cell must be addressable for live requoting")
	assert.Contains(t, html, "data-nudge", "nudge slot must exist for the script to fill")
	assert.Contains(t, html, `data-nudge-pct="0.1"`)
	assert.Contains(t, html, `data-nudge-floor="3"`)

	assert.NotContains(t, html, "data-price=", "the fixed per-row price is gone; quantity decides it now")
}

func TestPortalFlatPriceStaysQuiet(t *testing.T) {
	flat := domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: 1100}})
	html := renderPortal(t, QuickOrderVariant{ID: uuid.New(), SKU: "X", UnitPrice: 1100, Ladder: flat})

	assertNoUninvokedComponents(t, html)
	assert.Contains(t, html, `data-ladder="[[1,1100]]"`)
	assert.NotContains(t, html, "+ $11.00", "a flat price has no breaks to advertise")
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
	assert.Contains(t, html, "12+ $10.00 · 24+ $9.50")
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
		Drop:            &drop,
		DropProductName: "Ethiopia",
		Items: []WholesaleCheckoutItem{{
			ItemID: uuid.New(), ProductName: "Ethiopia", SKU: "E",
			Quantity: 23, UnitPrice: 1000, LineTotal: 23000, Ladder: testLadder(),
		}},
	})

	assertNoUninvokedComponents(t, html)
	assert.Contains(t, html, "Ethiopia — price went up.")
	assert.Contains(t, html, "Now $10.00 each — you were getting $9.50 at 24+.")
	assert.Contains(t, html, `role="status"`, "advisory, not an alert — nothing was blocked")
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
