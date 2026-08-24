package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// The dunning ladder is pure data, so these tests need no database. What they
// guard is the shape of the schedule — the thing that decides whether a
// customer whose card bounced ever hears from us in time.

// TestDunningLadderShape pins the schedule against the reasoning that produced
// it: roughly two weeks of runway, escalating gaps, and three customer emails
// with one deliberately silent rung.
func TestDunningLadderShape(t *testing.T) {
	require.Len(t, dunningLadder, 4, "five attempts total — see MaxDunningAttempts")
	assert.Equal(t, 5, MaxDunningAttempts)
	assert.Equal(t, domain.SubscriptionMaxDunningAttempts, MaxDunningAttempts,
		"the domain mirror the admin UI reads must agree with the ladder")

	var total time.Duration
	for _, stage := range dunningLadder {
		total += stage.wait
	}
	assert.Equal(t, 14*24*time.Hour, total,
		"the window should span two weeks: clustering retries recovers less than spacing them")

	// Attempt 2 stays quiet. Two emails inside three days reads as dunning spam
	// and costs more in unsubscribes than it recovers.
	assert.Equal(t, dunningEmailFirst, dunningLadder[0].emailStage)
	assert.Equal(t, dunningEmailSilent, dunningLadder[1].emailStage)
	assert.Equal(t, dunningEmailReminder, dunningLadder[2].emailStage)
	assert.Equal(t, dunningEmailFinal, dunningLadder[3].emailStage)

	// The domain mirror the admin UI reads must describe the same silent/notify
	// shape as the ladder. These live apart because ui may only import domain,
	// and a drift would make the admin say "the next reminder goes out <date>"
	// on a rung that sends nothing.
	require.Len(t, domain.SubscriptionDunningRungNotifies, len(dunningLadder))
	for i, stage := range dunningLadder {
		assert.Equalf(t, stage.emailStage != dunningEmailSilent, domain.SubscriptionDunningRungNotifies[i],
			"rung %d: ladder and domain disagree about whether it notifies", i)
	}

	// Five attempts must stay inside the tighter card-network ceiling
	// (Mastercard: 10 retries per 30 days). Blowing through it is fined per
	// attempt.
	assert.LessOrEqual(t, MaxDunningAttempts, 10)

	// Every non-silent rung must have copy to send, or the ladder would enqueue
	// a job for a template that does not exist.
	for i, stage := range dunningLadder {
		if stage.emailStage == dunningEmailSilent {
			continue
		}
		_, ok := pastDueStageSpecs[stage.emailStage]
		assert.Truef(t, ok, "rung %d sends stage %d, which has no template", i+1, stage.emailStage)
	}
}

// TestDunningExpiresAt covers the date the customer-facing emails promise. It
// has to be right: the final notice names it, and a wrong date either scares
// someone whose subscription is fine or fails to warn someone whose isn't.
func TestDunningExpiresAt(t *testing.T) {
	day0 := time.Date(2026, 8, 3, 2, 0, 0, 0, time.UTC)

	sub := func(attempt int, nextOrderAt time.Time) *domain.Subscription {
		return &domain.Subscription{
			Metadata:    map[string]any{domain.SubscriptionMetaDunningAttempt: float64(attempt)},
			NextOrderAt: nextOrderAt,
		}
	}

	// After attempt 1 fails on day 0, the next attempt is day 3 and the
	// remaining rungs (5d + 4d + 2d) carry it to day 14.
	assert.Equal(t, day0.AddDate(0, 0, 14), DunningExpiresAt(sub(1, day0.AddDate(0, 0, 3))))
	// After attempt 3 fails on day 8, the next attempt is day 12 and one 2-day
	// rung remains.
	assert.Equal(t, day0.AddDate(0, 0, 14), DunningExpiresAt(sub(3, day0.AddDate(0, 0, 12))))
	// On the last rung, next_order_at already *is* the expiry.
	assert.Equal(t, day0.AddDate(0, 0, 14), DunningExpiresAt(sub(4, day0.AddDate(0, 0, 14))))

	// A subscription that has never failed has no projected expiry to give, so
	// it reports its own next order date rather than inventing a deadline.
	never := &domain.Subscription{NextOrderAt: day0.AddDate(0, 0, 30)}
	assert.Equal(t, day0.AddDate(0, 0, 30), DunningExpiresAt(never))

	// Unreadable metadata must not produce a nonsense date either.
	junk := &domain.Subscription{
		Metadata:    map[string]any{domain.SubscriptionMetaDunningAttempt: "not a number"},
		NextOrderAt: day0,
	}
	assert.Equal(t, day0, DunningExpiresAt(junk))
}

