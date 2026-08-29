package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// RenewalBlocked is the named form of a contradiction that cost three customers
// months of unbilled subscriptions: a status that says the subscription is live
// beside an ends_at that stops the renewal scheduler ever selecting it. The
// admin banner and the alerting gauge both read this, so the boundaries matter.
func TestSubscriptionRenewalBlocked(t *testing.T) {
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)

	tests := []struct {
		name   string
		status SubscriptionStatus
		endsAt *time.Time
		want   bool
		why    string
	}{
		{
			name: "active with a past end date is blocked", status: SubscriptionStatusActive,
			endsAt: &past, want: true,
			why: "the exact state Jon Law's subscription sat in for two months",
		},
		{
			name: "past_due with a past end date is blocked", status: SubscriptionStatusPastDue,
			endsAt: &past, want: true,
			why: "the scheduler picks up past_due for dunning retries, so it can be stranded the same way",
		},
		{
			name: "active with no end date is fine", status: SubscriptionStatusActive,
			endsAt: nil, want: false,
			why: "the overwhelmingly common case must not light up the banner",
		},
		{
			name: "active with a future end date is fine", status: SubscriptionStatusActive,
			endsAt: &future, want: false,
			why: "a fixed-term subscription still renewing towards its end is legitimate, not broken",
		},
		{
			name: "expired with a past end date is fine", status: SubscriptionStatusExpired,
			endsAt: &past, want: false,
			why: "status and ends_at agree; nothing is contradictory and nothing is owed",
		},
		{
			name: "cancelled with a past end date is fine", status: SubscriptionStatusCancelled,
			endsAt: &past, want: false,
			why: "a cancelled subscription is supposed to stop billing",
		},
		{
			name: "paused with a past end date is fine", status: SubscriptionStatusPaused,
			endsAt: &past, want: false,
			why: "the scheduler never selects paused, so ends_at changes nothing about its billing",
		},
		{
			name: "end date exactly now is blocked", status: SubscriptionStatusActive,
			endsAt: &now, want: true,
			why: "the scheduler's clause is ends_at > now, so equality excludes — this must match it",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sub := Subscription{Status: tc.status, EndsAt: tc.endsAt}
			assert.Equal(t, tc.want, sub.RenewalBlocked(now), tc.why)
		})
	}
}

// CountsAsLive has to mirror the status clause of ListSubscriptionsDueForRenewal
// exactly. If it ever drifts, RenewalBlocked stops describing the query it is
// meant to describe and the banner starts lying in one direction or the other.
func TestSubscriptionStatusCountsAsLive(t *testing.T) {
	live := map[SubscriptionStatus]bool{
		SubscriptionStatusActive:    true,
		SubscriptionStatusPastDue:   true,
		SubscriptionStatusPaused:    false,
		SubscriptionStatusCancelled: false,
		SubscriptionStatusExpired:   false,
	}
	for status, want := range live {
		assert.Equal(t, want, status.CountsAsLive(), "status %q", status)
	}
}
