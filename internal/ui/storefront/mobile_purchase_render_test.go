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

// These pin the two defects that shipped once already on the product page, both
// of which were invisible to a compile and to every other test: the price tag's
// base-price attribute living on the element the price renderer replaces, and a
// second writer rewriting that element behind the controller's back.

func renderProduct(t *testing.T, props ProductDetailProps) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, ProductContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func productFixture() ProductDetailProps {
	small, large := 1800, 6480
	productID, optionID := uuid.New(), uuid.New()
	smallID, largeID := uuid.New(), uuid.New()
	smallValueID, largeValueID := uuid.New(), uuid.New()

	return ProductDetailProps{
		Product: &domain.Product{ID: productID, Title: "2-Stroke", Slug: "2-stroke"},
		Variants: []VariantWithPrice{
			{Variant: domain.Variant{ID: smallID, SKU: "2S-12OZ"}, BasePrice: &small},
			{Variant: domain.Variant{ID: largeID, SKU: "2S-3LB"}, BasePrice: &large},
		},
		Options: []OptionWithValues{{
			Option: domain.ProductOption{ID: optionID, ProductID: productID, Name: "Weight"},
			Values: []domain.ProductOptionValue{
				{ID: smallValueID, ProductOptionID: optionID, Value: "12oz"},
				{ID: largeValueID, ProductOptionID: optionID, Value: "3lb"},
			},
		}},
		VariantMap: map[string]VariantOptionEntry{
			smallID.String(): {OptionValueIDs: []string{smallValueID.String()}, Price: &small},
			largeID.String(): {OptionValueIDs: []string{largeValueID.String()}, Price: &large},
		},
		DefaultPrice: &small,
		CurrencyCode: "USD",
	}
}

// The price controller reads data-base-price off #price-display itself and
// rewrites that element's children on every update. Put the attribute back on a
// child and the first update deletes it, freezing the price from then on — which
// is exactly how the subscribe price stopped responding to frequency and
// quantity changes.
func TestProductPriceDisplayCarriesBasePriceOnTheContainer(t *testing.T) {
	html := renderProduct(t, productFixture())

	open := strings.Index(html, `id="price-display"`)
	require.NotEqual(t, -1, open, "product page should render a #price-display")

	tagStart := strings.LastIndex(html[:open], "<")
	tagEnd := strings.Index(html[open:], ">") + open
	openingTag := html[tagStart:tagEnd]

	assert.Contains(t, openingTag, "data-base-price",
		"data-base-price must live on the #price-display element, not on a child the renderer replaces")
	assert.Equal(t, 1, strings.Count(html, "data-base-price"),
		"exactly one data-base-price in the document; a second one means another writer is tracking the price separately")
}

// Anything that moves the price has to go through the controller. A direct
// innerHTML write to #price-display is how picking a variant used to wipe an
// applied subscription discount.
func TestProductPriceHasASingleWriter(t *testing.T) {
	plan := domain.SubscriptionPlan{ID: uuid.New(), Interval: domain.SubscriptionIntervalEvery14Days, IntervalCount: 1, DiscountPct: 10}
	props := productFixture()
	props.SubscriptionPlans = []domain.SubscriptionPlan{plan}

	html := renderProduct(t, props)

	require.Contains(t, html, "window.rrPrice", "the price controller should be emitted when the product has a price")
	assert.Equal(t, 1, strings.Count(html, `getElementById('price-display')`),
		"only priceControllerScript may reach for #price-display; route other updates through window.rrPrice")
	for _, setter := range []string{"setBase", "setDiscount", "setQuantity", "setMode"} {
		assert.Contains(t, html, "window.rrPrice."+setter,
			"subscription and variant handling should drive the price through %s", setter)
	}
}

