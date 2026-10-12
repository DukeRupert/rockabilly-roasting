package storefront

import (
	"bytes"
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The account subscriptions page tells the customer what they can change and
// how, next to the controls that do it. Without it the buttons were the only
// hint, and a customer who wanted a different size or schedule had no way to
// learn that the shop would switch it for them.

// helpSection cuts the help panel out of the page, so an assertion on it is
// not satisfied by the account nav or a button elsewhere.
func helpSection(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, "data-subscription-help")
	require.GreaterOrEqual(t, start, 0, "the help panel renders")
	end := strings.Index(html[start:], "</details>")
	require.GreaterOrEqual(t, end, 0)
	return html[start : start+end]
}

func TestAccountSubscriptions_HelpNamesWhatTheCustomerCanChange(t *testing.T) {
	f := newBoxPageFixture(t)
	html := renderAccountSubscriptions(t, f, []AccountSubscriptionRow{
		f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee"),
	})

	help := helpSection(t, html)
	for _, want := range []string{"Skip", "Pause", "Resume", "Cancel", "Update payment method", "bag size or schedule"} {
		assert.Contains(t, help, want)
	}
	assert.Contains(t, help, `href="/account/addresses"`, "a move is an address edit")
	assert.Contains(t, help, `href="mailto:info@rockabillyroasting.com"`, "what they cannot change, the shop can")
}

// Nothing to change, nothing to explain: the empty state keeps its own prompt.
func TestAccountSubscriptions_NoHelpWithoutSubscriptions(t *testing.T) {
	html := renderAccountSubscriptions(t, newBoxPageFixture(t), nil)
	assert.NotContains(t, html, "data-subscription-help")
}

// The help quotes button labels so the customer can find them. Each quoted
// label must still be a control on the page, or the help sends them looking
// for a button that was renamed.
func TestAccountSubscriptions_HelpQuotesLabelsThePageUses(t *testing.T) {
	f := newBoxPageFixture(t)
	// Every state that carries a quoted control: a box of active items (the
	// whole-box skip and pause), one of them skipped (undo), a paused item
	// (resume), and a past-due one shipping on another day (retry), since a
	// past-due member would withdraw the whole-box controls.
	skipped := f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee")
	skipped.Subscription.Metadata = map[string]any{domain.SubscriptionMetaSkipUndo: map[string]any{
		"period_end":            f.run.AddDate(0, 0, -7).Format(time.RFC3339Nano),
		"next_order_at":         f.run.AddDate(0, 0, -3).Format(time.RFC3339Nano),
		"applied_next_order_at": f.run.Format(time.RFC3339Nano),
	}}
	rows := []AccountSubscriptionRow{
		skipped,
		f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Tea"),
		f.row(f.weekly, f.run, domain.SubscriptionStatusPaused, "Cocoa"),
		f.row(f.weekly, f.run.AddDate(0, 0, 1), domain.SubscriptionStatusPastDue, "Chai"),
	}
	stripeID := "cus_test"
	props := BuildAccountSubscriptionsProps(&domain.Customer{StripeCustomerID: &stripeID}, rows, f.tz)
	var buf bytes.Buffer
	require.NoError(t, AccountSubscriptionsContent(props).Render(context.Background(), &buf))
	html := buf.String()

	help := helpSection(t, html)
	page := strings.Replace(html, help, "", 1)

	// The list is what the help quotes, no more and no less, and each label is
	// a button's own text, not a prompt or heading that happens to share it.
	var quoted []string
	for _, m := range regexp.MustCompile(`<strong>([^<]+)</strong>`).FindAllStringSubmatch(help, -1) {
		quoted = append(quoted, m[1])
	}
	assert.ElementsMatch(t, subscriptionHelpLabels, slices.Compact(slices.Sorted(slices.Values(quoted))))
	for _, label := range subscriptionHelpLabels {
		button := regexp.MustCompile(`>\s*` + regexp.QuoteMeta(label) + `\s*(<svg[^>]*>.*?</svg>\s*)?</button>`)
		assert.Regexp(t, button, page, "%q is still a button on the page", label)
	}
}

// A page of cancelled subscriptions has no controls, so nothing to explain.
func TestAccountSubscriptions_NoHelpWhenNothingCanChange(t *testing.T) {
	f := newBoxPageFixture(t)
	html := renderAccountSubscriptions(t, f, []AccountSubscriptionRow{
		f.row(f.weekly, f.run, domain.SubscriptionStatusCancelled, "Coffee"),
	})
	assert.NotContains(t, html, "data-subscription-help")
}
