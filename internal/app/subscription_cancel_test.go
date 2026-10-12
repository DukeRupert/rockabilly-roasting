package app_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/testutil"
)

// A customer can cancel any subscription that is still running, paused
// included: making them resume first would book a charge to get to the exit.
func TestCancelSubscription_ByStatus(t *testing.T) {
	for status, cancellable := range map[domain.SubscriptionStatus]bool{
		domain.SubscriptionStatusActive:    true,
		domain.SubscriptionStatusPaused:    true,
		domain.SubscriptionStatusPastDue:   true,
		domain.SubscriptionStatusCancelled: false,
		domain.SubscriptionStatusExpired:   false,
	} {
		t.Run(string(status), func(t *testing.T) {
			ctx := context.Background()
			sub, _ := dueBox(t, status)
			tx, err := testPool.Begin(ctx)
			require.NoError(t, err)
			defer tx.Rollback(ctx) //nolint:errcheck

			got, err := newSubscriptionService().CancelSubscription(ctx, tx, sub.ID, testutil.TestActor())
			if !cancellable {
				assert.ErrorIs(t, err, app.ErrSubscriptionNotCancellable)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, domain.SubscriptionStatusCancelled, got.Status)
			assert.NotNil(t, got.CancelledAt)
		})
	}
}
