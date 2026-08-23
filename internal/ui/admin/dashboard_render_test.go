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
		OnHold: []UrgentOrderGroup{{
			Channel: domain.OrderChannelRetail,
			Count:   3,
			Orders:  []domain.Order{testOrder("RR-1001")},
		}},
		FailedPayments: []UrgentOrderGroup{{
			Channel: domain.OrderChannelRetail,
			Count:   2,
			Orders:  []domain.Order{testOrder("RR-1002")},
		}},
	})

	assert.Contains(t, html, "/admin/orders?view=on_hold")
	assert.Contains(t, html, "/admin/orders?view=all&amp;payment=failed")
	assert.NotContains(t, html, "/admin/orders?status=on_hold")
}

// Orders and Fulfillment are channel-split pages, so an urgent group has to be
// too. A combined "5 orders on hold" linking to the retail list would send
// staff somewhere that shows three of them — the same broken promise as a count
// that overstates, just one click further in.
func TestDashboard_UrgentGroupsSplitByChannel(t *testing.T) {
	props := DashboardProps{
		OnHold: []UrgentOrderGroup{
			{Channel: domain.OrderChannelRetail, Count: 3, Orders: []domain.Order{testOrder("RR-1001")}},
			{Channel: domain.OrderChannelWholesale, Count: 2, Orders: []domain.Order{testOrder("RR-1002")}},
		},
	}
	html := renderDashboard(t, props)

	// Each channel states its own count and links to its own list.
	assert.Contains(t, html, "3 retail orders on hold")
	assert.Contains(t, html, `href="/admin/orders?view=on_hold"`)
	assert.Contains(t, html, "2 wholesale orders on hold")
	assert.Contains(t, html, `href="/admin/orders/wholesale?view=on_hold"`)

	// The band header still totals both — the split is in the links, not the
	// arithmetic.
	assert.Equal(t, 5, props.urgentCount())
}

// Failed payments and stuck labels take the same treatment, each landing on its
// own channel's page. The label group aims at needs_action because that is the
// bucket the store query mirrors; ready_to_ship would filter out every stuck
// order that has not been packed yet.
func TestDashboard_WholesaleUrgentGroupsLinkToWholesalePages(t *testing.T) {
	html := renderDashboard(t, DashboardProps{
		FailedPayments: []UrgentOrderGroup{{
			Channel: domain.OrderChannelWholesale, Count: 1, Orders: []domain.Order{testOrder("RR-2002")},
		}},
		LabelFailures: []UrgentOrderGroup{{
			Channel: domain.OrderChannelWholesale, Count: 1, Orders: []domain.Order{testOrder("RR-2003")},
		}},
	})

	assert.Contains(t, html, `href="/admin/orders/wholesale?view=all&amp;payment=failed"`)
	assert.Contains(t, html, `href="/admin/wholesale/fulfillment?view=needs_action"`)
	assert.NotContains(t, html, "view=ready_to_ship")
}

// A channel with nothing stuck must not take up a line saying so.
func TestDashboard_EmptyChannelGroupRendersNothing(t *testing.T) {
	html := renderDashboard(t, DashboardProps{
		OnHold: []UrgentOrderGroup{
			{Channel: domain.OrderChannelRetail, Count: 2, Orders: []domain.Order{testOrder("RR-1001")}},
			{Channel: domain.OrderChannelWholesale, Count: 0},
		},
	})

	assert.Contains(t, html, "2 retail orders on hold")
	assert.NotContains(t, html, "wholesale order")
}

// The count in a band header is the true total; the rows below it are a display
// sample. They are allowed to disagree, and the header is the one that has to
// be right — that is the number staff act on.
func TestDashboard_CountsAreIndependentOfDisplayedRows(t *testing.T) {
	html := renderDashboard(t, DashboardProps{
		OnHold: []UrgentOrderGroup{{
			Channel: domain.OrderChannelRetail,
			Count:   30,
			Orders:  []domain.Order{testOrder("RR-1001")},
		}},
	})

	assert.Contains(t, html, "30 retail orders on hold")
	assert.Contains(t, html, "RR-1001")
}

// A label job that gave up is not retried by anything. Without its own group
// the order looks like ordinary Ship work and sits forever, so it has to reach
// the Urgent band and count toward the urgent chip.
func TestDashboard_LabelFailuresSurfaceAsUrgent(t *testing.T) {
	props := DashboardProps{
		LabelFailures: []UrgentOrderGroup{{
			Channel: domain.OrderChannelRetail,
			Count:   2,
			Orders:  []domain.Order{testOrder("RR-2001")},
		}},
	}
	html := renderDashboard(t, props)

	assert.Equal(t, 2, props.urgentCount())
	assert.Contains(t, html, "2 retail shipping labels failed")
	assert.Contains(t, html, "Label failed")
	assert.Contains(t, html, "RR-2001")
	// needs_action, not ready_to_ship: the store query mirrors that bucket, and
	// ready_to_ship (fulfillment_status = 'fulfilled') would hide every stuck
	// order still waiting to be packed.
	assert.Contains(t, html, "/admin/fulfillment?view=needs_action")

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
