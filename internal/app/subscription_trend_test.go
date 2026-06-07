package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// subTrendFixture creates the prerequisite customer, address, variant, and plan
// for building subscriptions in the active-base reconstruction tests.
func subTrendFixture(t *testing.T, tx pgx.Tx) (subStore *store.SubscriptionStore, customerID, addrID, variantID, planID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	custID, shipID, _ := orderFixtures(t, tx)
	product := testutil.CreateProduct(t, tx)
	variant := testutil.CreateVariant(t, tx, product.ID, testutil.WithSKU("SUB-TREND"))

	subStore = store.NewSubscriptionStore(nil)
	plan, err := subStore.CreatePlan(ctx, tx, store.CreatePlanParams{
		Name:          "Monthly",
		Interval:      domain.SubscriptionIntervalEvery30Days,
		IntervalCount: 1,
		DiscountPct:   10,
		IsActive:      true,
	})
	require.NoError(t, err)
	return subStore, custID, shipID, variant.ID, plan.ID
}

// makeSub inserts a subscription, then backdates created_at and (when given)
// cancelled_at directly — the store's Create stamps created_at = now(), but the
// reconstruction queries key entirely off these lifecycle timestamps, so the
// test has to set them explicitly.
func makeSub(
	t *testing.T, tx pgx.Tx, s *store.SubscriptionStore,
	custID, addrID, variantID, planID uuid.UUID,
	createdAt time.Time, status domain.SubscriptionStatus,
	cancelledAt, endsAt *time.Time,
) {
	t.Helper()
	ctx := context.Background()
	sub, err := s.Create(ctx, tx, store.CreateSubscriptionParams{
		CustomerID:         custID,
		PlanID:             planID,
		VariantID:          variantID,
		Quantity:           1,
		Status:             status,
		ShippingAddressID:  addrID,
		CurrentPeriodStart: createdAt,
		CurrentPeriodEnd:   createdAt.AddDate(0, 0, 30),
		NextOrderAt:        createdAt.AddDate(0, 0, 30),
		EndsAt:             endsAt,
	})
	require.NoError(t, err)
	_, err = tx.Exec(ctx,
		`UPDATE subscriptions SET created_at = $1, cancelled_at = $2 WHERE id = $3`,
		createdAt, cancelledAt, sub.ID)
	require.NoError(t, err)
}

func TestSubscriptionStore_ActiveBaseReconstruction(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)
	s, custID, addrID, variantID, planID := subTrendFixture(t, tx)

	day := func(d int) time.Time { return time.Date(2025, 1, d, 0, 0, 0, 0, time.UTC) }
	at := func(d int) *time.Time { v := day(d); return &v }

	// A: created 01-10, still active        → live from 01-10 on
	makeSub(t, tx, s, custID, addrID, variantID, planID, day(10), domain.SubscriptionStatusActive, nil, nil)
	// B: created 01-10, cancelled 01-25      → live 01-10..01-25
	makeSub(t, tx, s, custID, addrID, variantID, planID, day(10), domain.SubscriptionStatusCancelled, at(25), nil)
	// C: created 01-22, still active         → live from 01-22 on
	makeSub(t, tx, s, custID, addrID, variantID, planID, day(22), domain.SubscriptionStatusActive, nil, nil)
	// D: created 01-01, cancelled 01-05      → fully outside a mid-January look
	makeSub(t, tx, s, custID, addrID, variantID, planID, day(1), domain.SubscriptionStatusCancelled, at(5), nil)
	// E: created 01-10, paused (no end)      → counts as live (temporary state)
	makeSub(t, tx, s, custID, addrID, variantID, planID, day(10), domain.SubscriptionStatusPaused, nil, nil)
	// F: created 01-10, expired ends 01-12   → live 01-10..01-12
	makeSub(t, tx, s, custID, addrID, variantID, planID, day(10), domain.SubscriptionStatusExpired, nil, at(12))

	t.Run("ActiveSubscriptionsAsOf counts the live base at an instant", func(t *testing.T) {
		// As of 01-20: A, B (cancel pending 01-25), E (paused) are live.
		// C not created yet, D cancelled, F already expired.
		got, err := s.ActiveSubscriptionsAsOf(ctx, tx, day(20))
		require.NoError(t, err)
		assert.Equal(t, 3, got)
	})

	t.Run("ActiveSubscriptionDeltasByDay reports per-day net change", func(t *testing.T) {
		deltas, err := s.ActiveSubscriptionDeltasByDay(ctx, tx, day(1), day(32), time.UTC)
		require.NoError(t, err)

		byDay := make(map[string]int, len(deltas))
		for _, d := range deltas {
			byDay[d.Date.Format("2006-01-02")] = d.Net
		}

		assert.Equal(t, 1, byDay["2025-01-01"], "D created")
		assert.Equal(t, -1, byDay["2025-01-05"], "D cancelled")
		assert.Equal(t, 4, byDay["2025-01-10"], "A, B, E, F created")
		assert.Equal(t, -1, byDay["2025-01-12"], "F expired")
		assert.Equal(t, 1, byDay["2025-01-22"], "C created")
		assert.Equal(t, -1, byDay["2025-01-25"], "B cancelled")
		assert.Len(t, deltas, 6, "exactly the days with a change")
	})

	t.Run("carry-forward reconstructs the running active total", func(t *testing.T) {
		// Seed at 01-08 (only A/B/E/F created on 01-10 are still ahead; D's
		// create+cancel are both before the window) and walk the deltas.
		baseline, err := s.ActiveSubscriptionsAsOf(ctx, tx, day(8))
		require.NoError(t, err)
		assert.Equal(t, 0, baseline, "nothing live on 01-08: D already cancelled, others not yet created")

		deltas, err := s.ActiveSubscriptionDeltasByDay(ctx, tx, day(8), day(32), time.UTC)
		require.NoError(t, err)
		byDay := make(map[string]int, len(deltas))
		for _, d := range deltas {
			byDay[d.Date.Format("2006-01-02")] = d.Net
		}
		running := baseline

		// Walk 01-08 → 01-31 accumulating; check a few checkpoints.
		checkpoints := map[int]int{}
		for d := 8; d <= 31; d++ {
			running += byDay[day(d).Format("2006-01-02")]
			checkpoints[d] = running
		}
		assert.Equal(t, 4, checkpoints[10], "A,B,E,F live")
		assert.Equal(t, 3, checkpoints[12], "F expired → 3")
		assert.Equal(t, 4, checkpoints[22], "C created → 4")
		assert.Equal(t, 3, checkpoints[25], "B cancelled → 3")
		assert.Equal(t, 3, checkpoints[31], "today: A, C, E")
	})
}