// buyBarScript returns just the sticky bar's own script. Asserting against the
// whole document is how the first version of these checks ended up inert: the
// page also carries a GA4 script that mentions the add-to-cart form, and templ
// emits JS comments verbatim, so a comment can satisfy an assertion about code.
func buyBarScript(t *testing.T, html string) string {
	t.Helper()
	marker := "var state = window.__rrBuyBar;"
	i := strings.Index(html, marker)
	require.NotEqual(t, -1, i, "expected the mobile buy bar script")
	end := strings.Index(html[i:], "</script>")
	require.NotEqual(t, -1, end, "expected the buy bar script to be closed")
	script := html[i : i+end]
	// Strip line comments so prose cannot stand in for behaviour.
	var code []string
	for _, line := range strings.Split(script, "\n") {
		if trimmed := strings.TrimSpace(line); strings.HasPrefix(trimmed, "//") {
			continue
		}
		code = append(code, line)
	}
	return strings.Join(code, "\n")
}

// The sticky bar is the only buy control a phone customer sees once the real buy
// box scrolls away, so it has to reach the real form rather than post on its own.
func TestMobileBuyBarDrivesTheRealForm(t *testing.T) {
	html := renderProduct(t, productFixture())
	require.Contains(t, html, `id="mobile-buy-bar"`)

	script := buyBarScript(t, html)

	assert.Contains(t, script, `querySelector('#onetime-form form')`,
		"the bar should submit the real add-to-cart form rather than post on its own")

	barTag := html[strings.Index(html, `<button id="mobile-buy-action"`):]
	barTag = barTag[:strings.Index(barTag, ">")]
	assert.NotContains(t, barTag, "hx-post",
		"the bar's button must not carry its own cart endpoint")

	assert.Contains(t, script, `classList.toggle('invisible'`,
		"a hidden bar should be visibility:hidden so its button leaves the tab order")
}

// A product with no variants cannot be bought, so it gets no bar and no spacer
// reserving room for one.
func TestMobileBuyBarAbsentWithoutVariants(t *testing.T) {
	props := productFixture()
	props.Variants = nil
	props.VariantMap = nil
	props.Options = nil

	html := renderProduct(t, props)

	assert.NotContains(t, html, `id="mobile-buy-bar"`)
}

func renderCart(t *testing.T, props CartPageProps) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, CartContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func cartFixture() CartPageProps {
	return CartPageProps{
		CartID: uuid.New(),
		Items: []CartItemDisplay{{
			ItemID:       uuid.New(),
			VariantID:    uuid.New(),
			ProductTitle: "2-Stroke",
			ProductSlug:  "2-stroke",
			VariantLabel: "12oz · whole bean",
			SKU:          "2S-12OZ",
			Quantity:     2,
			UnitPrice:    1800,
			LineTotal:    3600,
		}},
		Subtotal:  3600,
		CartCount: 2,
	}
}

// Alpine's $el inside an x-data method is whichever element invoked it — the
// clicked button — not the component root. Submitting through $el silently does
// nothing but log a TypeError, so the quantity never reaches the server.
func TestCartQuantityStepperSubmitsThroughRoot(t *testing.T) {
	html := renderCart(t, cartFixture())

	assert.Contains(t, html, "$root.requestSubmit()",
		"the stepper must submit the form ($root), not the button that was clicked ($el)")
	assert.NotContains(t, html, "$el.requestSubmit()",
		"$el is the clicked button here and has no requestSubmit")
}

// Every control on a cart line is a touch target on the surface this page was
// rebuilt for.
func TestCartLineControlsMeetTouchTargetSize(t *testing.T) {
	html := renderCart(t, cartFixture())

	for _, label := range []string{"Decrease quantity for 2-Stroke", "Increase quantity for 2-Stroke", "Remove 2-Stroke from cart"} {
		idx := strings.Index(html, label)
		require.NotEqual(t, -1, idx, "expected a control labelled %q", label)
		tagStart := strings.LastIndex(html[:idx], "<button")
		require.NotEqual(t, -1, tagStart)
		tagEnd := strings.Index(html[idx:], ">") + idx
		assert.Contains(t, html[tagStart:tagEnd], "min-h-11",
			"control %q should be at least 44px tall", label)
	}
}

// An empty cart has nothing to check out, so it gets no sticky checkout bar.
func TestCartMobileBarOnlyWithItems(t *testing.T) {
	full := renderCart(t, cartFixture())
	assert.Contains(t, full, `id="cart-mobile-bar"`)

	empty := renderCart(t, CartPageProps{CartID: uuid.New()})
	assert.NotContains(t, empty, `id="cart-mobile-bar"`)
}
