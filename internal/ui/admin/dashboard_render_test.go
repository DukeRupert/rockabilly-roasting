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

// The dashboard's job is to make unfinished work impossible to miss. These
// tests read the rendered markup rather than the props, because the failure
// mode that matters — a band that silently doesn't render, a link that goes
// somewhere useless — compiles cleanly and only shows up in the output.

func renderDashboard(t *testing.T, props DashboardProps) string {
	t.Helper()
	if props.MerchantTZ == nil {
		props.MerchantTZ = time.UTC
	}
	if props.Now.IsZero() {
		props.Now = time.Now()
	}
	var buf bytes.Buffer
	require.NoError(t, DashboardContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func testOrder(number string) domain.Order {
	return domain.Order{
		ID:       uuid.New(),
		Number:   number,
		Total:    2500,
		PlacedAt: time.Now().Add(-2 * time.Hour),
	}
}

// A queue link that drops staff on an unfiltered list makes them redo the
// triage the dashboard just did. Both of these query strings are the ones the
// orders list actually reads — it keys off ?view=, and ignores ?status=
// entirely, so an on-hold link written as ?status=on_hold silently lands on
// the default tab.
func TestDashboard_UrgentLinksCarryTheirFilter(t *testing.T) {
	html := renderDashboard(t, DashboardProps{
		OnHold:             []domain.Order{testOrder("RR-1001")},
		OnHoldCount:        3,
		FailedPayments:     []domain.Order{testOrder("RR-1002")},
		FailedPaymentCount: 2,
	})

	assert.Contains(t, html, "/admin/orders?view=on_hold")
	assert.Contains(t, html, "/admin/orders?view=all&amp;payment=failed")
	assert.NotContains(t, html, "/admin/orders?status=on_hold")
}

// The count in a band header is the true total; the rows below it are a display
// sample. They are allowed to disagree, and the header is the one that has to
// be right — that is the number staff act on.
func TestDashboard_CountsAreIndependentOfDisplayedRows(t *testing.T) {
	html := renderDashboard(t, DashboardProps{
		OnHold:      []domain.Order{testOrder("RR-1001")},
		OnHoldCount: 30,
	})

	assert.Contains(t, html, "30 orders on hold")
	assert.Contains(t, html, "RR-1001")
}

// A label job that gave up is not retried by anything. Without its own group
// the order looks like ordinary Ship work and sits forever, so it has to reach
// the Urgent band and count toward the urgent chip.
func TestDashboard_LabelFailuresSurfaceAsUrgent(t *testing.T) {
	props := DashboardProps{
		LabelFailures:     []domain.Order{testOrder("RR-2001")},
		LabelFailureCount: 2,
	}
	html := renderDashboard(t, props)

	assert.Equal(t, 2, props.urgentCount())
	assert.Contains(t, html, "2 shipping labels failed")
	assert.Contains(t, html, "Label failed")
	assert.Contains(t, html, "RR-2001")
	assert.Contains(t, html, "/admin/fulfillment?view=ready_to_ship")

	// The command bar's urgent chip is how staff notice without scrolling.
	assert.Contains(t, html, `href="#band-urgent"`)
}

// With no work at all the page says so rather than showing empty bands.
func TestDashboard_EmptyStateWhenNothingNeedsAction(t *testing.T) {
	html := renderDashboard(t, DashboardProps{})

	assert.Contains(t, html, "All caught up.")
	assert.NotContains(t, html, "band-urgent")
}

func TestDashboard_DeliveryStripOmittedWithoutSchedule(t *testing.T) {
	html := renderDashboard(t, DashboardProps{})
	assert.NotContains(t, html, "Next delivery run")
}

func TestDashboard_DeliveryStripShowsRunAndLoad(t *testing.T) {
	html := renderDashboard(t, DashboardProps{
		DeliveryRun: &DeliveryRunProps{
			DateLabel:   "Thursday, August 22",
			CutoffLabel: "9am",
			CutoffAt:    time.Now().Add(90 * time.Minute),
			OrderCount:  14,
			IsToday:     true,
		},
	})

	assert.Contains(t, html, "Next delivery run")
	assert.Contains(t, html, "Thursday, August 22")
	assert.Contains(t, html, ">14<")
	assert.Contains(t, html, "/admin/fulfillment/load-list")
}

// Inside three hours of a same-day cutoff the strip earns the rust stamp; a run
// two days out must stay quiet or staff learn to ignore the treatment.
func TestDeliveryRunProps_UrgentOnlyNearSameDayCutoff(t *testing.T) {
	soon := DeliveryRunProps{CutoffAt: time.Now().Add(45 * time.Minute), IsToday: true}
	assert.True(t, soon.urgent())

	laterToday := DeliveryRunProps{CutoffAt: time.Now().Add(6 * time.Hour), IsToday: true}
	assert.False(t, laterToday.urgent())

	nextWeek := DeliveryRunProps{CutoffAt: time.Now().Add(72 * time.Hour), IsToday: false}
	assert.False(t, nextWeek.urgent())

	assert.Contains(t, renderDashboard(t, DashboardProps{DeliveryRun: &soon}), "var(--color-rr-red)")
	assert.NotContains(t, renderDashboard(t, DashboardProps{DeliveryRun: &nextWeek}), "var(--color-rr-red)")
}

// A countdown is only meaningful for today's run. Anything further out reads as
// a plain cutoff time — "in 68h" is not how anyone thinks about next Monday.
//
// The offsets carry a spare 30s so the assertion is testing the floor, not
// racing the clock between constructing the props and reading them back.
func TestDeliveryRunProps_Countdown(t *testing.T) {
	today := DeliveryRunProps{CutoffLabel: "9am", CutoffAt: time.Now().Add(2*time.Hour + 31*time.Minute + 30*time.Second), IsToday: true}
	assert.Equal(t, "cutoff 9am · in 2h 31m", today.countdown())

	soon := DeliveryRunProps{CutoffLabel: "9am", CutoffAt: time.Now().Add(20*time.Minute + 30*time.Second), IsToday: true}
	assert.Equal(t, "cutoff 9am · in 20m", soon.countdown())

	future := DeliveryRunProps{CutoffLabel: "9am", CutoffAt: time.Now().Add(72 * time.Hour)}
	assert.Equal(t, "cutoff 9am", future.countdown())
}

// A dead background job is the failure this dashboard is structurally worst at
// showing: the symptom is an absence. It has to reach Urgent, count toward the
// chip, and say which kinds broke — a bare number sends staff hunting.
func TestDashboard_DeadJobsSurfaceWithKindBreakdown(t *testing.T) {
	props := DashboardProps{
		DeadJobCount: 5,
		DeadJobKinds: []domain.DeadJobKindCount{
			{Kind: "email_order_confirm", Count: 4},
			{Kind: "qb_create_invoice", Count: 1},
		},
	}
	html := renderDashboard(t, props)

	assert.Equal(t, 5, props.urgentCount())
	assert.Contains(t, html, "5 background jobs failed")
	assert.Contains(t, html, "4 email_order_confirm, 1 qb_create_invoice")
	assert.Contains(t, html, "/admin/jobs")
}

// The summary is one line, not the list page: past two kinds it counts the rest
// rather than running off the row.
func TestDashboardProps_DeadJobSummary(t *testing.T) {
	two := DashboardProps{DeadJobKinds: []domain.DeadJobKindCount{
		{Kind: "a", Count: 2}, {Kind: "b", Count: 1},
	}}
	assert.Equal(t, "2 a, 1 b — nothing will retry these on its own.", two.deadJobSummary())

	four := DashboardProps{DeadJobKinds: []domain.DeadJobKindCount{
		{Kind: "a", Count: 4}, {Kind: "b", Count: 3}, {Kind: "c", Count: 2}, {Kind: "d", Count: 1},
	}}
	assert.Equal(t, "4 a, 3 b, and 2 more kinds — nothing will retry these on its own.", four.deadJobSummary())

	one := DashboardProps{DeadJobKinds: []domain.DeadJobKindCount{{Kind: "a", Count: 1}}}
	assert.Equal(t, "1 a — nothing will retry these on its own.", one.deadJobSummary())

	// Count with no rollup still reads as a sentence rather than a bare dash.
	assert.Equal(t, "Nothing will retry these on its own — they need a look.", DashboardProps{}.deadJobSummary())
}

// The channel split has to survive the round trip through the template, not
// just hold in the predicate: a wholesale order mid-cycle and a retail order of
// the same age must not be painted the same.
func TestDashboard_StaleFlagIsPerChannel(t *testing.T) {
	threeDaysAgo := time.Now().Add(-3 * 24 * time.Hour)

	retail := testOrder("RR-3001")
	retail.Channel = domain.OrderChannelRetail
	retail.Status = domain.OrderStatusConfirmed
	retail.PlacedAt = threeDaysAgo

	wholesale := testOrder("RR-3002")
	wholesale.Channel = domain.OrderChannelWholesale
	wholesale.Status = domain.OrderStatusConfirmed
	wholesale.PlacedAt = threeDaysAgo

	html := renderDashboard(t, DashboardProps{
		RetailCount:    1,
		RetailRows:     []PipelineRow{{Order: retail, Stage: "Pack", ItemTitle: "Bike Blend", Quantity: 1, CustomerName: "A"}},
		WholesaleCount: 1,
		WholesaleRows:  []PipelineRow{{Order: wholesale, Stage: "Pack", ItemTitle: "Bike Blend", Quantity: 1, CustomerName: "B"}},
	})

	// Both rows show "3d ago"; only the retail one wears rust. The desktop and
	// mobile layouts each render the age, hence two occurrences apiece.
	assert.Equal(t, 2, strings.Count(html, `<span class="label-font text-rr-red text-right" style="font-size:0.6rem;">3d ago</span>`)+
		strings.Count(html, `<span class="label-font text-rr-red" style="font-size:0.6rem;">3d ago</span>`),
		"exactly one row (in both its layouts) should be flagged rust")
	assert.Equal(t, 4, strings.Count(html, "3d ago"), "both rows show their age")
}
