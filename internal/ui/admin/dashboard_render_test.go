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

// A bag marked ready and never collected has nothing ageing it. The fulfillment
// queue's needs-action tab does list ready_for_pickup — it has since b06ae7d —
// but beside every other order awaiting action and with no sense of how long,
// so one on the shelf a fortnight reads like one boxed this morning. This row
// is the ageing, and the only thing that singles those out.
func TestDashboard_UncollectedPickupsReachTheReviewBand(t *testing.T) {
	now := time.Now()
	props := DashboardProps{
		Now:                 now,
		WaitingPickupCount:  2,
		PickupStaleDays:     3,
		OldestPickupReadyAt: now.Add(-11 * 24 * time.Hour),
	}
	html := renderDashboard(t, props)

	assert.Equal(t, 2, props.reviewCount())
	assert.Contains(t, html, "2 pickup orders waiting over 3 days")
	assert.Contains(t, html, "Oldest was ready 11 days ago")

	// The link has to carry the filter, on the "all" view because the count is
	// not status-scoped the way the needs-action view is.
	assert.Contains(t, html, "/admin/orders?view=all&amp;fulfillment=ready_for_pickup")
	assert.Contains(t, html, `href="#band-review"`)
}

// Wait time is stated in days because that is what makes someone act. Under a
// day there is no number worth quoting, and a zero value must not render as a
// nonsense age.
func TestPickupWaitDetail(t *testing.T) {
	now := time.Now()

	assert.Contains(t, pickupWaitDetail(now.Add(-11*24*time.Hour), now), "ready 11 days ago")
	assert.Contains(t, pickupWaitDetail(now.Add(-25*time.Hour), now), "ready 1 day ago")

	// No usable timestamp: say the thing that matters, skip the arithmetic.
	assert.NotContains(t, pickupWaitDetail(time.Time{}, now), "ago")
	assert.NotContains(t, pickupWaitDetail(now.Add(-3*time.Hour), now), "ago")
}

// The failure mode this row exists for: a worker discarding jobs makes the rest
// of the dashboard look calmer, not busier. So it has to lead the Urgent band
// and count toward the chip, even though no order row is involved.
func TestDashboard_DeadJobsLeadTheUrgentBand(t *testing.T) {
	props := DashboardProps{
		DeadJobCount: 7,
		DeadJobKinds: []domain.DeadJobKindCount{
			{Kind: "renew_subscription", Count: 5},
			{Kind: "email_order_confirm", Count: 2},
		},
	}
	html := renderDashboard(t, props)

	assert.Equal(t, 7, props.urgentCount())
	assert.Contains(t, html, "7 background jobs failed")
	assert.Contains(t, html, "renew_subscription ×5")
	assert.Contains(t, html, `href="/admin/jobs"`)

	// It precedes the order-shaped groups: the automation failure is usually
	// the reason those queues look the way they do.
	jobs := strings.Index(html, "background jobs failed")
	band := strings.Index(html, "band-urgent")
	assert.Greater(t, jobs, band)
}

// The kinds are the diagnosis, so they get named — but not unboundedly, and a
// missing breakdown must still say something useful.
func TestDeadJobDetail(t *testing.T) {
	assert.Contains(t, deadJobDetail(nil), "nothing retries them")

	five := []domain.DeadJobKindCount{
		{Kind: "a", Count: 1}, {Kind: "b", Count: 2}, {Kind: "c", Count: 3},
		{Kind: "d", Count: 4}, {Kind: "e", Count: 5},
	}
	got := deadJobDetail(five)
	assert.Contains(t, got, "a ×1 · b ×2 · c ×3 · +2 more")
	assert.NotContains(t, got, "d ×4")

	// Exactly three fits with no overflow marker.
	assert.NotContains(t, deadJobDetail(five[:3]), "more")
}

func renderForecast(t *testing.T, props RenewalForecastProps) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, RenewalForecastCard(props).Render(context.Background(), &buf))
	return buf.String()
}

// The headline is pounds because that is what a roaster plans around; the
// per-coffee split is what goes on the schedule.
func TestRenewalForecastCard_TotalsAndBreakdown(t *testing.T) {
	props := RenewalForecastProps{
		Days: 7,
		Lines: []domain.RenewalForecastLine{
			// 4536g ≈ 10.0 lb, 2268g ≈ 5.0 lb
			{Title: "Chop Top", Subscriptions: 6, Units: 8, WeightGrams: 4536},
			{Title: "Hot Rod", Subscriptions: 3, Units: 4, WeightGrams: 2268},
		},
	}
	html := renderForecast(t, props)

	assert.Equal(t, 6804, props.totalGrams())
	assert.Equal(t, 9, props.totalSubscriptions())

	assert.Contains(t, html, "Roast forecast · next 7 days")
	assert.Contains(t, html, "15.0") // total pounds
	assert.Contains(t, html, "across 9 renewals")
	assert.Contains(t, html, "Chop Top")
	assert.Contains(t, html, "8 bags · 6 subscriptions")
	assert.Contains(t, html, "10.0")

	assert.Contains(t, html, `id="renewal-forecast-card"`)
	assert.Contains(t, html, `hx-target="#renewal-forecast-card"`)
	assert.Contains(t, html, "/admin/dashboard/renewals?days=30")

	// The exclusion has to be stated or the number silently means something
	// other than what a reader assumes.
	assert.Contains(t, html, "Active subscriptions only")
}

// A weightless variant contributes units but no grams, so the poundage is an
// undercount. Saying nothing would send someone to the roaster short.
func TestRenewalForecastCard_FlagsMissingWeights(t *testing.T) {
	withGap := RenewalForecastProps{
		Days:  7,
		Lines: []domain.RenewalForecastLine{{Title: "Mystery", Subscriptions: 2, Units: 3, WeightGrams: 0, UnitsMissingWeight: 3}},
	}
	assert.Equal(t, 3, withGap.missingWeightUnits())
	assert.Contains(t, renderForecast(t, withGap), "3 bags have no weight set")

	clean := RenewalForecastProps{
		Days:  7,
		Lines: []domain.RenewalForecastLine{{Title: "Chop Top", Subscriptions: 1, Units: 1, WeightGrams: 4536}},
	}
	assert.Equal(t, 0, clean.missingWeightUnits())
	assert.NotContains(t, renderForecast(t, clean), "no weight set")
}

// An empty window says so rather than rendering a 0.0 lb headline, which reads
// like a broken query.
func TestRenewalForecastCard_EmptyWindow(t *testing.T) {
	html := renderForecast(t, RenewalForecastProps{Days: 14})

	assert.Contains(t, html, "No subscription renewals due in this window")
	assert.NotContains(t, html, "across 0 renewals")
	// The toggle stays usable so staff can widen the window.
	assert.Contains(t, html, "/admin/dashboard/renewals?days=30")
}

// The forecast is a plan, so it sits above the trend charts but below the day's
// triage queues.
func TestDashboard_ForecastSitsAboveAnalytics(t *testing.T) {
	html := renderDashboard(t, DashboardProps{
		Renewals: RenewalForecastProps{
			Days:  7,
			Lines: []domain.RenewalForecastLine{{Title: "Chop Top", Subscriptions: 1, Units: 1, WeightGrams: 4536}},
		},
	})

	comingUp := strings.Index(html, "Coming up")
	analytics := strings.Index(html, ">Analytics<")
	assert.Greater(t, comingUp, 0)
	assert.Greater(t, analytics, comingUp)
}
