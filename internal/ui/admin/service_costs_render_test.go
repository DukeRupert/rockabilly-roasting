package admin

import (
	"bytes"
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// Every link in the control strip has to name its ranking.
//
// The regression: Hours carried the empty string, serviceCostsHref omitted the
// parameter for it, and the handler reads an absent sort as "give me the
// default" — so clicking Hours returned the cost ranking with Cost highlighted.
func TestServiceCostsHrefAlwaysNamesTheSort(t *testing.T) {
	for _, sort := range []string{"", "hours", "parts", "visits", "cost"} {
		href := serviceCostsHref(90, sort)

		u, err := url.Parse(href)
		require.NoError(t, err)
		got := u.Query().Get("sort")

		assert.NotEmpty(t, got, "%q produced %q, which reads as no preference", sort, href)
		if sort != "" {
			assert.Equal(t, sort, got)
		} else {
			assert.Equal(t, "hours", got,
				"the strip's own Hours link must name hours — an omitted parameter asks for the default, which is cost")
		}
	}
}

// The strip offers Cost only where there is money to rank on.
func TestServiceSortsOffersCostOnlyWhenCostable(t *testing.T) {
	var unrated []string
	for _, s := range serviceSorts(false) {
		unrated = append(unrated, s.Value)
	}
	assert.NotContains(t, unrated, "cost")
	assert.Contains(t, unrated, "hours")

	var rated []string
	for _, s := range serviceSorts(true) {
		rated = append(rated, s.Value)
	}
	assert.Equal(t, "cost", rated[0], "cost leads once it means something")
}

// The accessibility work on the new pages was asserted by reading the rendered
// HTML once. That is exactly the kind of thing that regresses silently, so it
// gets a test: CLAUDE.md commits the project to WCAG 2.1 AA and the calendar is
// a new primary surface.
func TestNewTablesCarryAccessibleNames(t *testing.T) {
	ctx := context.Background()
	today := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)

	t.Run("the month grid names itself and its days", func(t *testing.T) {
		var buf bytes.Buffer
		props := MaintenanceCalendarProps{
			Month: today,
			Days: []MaintenanceCalendarDay{
				{Date: today, InMonth: true, Rows: []domain.MaintenanceDueRow{{
					MaintenanceDue: domain.MaintenanceDue{DueOn: today},
					CustomerName:   "Blue Bottle",
					TaskName:       "Backflush",
				}}},
			},
			Today: today,
		}
		require.NoError(t, MaintenanceCalendarContent(props).Render(ctx, &buf))
		html := buf.String()

		assert.Contains(t, html, "<caption", "a data table needs an accessible name")
		assert.Contains(t, html, "Preventive maintenance due in September 2026")
		assert.Contains(t, html, "Tuesday 1 September",
			"a chip read aloud with no date attached tells a screen-reader user nothing")
	})

	t.Run("the cross-account table names itself", func(t *testing.T) {
		var buf bytes.Buffer
		props := ServiceCostsProps{Days: 90, Report: domain.ServiceAccountReport{
			Rows: []domain.ServiceAccountCost{{CustomerName: "Blue Bottle"}},
		}}
		require.NoError(t, ServiceCostsContent(props).Render(ctx, &buf))
		assert.Contains(t, buf.String(), "What servicing each account has taken")
	})

	t.Run("the machine cost card names itself", func(t *testing.T) {
		var buf bytes.Buffer
		windows := []domain.ServiceCostWindow{{
			Label:   "All time",
			Summary: domain.ServiceCostSummary{ServiceTotals: domain.ServiceTotals{PartsCostCents: 100}},
		}}
		require.NoError(t, ServiceCostCard(windows, false, "nothing yet").Render(ctx, &buf))
		assert.Contains(t, buf.String(), "Parts and hours by period")
	})
}

// Pinning the accessibility work that a018eb1 added, which until now lived only
// in source and could regress in silence.
func TestCalendarAccessibleAffordances(t *testing.T) {
	ctx := context.Background()
	today := time.Date(2026, time.September, 1, 0, 0, 0, 0, time.UTC)
	overdue := time.Date(2026, time.August, 20, 0, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	props := MaintenanceCalendarProps{
		Month: today,
		Days: []MaintenanceCalendarDay{
			{Date: overdue, InMonth: true, Rows: []domain.MaintenanceDueRow{{
				MaintenanceDue: domain.MaintenanceDue{DueOn: overdue, Status: domain.MaintenanceStatusPending},
				CustomerName:   "Blue Bottle",
				TaskName:       "Backflush",
				LeadDays:       5,
			}}},
		},
		Today: today,
	}
	require.NoError(t, MaintenanceCalendarContent(props).Render(ctx, &buf))
	html := buf.String()

	// WCAG 1.4.1: the chips differ by background colour, so the urgency has to
	// exist as text a screen reader can reach.
	assert.Contains(t, html, "overdue",
		"a chip's colour must not be the only thing carrying its state")
	assert.Contains(t, html, "20 August", "and the date must travel with it")
}

// Two landmarks called "Section" on one page are two a screen-reader user
// cannot tell apart. The due list draws the Service strip and its own scope
// strip, so the second carries a name of its own.
func TestDueListLandmarksAreDistinct(t *testing.T) {
	var buf bytes.Buffer
	props := MaintenanceDueProps{Today: time.Now()}
	require.NoError(t, MaintenanceDueContent(props).Render(context.Background(), &buf))
	html := buf.String()

	assert.Contains(t, html, `aria-label="Maintenance scope"`)
	assert.Equal(t, 1, strings.Count(html, `aria-label="Section"`),
		"exactly one landmark may be called Section")
}
