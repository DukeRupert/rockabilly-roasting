package admin

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// Reading the rendered markup: every link on this page is built by copying the
// filter state and changing one field, and a dropped field is invisible in the
// source — it only shows as a filter that vanishes when you sort or page.

func renderSubscriptionList(t *testing.T, props SubscriptionListProps) string {
	t.Helper()
	if props.MerchantTZ == nil {
		props.MerchantTZ = time.UTC
	}
	var buf bytes.Buffer
	require.NoError(t, SubscriptionListContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func subRow(name string, status domain.SubscriptionStatus, nextOrder time.Time) EnrichedSubscription {
	return EnrichedSubscription{
		Subscription: domain.Subscription{
			ID:          uuid.New(),
			Status:      status,
			Quantity:    1,
			NextOrderAt: nextOrder,
			CreatedAt:   nextOrder.AddDate(0, 0, -30),
		},
		CustomerName:  name,
		CustomerEmail: "someone@example.com",
		PlanName:      "Monthly",
		ProductTitle:  "House Blend",
	}
}

func TestSubscriptionList_StatusTabsCarryCounts(t *testing.T) {
	html := renderSubscriptionList(t, SubscriptionListProps{
		Subscriptions: []EnrichedSubscription{subRow("Ash Kagan", domain.SubscriptionStatusActive, time.Now())},
		StatusFilter:  "active",
		Counts:        map[string]int{"": 9, "active": 5, "paused": 2, "past_due": 1, "cancelled": 1},
		TotalCount:    5,
		Page:          1,
		PerPage:       25,
		Now:           time.Now(),
	})

	assert.Contains(t, html, "Past due")
	assert.Contains(t, html, ">9<", "the All tab shows the unfiltered total")
	assert.Contains(t, html, ">5<")
	// The active tab is a link to itself but marked as current.
	assert.Contains(t, html, `aria-current="page"`)
}

// Sorting must not drop the filters, and paging must not drop the search —
// this is the whole reason every link is built from one filter state.
func TestSubscriptionList_LinksPreserveFilterState(t *testing.T) {
	planID := uuid.New()
	props := SubscriptionListProps{
		Subscriptions: []EnrichedSubscription{subRow("Ash Kagan", domain.SubscriptionStatusActive, time.Now())},
		SearchQuery:   "kagan",
		StatusFilter:  "active",
		PlanFilter:    planID.String(),
		Due:           "7d",
		Page:          2,
		PerPage:       25,
		HasMore:       true,
		TotalCount:    60,
		Now:           time.Now(),
	}

	sortHref := subscriptionSortURL(props, "next_order")
	assert.Contains(t, sortHref, "q=kagan")
	assert.Contains(t, sortHref, "status=active")
	assert.Contains(t, sortHref, "plan="+planID.String())
	assert.Contains(t, sortHref, "due=7d")
	assert.Contains(t, sortHref, "sort=next_order_asc")
	assert.NotContains(t, sortHref, "page=", "changing the sort returns to page 1")

	nextHref := subscriptionPageHref(props, 3)
	assert.Contains(t, nextHref, "page=3")
	assert.Contains(t, nextHref, "q=kagan")
	assert.Contains(t, nextHref, "status=active")

	// Switching status keeps the rest but resets the page.
	statusHref := subscriptionFilterURL(props, "status", "paused")
	assert.Contains(t, statusHref, "status=paused")
	assert.Contains(t, statusHref, "q=kagan")
	assert.NotContains(t, statusHref, "page=")

	// Leaving the custom window drops the stale dates it owned.
	dated := props
	dated.Due, dated.From, dated.To = "custom", "2026-08-01", "2026-08-31"
	assert.NotContains(t, subscriptionFilterURL(dated, "due", "30d"), "from=")

	html := renderSubscriptionList(t, props)
	assert.Contains(t, html, "Showing")
	assert.Contains(t, html, ">60<", "the total is the count, not the page size")
}

// Typing in the search box must carry every active filter with it, or the
// search silently widens the list it was meant to narrow.
//
// This used to be asserted against a hand-maintained state container matched by
// id. The container is gone: every control now lives in one form and the search
// box sends that form, so the thing worth asserting is that each filter is
// present exactly once — see list_filter_form_test.go for why "exactly once"
// is the load-bearing half.
func TestSubscriptionList_SearchCarriesFilterState(t *testing.T) {
	html := renderSubscriptionList(t, SubscriptionListProps{
		Subscriptions: []EnrichedSubscription{subRow("Ash Kagan", domain.SubscriptionStatusActive, time.Now())},
		StatusFilter:  "paused",
		Due:           "overdue",
		Sort:          "customer_asc",
		Page:          1,
		PerPage:       25,
		Now:           time.Now(),
	})

	assert.Contains(t, html, `hx-include="closest form"`)
	for _, name := range []string{"status", "plan", "product", "due", "from", "to", "sort", "q"} {
		assert.Equal(t, 1, strings.Count(html, `name="`+name+`"`),
			"%s must be carried by exactly one element", name)
	}
	// The active values, not just the names.
	assert.Contains(t, html, `name="status" value="paused"`)
	assert.Contains(t, html, `name="due" value="overdue"`)
	assert.Contains(t, html, `name="sort" value="customer_asc"`)
}

// A failed search offers a way forward; an over-narrow filter set says so
// instead of reading as "you have no subscriptions".
func TestSubscriptionList_EmptyStates(t *testing.T) {
	company := "Zimmer Coffee"
	withSuggestions := renderSubscriptionList(t, SubscriptionListProps{
		SearchQuery: "kagen",
		Suggestions: []domain.Customer{{
			FirstName: "Ash", LastName: "Kagan",
			Email: "ash@example.com", CompanyName: &company,
		}},
		Page: 1, PerPage: 25, Now: time.Now(),
	})
	assert.Contains(t, withSuggestions, "Did you mean")
	assert.Contains(t, withSuggestions, "ash@example.com")
	assert.Contains(t, withSuggestions, "q=ash%40example.com")
	assert.Contains(t, withSuggestions, "Clear search and filters")

	filtered := renderSubscriptionList(t, SubscriptionListProps{
		StatusFilter: "expired",
		Due:          "today",
		Page:         1, PerPage: 25, Now: time.Now(),
	})
	assert.Contains(t, filtered, "No subscriptions match the current filters.")
	assert.Contains(t, filtered, "Clear all filters")

	virgin := renderSubscriptionList(t, SubscriptionListProps{Page: 1, PerPage: 25, Now: time.Now()})
	assert.Contains(t, virgin, "Subscriptions will appear here once customers subscribe to plans.")
	assert.NotContains(t, virgin, "Did you mean")
}

// An overdue renewal is the one date on this page that has to catch the eye.
func TestSubscriptionList_OverdueNextOrderIsAccented(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	overdue := subRow("Ash Kagan", domain.SubscriptionStatusActive, now.AddDate(0, 0, -2))
	upcoming := subRow("Billie Alvarez", domain.SubscriptionStatusActive, now.AddDate(0, 0, 2))

	assert.Contains(t, subscriptionNextOrderClass(overdue, now), "text-rr-red")
	assert.NotContains(t, subscriptionNextOrderClass(upcoming, now), "text-rr-red")

	// A cancelled subscription's stale next-order date isn't a call to action.
	cancelled := overdue
	cancelled.Status = domain.SubscriptionStatusCancelled
	assert.NotContains(t, subscriptionNextOrderClass(cancelled, now), "text-rr-red")
}