// TestPastDueStageSpecsFallback covers the stage-to-template mapping, including
// the zero value. Jobs enqueued before the ladder existed carry Stage 0, and
// those must still send something sensible rather than failing to render.
func TestPastDueStageSpecsFallback(t *testing.T) {
	first := pastDueStageSpecs[dunningEmailFirst]
	require.NotEmpty(t, first.template)

	// Distinct template, subject, and tag per rung — a reused tag would make the
	// three notices indistinguishable in Postmark's per-tag delivery stats.
	seen := map[string]bool{}
	for stage, spec := range pastDueStageSpecs {
		assert.NotEmptyf(t, spec.template, "stage %d has no template", stage)
		assert.NotEmptyf(t, spec.subject, "stage %d has no subject", stage)
		assert.Falsef(t, seen[spec.tag], "stage %d reuses tag %q", stage, spec.tag)
		seen[spec.tag] = true
	}

	// The silent rung must never map to a template — if it did, the "quiet"
	// attempt would start mailing.
	_, ok := pastDueStageSpecs[dunningEmailSilent]
	assert.False(t, ok, "the silent rung must have no template")
}

// TestDunningMetadataAccessors covers the jsonb round-trip. These read values
// that came back through JSON decoding, where numbers arrive as float64 and a
// hand-edited row can hold anything at all — a wrong answer here either stops
// charging a healthy card or keeps charging a dead one.
func TestDunningMetadataAccessors(t *testing.T) {
	t.Run("absent metadata reads as a clean subscription", func(t *testing.T) {
		var sub domain.Subscription
		assert.Equal(t, 0, sub.DunningAttempt())
		assert.False(t, sub.DunningHardDeclined())
		assert.Empty(t, sub.DunningDeclineCode())
	})

	t.Run("jsonb float64 decodes as an attempt count", func(t *testing.T) {
		sub := domain.Subscription{Metadata: map[string]any{
			domain.SubscriptionMetaDunningAttempt: float64(3),
		}}
		assert.Equal(t, 3, sub.DunningAttempt())
	})

	t.Run("hard decline latch and reason", func(t *testing.T) {
		sub := domain.Subscription{Metadata: map[string]any{
			domain.SubscriptionMetaDunningHardDecline: true,
			domain.SubscriptionMetaDunningDeclineCode: "lost_card",
		}}
		assert.True(t, sub.DunningHardDeclined())
		assert.Equal(t, "lost_card", sub.DunningDeclineCode())
	})

	t.Run("garbage reads as not hard-declined", func(t *testing.T) {
		// The safe default is to keep charging: refusing to charge on the
		// strength of an unparseable field would strand a live subscription.
		sub := domain.Subscription{Metadata: map[string]any{
			domain.SubscriptionMetaDunningHardDecline: "true",
			domain.SubscriptionMetaDunningDeclineCode: 42,
		}}
		assert.False(t, sub.DunningHardDeclined())
		assert.Empty(t, sub.DunningDeclineCode())
	})
}

// TestDunningChargeBlocked covers the release mechanism for the hard-decline
// latch — the single most dangerous piece of this feature.
//
// The latch stops us charging a card the issuer killed. But nothing else clears
// it: ClearDunning only runs on a successful charge, and a latched subscription
// never attempts one. So if the latch could not release on its own, every
// hard-declined subscription would be guaranteed to expire regardless of what
// card the customer added — and the one-click card link, the whole point of the
// dunning emails, would be decorative.
func TestDunningChargeBlocked(t *testing.T) {
	latched := func(deadPM string) *domain.Subscription {
		meta := map[string]any{
			domain.SubscriptionMetaDunningAttempt:     float64(1),
			domain.SubscriptionMetaDunningHardDecline: true,
		}
		if deadPM != "" {
			meta[domain.SubscriptionMetaDunningDeadPaymentMethod] = deadPM
		}
		return &domain.Subscription{Metadata: meta}
	}

	t.Run("same card stays blocked", func(t *testing.T) {
		assert.True(t, latched("pm_dead").DunningChargeBlocked("pm_dead"))
	})

	t.Run("different card releases the latch", func(t *testing.T) {
		// This is the path that saves the subscription.
		assert.False(t, latched("pm_dead").DunningChargeBlocked("pm_fresh"))
	})

	t.Run("unknown dead card stays blocked", func(t *testing.T) {
		// We cannot tell old from new, so we do not risk re-charging a dead
		// card. The emails still run and the customer's way out is unchanged.
		assert.True(t, latched("").DunningChargeBlocked("pm_whatever"))
	})

	t.Run("no latch never blocks", func(t *testing.T) {
		soft := &domain.Subscription{Metadata: map[string]any{
			domain.SubscriptionMetaDunningAttempt: float64(2),
		}}
		assert.False(t, soft.DunningChargeBlocked("pm_any"))
		assert.False(t, (&domain.Subscription{}).DunningChargeBlocked(""))
	})

	t.Run("empty resolved card is not blocked here", func(t *testing.T) {
		// "no payment method on file" has its own failure path upstream; this
		// predicate must not swallow it, or that branch becomes unreachable.
		assert.False(t, latched("pm_dead").DunningChargeBlocked(""))
	})
}
