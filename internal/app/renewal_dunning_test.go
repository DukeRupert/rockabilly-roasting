package app

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/payments"
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

// TestReleaseDunningHardDeclineLatch is the in-memory half of releasing the
// latch, and it exists because getting it wrong is silent and severe.
//
// RenewSubscription drops the latch in the database and then keeps using the
// same struct. recordRenewalFailure reads the latch back off that struct to
// decide whether a decline is permanent, so a stale true there means the
// customer's *replacement* card gets branded dead on an ordinary soft decline —
// insufficient funds recorded as "your bank blocked this card for good", every
// remaining charge attempt skipped, and the subscription expiring anyway. That
// is the original bug wearing a different hat, one step further along.
func TestReleaseDunningHardDeclineLatch(t *testing.T) {
	sub := &domain.Subscription{Metadata: map[string]any{
		domain.SubscriptionMetaDunningAttempt:           float64(2),
		domain.SubscriptionMetaDunningHardDecline:       true,
		domain.SubscriptionMetaDunningDeclineCode:       "lost_card",
		domain.SubscriptionMetaDunningDeadPaymentMethod: "pm_dead",
		domain.SubscriptionMetaShippingGrandfathered:    true,
	}}

	sub.ReleaseDunningHardDeclineLatch()

	assert.False(t, sub.DunningHardDeclined(), "latch must be gone in memory, not just in the row")
	assert.False(t, sub.DunningChargeBlocked("pm_new"), "a released latch must not block the new card")

	// The dead card's record survives the release — releasing the latch says
	// "charge the new card", not "forget which card was dead".
	assert.Equal(t, "pm_dead", sub.DunningDeadPaymentMethod())
	assert.True(t, sub.DunningChargeBlocked("pm_dead"), "the dead card stays refused")

	// The ladder and everything unrelated survive: releasing the latch is not a
	// reset, and a customer does not lose free shipping by fixing their card.
	assert.Equal(t, 2, sub.DunningAttempt())
	assert.True(t, sub.ShippingGrandfathered())

	// Safe on a subscription that never had metadata.
	assert.NotPanics(t, func() { (&domain.Subscription{}).ReleaseDunningHardDeclineLatch() })
}

// applyVerdict mutates sub the way recordRenewalFailure's database writes do, so
// a test can run several rungs in sequence against one subscription. It mirrors
// SetDunningRetry + SetDunningHardDecline; keeping it this small is the point,
// since anything more would be re-implementing the thing under test.
func applyVerdict(sub *domain.Subscription, v dunningVerdict) {
	if sub.Metadata == nil {
		sub.Metadata = map[string]any{}
	}
	sub.Metadata[domain.SubscriptionMetaDunningAttempt] = float64(v.attempt)
	if v.hardDecline {
		sub.Metadata[domain.SubscriptionMetaDunningHardDecline] = true
		sub.Metadata[domain.SubscriptionMetaDunningDeclineCode] = v.declineCode
		sub.Metadata[domain.SubscriptionMetaDunningDeadPaymentMethod] = v.deadPM
	}
}

