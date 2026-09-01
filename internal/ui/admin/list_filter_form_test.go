package admin

import (
	"bytes"
	"context"
	"regexp"
	"sort"
	"testing"
	"time"

	"github.com/a-h/templ"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Every admin list page carries its filter state through one GET form, and the
// search box sends that form with hx-include="closest form". This test exists
// because the alternative is silent and was live for months.
//
// hx-include resolves its selector with querySelectorAll and htmx's addValues
// dedupes by *element*, not by name, before appending each value to the
// FormData. So a parameter carried both as a hidden input beside the search box
// and as a real control further down was sent twice, and Go's Query().Get takes
// the first in document order — the stale hidden copy. The visible symptom was
// small and baffling: type a custom date, don't press Apply, type one character
// in search, and the date reverts.
//
// Nothing about that is visible in a diff, in a passing handler test, or on
// screen. It is only visible in the rendered markup, which is what these read.

// listFilterPages is every page whose search box includes a filter form, in
// both of the states whose markup differs. Adding a list page here is cheaper
// than rediscovering this bug on it.
//
// Each page appears twice. Under a *custom* date range the from/to inputs are
// rendered and the range is carried beside them; under a *preset* they are not
// rendered at all and the range rides a hidden input instead. Those are two
// different sets of elements, so testing only one leaves the other free to grow
// a duplicate — which is exactly the shape of the bug this file exists for.
func listFilterPages(t *testing.T) map[string]templ.Component {
	t.Helper()
	now := time.Now()
	return map[string]templ.Component{
		// Each is built with every filter populated at once — that is the state
		// where duplicate names actually collide.
		"audit": AuditListContent(AuditListProps{
			Search: "w", ActorTypeFilter: "staff", Sort: "oldest",
			Range: "custom", From: "2026-08-01", To: "2026-08-15",
			Area: "order", ActionFilter: "order.refunded", ResourceFilter: "order",
			Areas: []string{"order"}, Actions: []string{"order.refunded", "order.shipped"},
			ResourceTypes: []string{"order"},
			ActorID:       "11111111-1111-4111-8111-111111111111",
			ResourceID:    "22222222-2222-4222-8222-222222222222",
			CustomerID:    "33333333-3333-4333-8333-333333333333",
			MerchantTZ:    time.UTC, Page: 1, PerPage: 50,
		}),
		"orders": OrderListContent(OrderListProps{
			BasePath: "/admin/orders", Title: "Orders", Search: "w",
			View: "all", Sort: "placed_desc", Payment: "captured", Fulfillment: "unfulfilled",
			Range: "custom", From: "2026-08-01", To: "2026-08-15", Min: "5", Max: "50",
			MerchantTZ: time.UTC, Now: now, Page: 1, PerPage: 25,
		}),
		"customers": CustomerListContent(CustomerListProps{
			Search: "w", StatusFilter: "pending", VerifiedFilter: "yes", Sort: "name",
			MerchantTZ: time.UTC, Page: 1, PerPage: 25,
		}),
		"subscriptions": SubscriptionListContent(SubscriptionListProps{
			SearchQuery: "w", StatusFilter: "active", Sort: "next",
			Due: "custom", From: "2026-08-01", To: "2026-08-15",
			Counts: map[string]int{}, MerchantTZ: time.UTC, Page: 1, PerPage: 25,
		}),

		// The preset branch: no date inputs, range carried hidden instead.
		"audit (preset range)": AuditListContent(AuditListProps{
			Search: "w", ActorTypeFilter: "staff", Sort: "oldest", Range: "30d",
			Area: "order", ActionFilter: "order.refunded", ResourceFilter: "order",
			Areas: []string{"order"}, Actions: []string{"order.refunded", "order.shipped"},
			ResourceTypes: []string{"order"},
			ActorID:       "11111111-1111-4111-8111-111111111111",
			MerchantTZ:    time.UTC, Page: 1, PerPage: 50,
		}),
		"orders (preset range)": OrderListContent(OrderListProps{
			BasePath: "/admin/orders", Title: "Orders", Search: "w",
			View: "all", Sort: "placed_desc", Payment: "captured", Fulfillment: "unfulfilled",
			Range: "30d", Min: "5", Max: "50",
			MerchantTZ: time.UTC, Now: now, Page: 1, PerPage: 25,
		}),
		"subscriptions (preset range)": SubscriptionListContent(SubscriptionListProps{
			SearchQuery: "w", StatusFilter: "active", Sort: "next", Due: "30d",
			Counts: map[string]int{}, MerchantTZ: time.UTC, Page: 1, PerPage: 25,
		}),
	}
}

func renderPage(t *testing.T, c templ.Component) string {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, c.Render(context.Background(), &b))
	return b.String()
}

var (
	namedInputRe = regexp.MustCompile(`<(?:input|select|textarea)[^>]*\bname="([a-zA-Z_]+)"`)
	formTagRe    = regexp.MustCompile(`</?form`)
)

// A parameter carried by two elements is a parameter whose value is decided by
// document order rather than by what the operator did.
func TestListFilterFormsCarryEachParameterOnce(t *testing.T) {
	for name, page := range listFilterPages(t) {
		t.Run(name, func(t *testing.T) {
			counts := map[string]int{}
			for _, m := range namedInputRe.FindAllStringSubmatch(renderPage(t, page), -1) {
				counts[m[1]]++
			}
			dupes := []string{}
			for param, n := range counts {
				if n > 1 {
					dupes = append(dupes, param)
				}
			}
			sort.Strings(dupes)
			assert.Empty(t, dupes,
				"these parameters are carried by more than one element, so htmx sends each of them "+
					"more than once and the server reads whichever the markup happens to emit first")
		})
	}
}

// hx-include="closest form" is only correct while there is exactly one form and
// nothing is nested inside it — a nested form is dropped by the parser, taking
// its fields with it, and the failure is invisible until a filter stops working.
func TestListFilterFormsAreSingleAndUnnested(t *testing.T) {
	for name, page := range listFilterPages(t) {
		t.Run(name, func(t *testing.T) {
			html := renderPage(t, page)
			depth, max, opens := 0, 0, 0
			for _, tag := range formTagRe.FindAllString(html, -1) {
				if tag == "<form" {
					opens++
					depth++
					if depth > max {
						max = depth
					}
				} else {
					depth--
				}
			}
			assert.Equal(t, 1, opens, "the filter state belongs to exactly one form")
			assert.LessOrEqual(t, max, 1, "a nested form is silently dropped along with its fields")
			assert.Equal(t, 0, depth, "unbalanced form tags")
			assert.Contains(t, html, `hx-include="closest form"`,
				"the search box must send its own form, not a document-wide name selector")
		})
	}
}

// The selector this bans is the one that caused the bug: it matches by name
// across the whole document, so it picks up every copy of a parameter wherever
// it lives.
func TestListFilterSearchDoesNotIncludeByBareName(t *testing.T) {
	for name, page := range listFilterPages(t) {
		t.Run(name, func(t *testing.T) {
			assert.NotRegexp(t, `hx-include="\[name=`, renderPage(t, page),
				"include the form, not a document-wide match on a parameter name")
		})
	}
}
