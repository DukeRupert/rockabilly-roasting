package storefront

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

// The account page groups the customer's subscriptions into the boxes they
// ship in, offers whole-box skip and pause, and says when items at one address
// ship on different days and so carry separate shipping charges.

type boxPageFixture struct {
	tz     *time.Location
	home   uuid.UUID
	run    time.Time
	weekly *domain.SubscriptionPlan
	month  *domain.SubscriptionPlan
}

func newBoxPageFixture(t *testing.T) boxPageFixture {
	t.Helper()
	tz, err := time.LoadLocation("America/Denver")
	require.NoError(t, err)
	now := time.Now().In(tz)
	return boxPageFixture{
		tz:     tz,
		home:   uuid.New(),
		run:    time.Date(now.Year(), now.Month(), now.Day(), 2, 0, 0, 0, tz).AddDate(0, 0, 5),
		weekly: &domain.SubscriptionPlan{ID: uuid.New(), Name: "Weekly", Interval: domain.SubscriptionIntervalEvery7Days, IntervalCount: 1},
		month:  &domain.SubscriptionPlan{ID: uuid.New(), Name: "Monthly", Interval: domain.SubscriptionIntervalEvery30Days, IntervalCount: 1},
	}
}

func (f boxPageFixture) row(plan *domain.SubscriptionPlan, at time.Time, status domain.SubscriptionStatus, title string) AccountSubscriptionRow {
	return AccountSubscriptionRow{
		Subscription: domain.Subscription{
			ID: uuid.New(), PlanID: plan.ID, ShippingAddressID: f.home,
			NextOrderAt: at, Status: status, Quantity: 1,
		},
		Plan:    plan,
		Product: &domain.Product{Title: title, Slug: strings.ToLower(title)},
	}
}

func renderAccountSubscriptions(t *testing.T, f boxPageFixture, rows []AccountSubscriptionRow) string {
	t.Helper()
	var buf bytes.Buffer
	props := BuildAccountSubscriptionsProps(&domain.Customer{}, rows, f.tz)
	require.NoError(t, AccountSubscriptionsContent(props).Render(context.Background(), &buf))
	return buf.String()
}

const separateChargesNotice = "These go out on different days — each shipment carries its own shipping charge."

func TestAccountSubscriptions_OneBoxHasNoNotice(t *testing.T) {
	f := newBoxPageFixture(t)
	a := f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee")
	b := f.row(f.month, f.run, domain.SubscriptionStatusActive, "Tea")
	html := renderAccountSubscriptions(t, f, []AccountSubscriptionRow{a, b})

	assert.Equal(t, 1, strings.Count(html, "data-subscription-box="))
	assert.NotContains(t, html, separateChargesNotice)

	// The heading names the ship day and carries both whole-box forms.
	assert.Contains(t, html, "Ships "+f.run.Format("Jan 2, 2006"))
	assert.Contains(t, html, `action="/account/subscriptions/box/skip"`)
	assert.Contains(t, html, `action="/account/subscriptions/box/pause"`)
	assert.Contains(t, html, `value="`+f.home.String()+`"`)
	assert.Contains(t, html, `value="`+f.run.UTC().Format(time.RFC3339Nano)+`"`)
	// Prefilled with the box's day plus the shortest member interval: a week.
	assert.Contains(t, html, `value="`+f.run.AddDate(0, 0, 7).Format("2006-01-02")+`"`)

	// Rows stay individual cards with their own forms.
	assert.Contains(t, html, "/account/subscriptions/"+a.Subscription.ID.String()+"/pause")
	assert.Contains(t, html, "/account/subscriptions/"+b.Subscription.ID.String()+"/pause")
}

func TestAccountSubscriptions_TwoBoxesAtOneAddressSayTheyShipSeparately(t *testing.T) {
	f := newBoxPageFixture(t)

	t.Run("same plan: a skip can line them up", func(t *testing.T) {
		html := renderAccountSubscriptions(t, f, []AccountSubscriptionRow{
			f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee"),
			f.row(f.weekly, f.run.AddDate(0, 0, 3), domain.SubscriptionStatusActive, "Tea"),
		})
		assert.Equal(t, 2, strings.Count(html, "data-subscription-box="))
		assert.Contains(t, html, separateChargesNotice)
		assert.Contains(t, html, "Skip one to line them up")
	})

	t.Run("different plans: no skip lines a weekly up with a monthly", func(t *testing.T) {
		html := renderAccountSubscriptions(t, f, []AccountSubscriptionRow{
			f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee"),
			f.row(f.month, f.run.AddDate(0, 0, 3), domain.SubscriptionStatusActive, "Tea"),
		})
		assert.Contains(t, html, separateChargesNotice)
		assert.NotContains(t, html, "Skip one to line them up")
	})

	t.Run("two runs on one day are two boxes", func(t *testing.T) {
		html := renderAccountSubscriptions(t, f, []AccountSubscriptionRow{
			f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee"),
			f.row(f.weekly, f.run.Add(5*time.Hour), domain.SubscriptionStatusActive, "Tea"),
		})
		assert.Equal(t, 2, strings.Count(html, "data-subscription-box="))
		assert.Contains(t, html, separateChargesNotice)
	})
}

func TestAccountSubscriptions_PausedRowsSitOutsideTheBoxes(t *testing.T) {
	f := newBoxPageFixture(t)
	active := f.row(f.weekly, f.run, domain.SubscriptionStatusActive, "Coffee")
	paused := f.row(f.weekly, f.run, domain.SubscriptionStatusPaused, "Tea")
	html := renderAccountSubscriptions(t, f, []AccountSubscriptionRow{active, paused})

	assert.Equal(t, 1, strings.Count(html, "data-subscription-box="))
	assert.Contains(t, html, "/account/subscriptions/"+paused.Subscription.ID.String()+"/resume")
}