// TestDunningStoryDeadCardThenReplacement walks one customer's whole story, rung
// by rung, because every bug this feature has shipped lived in the sequence
// rather than in any single step.
//
// Three separate review rounds caught three variants of the same defect — the
// latch could not be released; the release was undone in memory; the release
// erased the record the release depended on — and all three survived a suite
// that tested each piece on its own. This is the test that would have caught
// every one of them.
func TestDunningStoryDeadCardThenReplacement(t *testing.T) {
	sub := &domain.Subscription{}
	hard := &payments.DeclineError{Code: "card_declined", DeclineCode: "lost_card"}
	soft := &payments.DeclineError{Code: "card_declined", DeclineCode: "insufficient_funds"}

	// Rung 1 — the original card is reported lost.
	v := dunningVerdictFor(sub, "pm_dead", hard)
	require.Equal(t, 1, v.attempt)
	require.True(t, v.hardDecline)
	assert.Equal(t, "pm_dead", v.deadPM)
	assert.Equal(t, "lost_card", v.declineCode)
	assert.Equal(t, dunningEmailFirst, v.emailStage, "the customer is told immediately")
	applyVerdict(sub, v)

	// Charging is stopped, and stopped specifically for that card.
	require.True(t, sub.DunningChargeBlocked("pm_dead"))
	require.True(t, sub.DunningHasDeadCard(), "must be routed to a solo renewal, not a batch")

	// Rung 2 — nothing to charge, so the rung only advances the clock. The
	// verdict must not read the absence of a decline as an all-clear.
	v = dunningVerdictFor(sub, "pm_dead", nil)
	require.True(t, v.hardDecline, "a rung that does not charge cannot clear the verdict")
	assert.Equal(t, "lost_card", v.declineCode, "the issuer's reason survives a silent rung")
	assert.Equal(t, "pm_dead", v.deadPM)
	assert.Equal(t, dunningEmailSilent, v.emailStage)
	applyVerdict(sub, v)

	// The customer adds a card. It does not become the Stripe default, so the
	// picker has to skip the dead one to find it.
	require.False(t, sub.DunningChargeBlocked("pm_new"), "the replacement card is chargeable")
	sub.ReleaseDunningHardDeclineLatch()

	// Rung 3 — the replacement card declines for an ordinary reason. This must
	// NOT be recorded as permanent, or the customer who did what we asked gets
	// told their new card is dead too.
	v = dunningVerdictFor(sub, "pm_new", soft)
	require.False(t, v.hardDecline, "a soft decline on the replacement must not re-latch")
	assert.Equal(t, "pm_dead", v.deadPM, "the dead card is still remembered")
	assert.Equal(t, "insufficient_funds", v.declineCode)
	applyVerdict(sub, v)

	// Rung 4 — the state that broke three times over. The replacement card is
	// still retryable, and the dead card is still refused even though no latch
	// is set. Routing must still keep this off the batch path, which has no way
	// to know which card to avoid.
	assert.False(t, sub.DunningChargeBlocked("pm_new"), "the replacement keeps its turn")
	assert.True(t, sub.DunningChargeBlocked("pm_dead"),
		"a card the issuer killed is never charged again, latch or no latch")
	assert.True(t, sub.DunningHasDeadCard(), "still routed solo after the release")

	// And if the replacement card is removed, the picker falls back to the dead
	// card — we refuse it, and re-latching is what keeps the admin and the
	// customer email from promising a charge that will not happen.
	sub.LatchDunningHardDeclineMeta()
	assert.True(t, sub.DunningHardDeclined())
}

// TestDunningVerdictExpiry pins the end of the ladder, including that the final
// verdict still carries the card verdict forward — the "subscription ended"
// audit record is the last thing that can say why.
func TestDunningVerdictExpiry(t *testing.T) {
	sub := &domain.Subscription{Metadata: map[string]any{
		domain.SubscriptionMetaDunningAttempt:           float64(MaxDunningAttempts - 1),
		domain.SubscriptionMetaDunningHardDecline:       true,
		domain.SubscriptionMetaDunningDeclineCode:       "stolen_card",
		domain.SubscriptionMetaDunningDeadPaymentMethod: "pm_dead",
	}}

	v := dunningVerdictFor(sub, "pm_dead", nil)
	assert.True(t, v.expire)
	assert.Equal(t, MaxDunningAttempts, v.attempt)
	assert.True(t, v.hardDecline)
	assert.Equal(t, "stolen_card", v.declineCode)
	assert.Zero(t, v.wait, "an expiring verdict schedules nothing")
}

// TestDunningVerdictSoftDeclineNeverNamesADeadCard: only a permanent decline
// records a dead card. A soft decline naming one would freeze a perfectly good
// card out of every future charge.
func TestDunningVerdictSoftDeclineNeverNamesADeadCard(t *testing.T) {
	v := dunningVerdictFor(&domain.Subscription{}, "pm_fine",
		&payments.DeclineError{DeclineCode: "do_not_honor"})
	assert.False(t, v.hardDecline)
	assert.Empty(t, v.deadPM)
	assert.Equal(t, "do_not_honor", v.declineCode)

	// A non-card error (an outage, a network blip) says nothing about the card
	// at all and must not advance any verdict beyond the attempt count.
	v = dunningVerdictFor(&domain.Subscription{}, "pm_fine", errors.New("connection reset"))
	assert.False(t, v.hardDecline)
	assert.Empty(t, v.deadPM)
	assert.Empty(t, v.declineCode)
}
