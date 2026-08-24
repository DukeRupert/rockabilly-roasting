package domain

import (
	"time"

	"github.com/google/uuid"
)

// SubscriptionInterval represents the billing interval for a subscription.
type SubscriptionInterval string

const (
	SubscriptionIntervalEvery2Minutes SubscriptionInterval = "every_2_minutes" // dev/testing only
	SubscriptionIntervalEvery7Days    SubscriptionInterval = "every_7_days"
	SubscriptionIntervalEvery14Days   SubscriptionInterval = "every_14_days"
	SubscriptionIntervalEvery21Days   SubscriptionInterval = "every_21_days"
	SubscriptionIntervalEvery30Days   SubscriptionInterval = "every_30_days"
	SubscriptionIntervalEvery60Days   SubscriptionInterval = "every_60_days"
	SubscriptionIntervalEvery90Days   SubscriptionInterval = "every_90_days"
)

// SubscriptionStatus represents the lifecycle state of a subscription.
type SubscriptionStatus string

const (
	SubscriptionStatusActive    SubscriptionStatus = "active"
	SubscriptionStatusPaused    SubscriptionStatus = "paused"
	SubscriptionStatusPastDue   SubscriptionStatus = "past_due"
	SubscriptionStatusCancelled SubscriptionStatus = "cancelled"
	SubscriptionStatusExpired   SubscriptionStatus = "expired"
)

// SubscriptionPlan defines a recurring delivery cadence (decoupled from products).
type SubscriptionPlan struct {
	ID            uuid.UUID
	Name          string
	Interval      SubscriptionInterval
	IntervalCount int
	DiscountPct   int
	IsActive      bool
	Metadata      map[string]any
}

