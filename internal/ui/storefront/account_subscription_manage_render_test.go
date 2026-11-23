package storefront

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The Change form on a subscription and the Add a coffee form on a box. They
// are offered only where the service would accept them, and open on what the
// subscription already has, so saving untouched changes nothing.

func renderWithChoices(t *testing.T, f boxPageFixture, rows []AccountSubscriptionRow, choices []SubscriptionChoice) string {
	t.Helper()
	props := BuildAccountSubscriptionsProps(&domain.Customer{}, rows, f.tz)
	props.Choices = choices
	props.Plans = []domain.SubscriptionPlan{*f.weekly, *f.month}
	var buf bytes.Buffer
	require.NoError(t, AccountSubscriptionsContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func TestAccountSubscriptions_ChangeFormOnlyWhereItCanBeSaved(t *testing.T) {
	f := newBoxPageFixture(t)
	active := f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee")
	paused := f.row(f.weekly, f.run, domain.SubscriptionStatusPaused, "Tea")
	pastDue := f.row(f.weekly, f.run.AddDate(0, 0, 1), domain.SubscriptionStatusPastDue, "Chai")
	cancelled := f.row(f.weekly, f.run, domain.SubscriptionStatusCancelled, "Cocoa")
	html := renderWithChoices(t, f, []AccountSubscriptionRow{active, paused, pastDue, cancelled}, nil)

	for _, row := range []AccountSubscriptionRow{active, paused} {
		assert.Contains(t, html, `action="/account/subscriptions/`+row.Subscription.ID.String()+`/edit"`)
	}
	for _, row := range []AccountSubscriptionRow{pastDue, cancelled} {
		assert.NotContains(t, html, `action="/account/subscriptions/`+row.Subscription.ID.String()+`/edit"`,
			"the service refuses it, so the page does not offer it")
	}
}

func TestAccountSubscriptions_ChangeFormOpensOnWhatTheSubscriptionHas(t *testing.T) {
	f := newBoxPageFixture(t)
	productID := uuid.New()
	row := f.row(f.month, f.run, domain.SubscriptionStatusActive, "Coffee")
	row.Product.ID = productID
	row.Subscription.Quantity = 2
	row.Subscription.VariantID = uuid.New()
	row.VariantLabel = "Whole Bean · 12oz"
	base := 2000
	row.BasePrice = &base
	f.month.DiscountPct = 10

	ground := SubscriptionChoice{VariantID: uuid.New(), ProductID: productID, ProductTitle: "Coffee", VariantLabel: "Ground · 12oz", BasePrice: 2000}
	elsewhere := SubscriptionChoice{VariantID: uuid.New(), ProductID: uuid.New(), ProductTitle: "Decaf", VariantLabel: "Whole Bean · 12oz", BasePrice: 1900}
	html := renderWithChoices(t, f, []AccountSubscriptionRow{row}, []SubscriptionChoice{ground, elsewhere})

	start := strings.Index(html, `data-subscription-change="`+row.Subscription.ID.String()+`"`)
	require.GreaterOrEqual(t, start, 0)
	form := html[start : start+strings.Index(html[start:], "</form>")]

	assert.Contains(t, form, `<option value="`+row.Subscription.VariantID.String()+`" data-price="2000" selected>Whole Bean · 12oz</option>`,
		"the current grind first, selected")
	assert.Contains(t, form, ground.VariantID.String(), "a sibling of the same coffee")
	assert.NotContains(t, form, elsewhere.VariantID.String(), "never another coffee: that is add, then cancel")
	assert.Contains(t, form, `<option value="2" selected>2</option>`)
	assert.Contains(t, form, `<option value="`+f.month.ID.String()+`" data-discount="10" selected>Monthly</option>`)
	// $20.00 less 10% is $18.00, times two.
	assert.Contains(t, form, `data-testid="change-price">$36.00</span>`)
	assert.Contains(t, form, "Nothing is charged today")
}

func TestAccountSubscriptions_AddACoffee(t *testing.T) {
	f := newBoxPageFixture(t)
	decaf := []SubscriptionChoice{{VariantID: uuid.New(), ProductID: uuid.New(), ProductTitle: "Decaf", BasePrice: 1900}}
	const addForm = `action="/account/subscriptions/box/add"`

	t.Run("offered on a box, opening on the box's plan", func(t *testing.T) {
		html := renderWithChoices(t, f, []AccountSubscriptionRow{
			f.row(f.month, f.run, domain.SubscriptionStatusActive, "Coffee"),
		}, decaf)
		assert.Contains(t, html, addForm)
		assert.Contains(t, html, ">Add a coffee</button>")
		assert.Contains(t, html, `<option value="`+f.month.ID.String()+`" data-discount="0" selected>`)
		assert.Contains(t, html, `value="`+f.home.String()+`"`)
	})

	t.Run("not with a past-due member", func(t *testing.T) {
		html := renderWithChoices(t, f, []AccountSubscriptionRow{
			f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee"),
			f.row(f.weekly, f.run, domain.SubscriptionStatusPastDue, "Tea"),
		}, decaf)
		assert.NotContains(t, html, addForm)
	})

	t.Run("not on a full box", func(t *testing.T) {
		var rows []AccountSubscriptionRow
		for i := 0; i < maxBoxItems; i++ {
			rows = append(rows, f.row(f.weekly, f.run, domain.SubscriptionStatusActive, fmt.Sprintf("Coffee %d", i)))
		}
		html := renderWithChoices(t, f, rows, decaf)
		assert.NotContains(t, html, addForm)
	})

	t.Run("not when there is nothing to add", func(t *testing.T) {
		html := renderWithChoices(t, f, []AccountSubscriptionRow{
			f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee"),
		}, nil)
		assert.NotContains(t, html, addForm)
	})
}

// Every form on the list saves back to this page. The body's boost scrolls
// to the top after a swap, so the list overrides it: a customer who changes
// the third card should still be looking at the third card.
func TestAccountSubscriptions_SavingDoesNotScrollToTheTop(t *testing.T) {
	f := newBoxPageFixture(t)
	decaf := []SubscriptionChoice{{VariantID: uuid.New(), ProductID: uuid.New(), ProductTitle: "Decaf", BasePrice: 1900}}
	html := renderWithChoices(t, f, []AccountSubscriptionRow{
		f.row(f.month, f.run, domain.SubscriptionStatusActive, "Coffee"),
	}, decaf)

	list := strings.Index(html, `hx-swap="innerHTML show:none"`)
	require.GreaterOrEqual(t, list, 0, "the list overrides the body's show:window:top")
	for _, form := range []string{`/edit"`, `action="/account/subscriptions/box/add"`, `action="/account/subscriptions/box/skip"`} {
		assert.Greater(t, strings.Index(html, form), list, "%s sits inside the list and inherits the override", form)
	}
}

// The Change and Add a coffee forms sit in drawers that slide open and shut.
// A saved change keeps its drawer open and swaps only the card's summary, so
// the customer sees the new values without anything moving. A coffee added
// closes its drawer, and the swap waits for the slide, so nothing under the
// form leaps up when the page comes back.
func TestAccountSubscriptions_Drawers(t *testing.T) {
	f := newBoxPageFixture(t)
	decaf := []SubscriptionChoice{{VariantID: uuid.New(), ProductID: uuid.New(), ProductTitle: "Decaf", BasePrice: 1900}}
	active := f.row(f.month, f.run, domain.SubscriptionStatusActive, "Coffee")
	paused := f.row(f.month, f.run, domain.SubscriptionStatusPaused, "Tea")
	html := renderWithChoices(t, f, []AccountSubscriptionRow{active, paused}, decaf)

	assert.Equal(t, 2, strings.Count(html, `data-manage-drawer="change"`), "the active and the paused card")
	assert.Equal(t, 1, strings.Count(html, `data-manage-drawer="add"`))
	assert.NotContains(t, html, `x-show="confirming === &#39;change&#39;"`, "x-show cannot animate")

	t.Run("a saved change swaps only its own card's summary", func(t *testing.T) {
		for _, row := range []AccountSubscriptionRow{active, paused} {
			summary := "sub-summary-" + row.Subscription.ID.String()
			assert.Contains(t, html, `id="`+summary+`"`)
			start := strings.Index(html, `data-subscription-change="`+row.Subscription.ID.String()+`"`)
			require.GreaterOrEqual(t, start, 0)
			tag := html[strings.LastIndex(html[:start], "<form"):start]
			assert.Contains(t, tag, `hx-target="#`+summary+`"`)
			assert.Contains(t, tag, `hx-select="#`+summary+`"`)
			assert.Contains(t, tag, `hx-swap="outerHTML show:none"`)
			assert.Contains(t, tag, `saved = true`, "the drawer stays open and says Saved")
			assert.NotContains(t, tag, `confirming = &#39;&#39;`, "a saved change does not close the drawer")
		}
	})

	t.Run("a coffee added closes its drawer before the swap", func(t *testing.T) {
		start := strings.Index(html, `data-box-add`)
		require.GreaterOrEqual(t, start, 0)
		tag := html[strings.LastIndex(html[:start], "<form"):start]
		assert.Contains(t, tag, `hx-swap="innerHTML show:none swap:300ms"`)
		assert.Contains(t, html, "duration-300", "the swap delay is the slide's duration")
		assert.Contains(t, tag, `x-on:htmx:before-swap="if ($event.detail.xhr.getResponseHeader(&#39;HX-Reswap&#39;) !== &#39;none&#39;) confirming = &#39;&#39;"`,
			"a rejected save (HX-Reswap: none) leaves the drawer open")
	})

	t.Run("closed and inert until opened", func(t *testing.T) {
		start := strings.Index(html, `data-manage-drawer="add"`)
		tag := html[strings.LastIndex(html[:start], "<div"):start]
		assert.Contains(t, tag, " inert", "out of the tab order")
		assert.Contains(t, tag, "grid-rows-[0fr]")
	})
}

// The page's price, the form's live price and a renewal's charge must agree,
// so all three round the discount the same way: down, in whole cents.
func TestLineTotal_RoundsTheDiscountLikeARenewal(t *testing.T) {
	assert.Equal(t, 1700, discountedUnit(1999, 15), "1999 less 299 (299.85 rounded down)")
	assert.Equal(t, 3400, lineTotal(1999, 15, 2))
	assert.Contains(t, priceExpr, "Math.floor(unit * disc / 100)", "the live price rounds the discount down too")
}
