package web

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// normalizeSubscribeLines is the one place a signup body becomes lines: the
// legacy one-line shape wrapped, duplicates merged, then the caps. Merging comes
// before the caps on purpose — two lines of 6 on one variant and plan are a
// line of 12, and 12 is refused rather than clamped.

func lineReq(plan, variant uuid.UUID, qty int) subscribeLineRequest {
	return subscribeLineRequest{PlanID: plan.String(), VariantID: variant.String(), Quantity: qty}
}

func TestNormalizeSubscribeLines(t *testing.T) {
	plan, plan2 := uuid.New(), uuid.New()
	variant, variant2 := uuid.New(), uuid.New()

	t.Run("the legacy top-level body is one line", func(t *testing.T) {
		got, err := normalizeSubscribeLines(subscribePaymentIntentRequest{
			PlanID: plan.String(), VariantID: variant.String(), Quantity: 2,
		})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, plan, got[0].PlanID)
		assert.Equal(t, variant, got[0].VariantID)
		assert.Equal(t, 2, got[0].Quantity)
	})

	t.Run("lines pass through", func(t *testing.T) {
		got, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: []subscribeLineRequest{
			lineReq(plan, variant, 1), lineReq(plan2, variant2, 3),
		}})
		require.NoError(t, err)
		require.Len(t, got, 2)
	})

	t.Run("both shapes at once is refused", func(t *testing.T) {
		_, err := normalizeSubscribeLines(subscribePaymentIntentRequest{
			PlanID: plan.String(), VariantID: variant.String(), Quantity: 1,
			Lines: []subscribeLineRequest{lineReq(plan2, variant2, 1)},
		})
		assert.Error(t, err)
	})

	t.Run("the same variant and plan merge", func(t *testing.T) {
		got, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: []subscribeLineRequest{
			lineReq(plan, variant, 2), lineReq(plan, variant, 3),
		}})
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 5, got[0].Quantity)
	})

	t.Run("one variant on two plans stays apart", func(t *testing.T) {
		got, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: []subscribeLineRequest{
			lineReq(plan, variant, 1), lineReq(plan2, variant, 1),
		}})
		require.NoError(t, err)
		assert.Len(t, got, 2)
	})

	t.Run("a merged quantity over ten is refused, not clamped", func(t *testing.T) {
		_, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: []subscribeLineRequest{
			lineReq(plan, variant, 6), lineReq(plan, variant, 6),
		}})
		assert.Error(t, err)
	})

	t.Run("quantity 0 and 11 are refused", func(t *testing.T) {
		for _, qty := range []int{0, 11} {
			_, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: []subscribeLineRequest{
				lineReq(plan, variant, qty),
			}})
			assert.Error(t, err, "quantity %d", qty)
		}
	})

	t.Run("eleven lines are refused", func(t *testing.T) {
		lines := make([]subscribeLineRequest, 11)
		for i := range lines {
			lines[i] = lineReq(plan, uuid.New(), 1)
		}
		_, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: lines})
		assert.Error(t, err)
	})

	t.Run("ten lines are fine", func(t *testing.T) {
		lines := make([]subscribeLineRequest, 10)
		for i := range lines {
			lines[i] = lineReq(plan, uuid.New(), 1)
		}
		got, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: lines})
		require.NoError(t, err)
		assert.Len(t, got, 10)
	})

	t.Run("an unparseable id is refused", func(t *testing.T) {
		_, err := normalizeSubscribeLines(subscribePaymentIntentRequest{Lines: []subscribeLineRequest{
			{PlanID: "nope", VariantID: variant.String(), Quantity: 1},
		}})
		assert.Error(t, err)
	})

	t.Run("no lines at all is refused", func(t *testing.T) {
		_, err := normalizeSubscribeLines(subscribePaymentIntentRequest{})
		assert.Error(t, err)
	})
}