// Subscription represents an active customer subscription.
type Subscription struct {
	ID                 uuid.UUID
	CustomerID         uuid.UUID
	PlanID             uuid.UUID
	VariantID          uuid.UUID
	Quantity           int
	Status             SubscriptionStatus
	ShippingAddressID      uuid.UUID
	StripePaymentMethodID  *string
	CurrentPeriodStart time.Time
	CurrentPeriodEnd   time.Time
	NextOrderAt        time.Time
	EndsAt             *time.Time
	CancelledAt        *time.Time
	PauseUntil         *time.Time
	Metadata           map[string]any
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// SubscriptionMaxSkipIntervals caps how many upcoming shipments a subscriber
// (or staff on their behalf) can skip in one request. Six keeps a weekly
// subscriber inside a month and a half and a monthly subscriber inside half a
// year — long enough for a trip or a full pantry, short enough that a forgotten
// subscription resurfaces rather than quietly going dormant. Anything longer is
// a pause, not a skip.
const SubscriptionMaxSkipIntervals = 6

// SubscriptionMaxSkipDays caps the date form of a skip: any restart day up to
// 60 days out. Beyond that the customer wants a pause — an open-ended gap is a
// different intent, and we'd rather they tell us so.
const SubscriptionMaxSkipDays = 60

// SubscriptionMetaSkipUndo is the metadata key holding the schedule a skip
// replaced, so a mistaken skip can be put back exactly as it was rather than
// re-derived from a cadence that may since have changed.
const SubscriptionMetaSkipUndo = "skip_undo"

// SkipUndo is the schedule snapshot taken when a subscription was skipped.
// AppliedNextOrderAt records what the skip set next_order_at to: an undo is
// only offered while the subscription still sits on that date, so any later
// change (a renewal, a resume, a plan swap, a second skip) retires the undo on
// its own without anyone having to remember to clear it.
type SkipUndo struct {
	PeriodEnd          time.Time
	NextOrderAt        time.Time
	AppliedNextOrderAt time.Time
}

// Metadata renders the snapshot for the jsonb column. Times are RFC3339 so they
// survive the round-trip through jsonb as strings.
func (u SkipUndo) Metadata() map[string]any {
	return map[string]any{
		"period_end":            u.PeriodEnd.Format(time.RFC3339Nano),
		"next_order_at":         u.NextOrderAt.Format(time.RFC3339Nano),
		"applied_next_order_at": u.AppliedNextOrderAt.Format(time.RFC3339Nano),
	}
}

// SkipUndo reads back the snapshot written by the last skip. ok is false when
// there is none, or when the stored shape is not what we wrote (an old row, or
// hand-edited metadata) — an unreadable snapshot must read as "nothing to
// undo", never as a partially-restored schedule.
func (s *Subscription) SkipUndo() (SkipUndo, bool) {
	if s.Metadata == nil {
		return SkipUndo{}, false
	}
	raw, _ := s.Metadata[SubscriptionMetaSkipUndo].(map[string]any)
	if raw == nil {
		return SkipUndo{}, false
	}
	parse := func(key string) (time.Time, bool) {
		str, _ := raw[key].(string)
		t, err := time.Parse(time.RFC3339Nano, str)
		return t, err == nil
	}
	periodEnd, okEnd := parse("period_end")
	nextOrder, okNext := parse("next_order_at")
	applied, okApplied := parse("applied_next_order_at")
	if !okEnd || !okNext || !okApplied {
		return SkipUndo{}, false
	}
	return SkipUndo{PeriodEnd: periodEnd, NextOrderAt: nextOrder, AppliedNextOrderAt: applied}, true
}

// SubscriptionMetaShippingGrandfathered is the metadata key marking a
// subscription whose renewals keep free shipping (it predates, or was manually
// exempted from, the shipping-on-renewal policy). Migration 054 seeds it on
// pre-existing subscriptions; staff toggle it per subscription thereafter.
const SubscriptionMetaShippingGrandfathered = "shipping_grandfathered"

// ShippingGrandfathered reports whether this subscription's renewals should
// waive the shipping charge. jsonb true round-trips through Metadata as a Go
// bool; any other shape reads as false (charge shipping normally).
func (s *Subscription) ShippingGrandfathered() bool {
	if s.Metadata == nil {
		return false
	}
	v, _ := s.Metadata[SubscriptionMetaShippingGrandfathered].(bool)
	return v
}

// SubscriptionDelta is the net change in the active subscription base on one
// calendar day (merchant timezone): +1 for each subscription created that day,
// -1 for each that was cancelled or expired. Days with no change are omitted —
// the caller carries a running total forward to reconstruct the active count
// over time.
type SubscriptionDelta struct {
	Date time.Time
	Net  int
}

// SubscriptionOrder links a subscription to an order for a billing period.
type SubscriptionOrder struct {
	SubscriptionID uuid.UUID
	OrderID        uuid.UUID
	PeriodStart    time.Time
	PeriodEnd      time.Time
}

// Metadata keys tracking a subscription's dunning state — the automated
// past-due ladder that runs after a renewal charge is declined. The schedule
// itself lives in the app layer; these are just the persisted fields it reads
// and writes, exposed here so the admin UI and email templates can report on
// dunning without importing the ladder.
const (
	// SubscriptionMetaDunningAttempt is the running count of failed charge
	// attempts on the current past-due run.
	SubscriptionMetaDunningAttempt = "dunning_attempt"
	// SubscriptionMetaDunningHardDecline marks a subscription whose card the
	// issuer has permanently blocked. Set, we stop charging but keep emailing.
	SubscriptionMetaDunningHardDecline = "dunning_hard_decline"
	// SubscriptionMetaDunningDeclineCode is the issuer's last stated reason.
	SubscriptionMetaDunningDeclineCode = "dunning_decline_code"
	// SubscriptionMetaDunningDeadPaymentMethods lists every payment method that
	// has come back permanently declined on this past-due run. It is what lets
	// the hard-decline latch release on its own: when the customer puts a card
	// on file that is not in this set, the subscription goes back into the
	// normal charge path. Without it the latch would be a trap — no charge is
	// attempted, so no charge can ever succeed to clear it.
	//
	// A set, not a single card, because a customer can replace a dead card with
	// another dead one. Remembering only the latest meant the previous card
	// looked chargeable again, and the ladder alternated between two cards that
	// could never work — spending every remaining attempt, and every attempt
	// carries a network fine.
	SubscriptionMetaDunningDeadPaymentMethods = "dunning_dead_payment_methods"
	// SubscriptionMetaDunningDeadPaymentMethod is the superseded single-card
	// form of the key above. Read-only, so rows written by an earlier build
	// still refuse the card they recorded.
	SubscriptionMetaDunningDeadPaymentMethod = "dunning_dead_payment_method"
)

// SubscriptionMaxDunningAttempts is how many charge attempts a past-due
// subscription gets before it is given up on and expired. The schedule that
// spaces those attempts lives in the app layer; only the count is shared, so
// the admin UI can render "attempt 3 of 5" without reaching across the layer
// boundary. app asserts at compile time that its ladder agrees with this.
const SubscriptionMaxDunningAttempts = 5

// SubscriptionDunningRungNotifies says, for each rung of the past-due ladder,
// whether the customer is emailed when that rung's charge attempt fails. Index i
// is the rung entered after attempt i+1 fails.
//
// The ladder is not uniform — one rung is deliberately silent, so the rung
// number and the notice number diverge — and the admin UI has to describe what
// actually happens on a given date rather than assume every rung mails. The
// schedule itself lives in the app layer; this mirrors only the notify/silent
// shape, and TestDunningLadderShape fails if the two ever disagree.
var SubscriptionDunningRungNotifies = [SubscriptionMaxDunningAttempts - 1]bool{true, false, true, true}

// DunningAttempt reports how many charge attempts have failed on the current
// past-due run. Zero for a subscription that has never failed. JSON decoding
// yields float64 for numbers, so both float64 and int are tolerated.
func (s *Subscription) DunningAttempt() int {
	if s.Metadata == nil {
		return 0
	}
	switch v := s.Metadata[SubscriptionMetaDunningAttempt].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

// DunningHardDeclined reports whether this subscription's card has been
// permanently blocked by the issuer, meaning no further charge will be
// attempted against it. The customer can still rescue the subscription by
// putting a different card on file.
func (s *Subscription) DunningHardDeclined() bool {
	if s.Metadata == nil {
		return false
	}
	v, _ := s.Metadata[SubscriptionMetaDunningHardDecline].(bool)
	return v
}

// ReleaseDunningHardDeclineLatch drops the hard-decline latch from this
// in-memory copy, mirroring what SubscriptionStore.ReleaseDunningHardDecline
// does to the row.
//
// Callers that release the latch and then keep using the same struct must call
// this. The failure path reads the latch back off the struct to decide whether a
// subsequent decline is permanent, so a stale true there re-latches the
// replacement card on any soft decline — turning "your card didn't go through"
// into "your bank has blocked this card for good" and stopping every remaining
// charge attempt.
//
// It releases the *latch* only. The record of which card died outlives it, on
// purpose: that record is what keeps the dead card from being charged again, and
// it has to survive a release or the next rung forgets and goes right back to
// the card the issuer killed. Only a successful charge clears the whole set —
// see SubscriptionStore.ClearDunning.
func (s *Subscription) ReleaseDunningHardDeclineLatch() {
	if s.Metadata == nil {
		return
	}
	delete(s.Metadata, SubscriptionMetaDunningHardDecline)
}

// LatchDunningHardDeclineMeta asserts the hard-decline latch on this in-memory
// copy so the write path persists it.
//
// The latch is an invariant, not a historical fact: it means "we are not
// charging this subscription", and roughly a dozen places — the admin badge and
// status line, the customer email's copy, the update-card page — read it that
// way. So whenever a charge is refused because the card is the dead one, the
// latch is re-asserted, even if a release had lifted it earlier. Letting it drift
// out of step with what the charge path actually does is what made the admin
// promise a "next charge attempt" that was never going to run.
func (s *Subscription) LatchDunningHardDeclineMeta() {
	if s.Metadata == nil {
		s.Metadata = map[string]any{}
	}
	s.Metadata[SubscriptionMetaDunningHardDecline] = true
}

// DunningHasDeadCard reports whether this subscription carries any memory of a
// permanently declined card — latched or released.
//
// Renewal routing keys on this rather than on the latch. A released
// subscription still needs the solo path: the release only means "a different
// card turned up", and deciding whether that is still true requires resolving
// the payment method against the dead one, which the batch path cannot do.
// Routing on the latch alone sent released subscriptions back into batching,
// where nothing knew which card to avoid and the dead one got charged again.
func (s *Subscription) DunningHasDeadCard() bool {
	return s.DunningHardDeclined() || len(s.DunningDeadPaymentMethods()) > 0
}

// DunningChargeBlocked reports whether a renewal charge must be skipped, given
// the payment method we would otherwise charge.
//
// The rule it enforces is about the card, not about the flag: a card the issuer
// permanently declined is never charged again. Keying on the recorded card
// rather than on the latch is what makes that hold across a release — the latch
// comes off as soon as a different card appears, and if that card later goes
// away we must still not fall back to the dead one.
//
// An empty paymentMethodID (nothing on file) is not blocked here; that case has
// its own failure path upstream and must stay reachable.
func (s *Subscription) DunningChargeBlocked(paymentMethodID string) bool {
	if dead := s.DunningDeadPaymentMethods(); len(dead) > 0 {
		for _, id := range dead {
			if id == paymentMethodID {
				return true
			}
		}
		return false
	}
	// Latched but with no recorded card — a row written before the card was
	// tracked, or a decline we could not attribute. We cannot tell old from new,
	// so stay blocked rather than risk re-charging the dead one. The emails
	// still run, and the customer's way out is unchanged.
	return s.DunningHardDeclined()
}

// DunningDeadPaymentMethods returns every payment method recorded as
// permanently declined on this past-due run, newest last. Empty when none is
// recorded.
//
// Reads the superseded single-card key too, so a subscription written by an
// earlier build keeps refusing the card it recorded.
func (s *Subscription) DunningDeadPaymentMethods() []string {
	if s.Metadata == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}

	if legacy, ok := s.Metadata[SubscriptionMetaDunningDeadPaymentMethod].(string); ok {
		add(legacy)
	}
	// jsonb arrays decode as []any of string; tolerate []string for callers that
	// built the value in Go without a round-trip.
	switch raw := s.Metadata[SubscriptionMetaDunningDeadPaymentMethods].(type) {
	case []any:
		for _, item := range raw {
			id, _ := item.(string)
			add(id)
		}
	case []string:
		for _, id := range raw {
			add(id)
		}
	}
	return out
}

// DunningDeclineCode returns the issuer's last stated reason for declining
// (e.g. "insufficient_funds"), or "" when none was recorded.
func (s *Subscription) DunningDeclineCode() string {
	if s.Metadata == nil {
		return ""
	}
	v, _ := s.Metadata[SubscriptionMetaDunningDeclineCode].(string)
	return v
}
