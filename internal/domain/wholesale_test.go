package domain_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func intPtr(v int) *int { return &v }

func TestValidateWholesaleCart(t *testing.T) {
	v1 := uuid.New()
	v2 := uuid.New()
	v3 := uuid.New()

	variants := []domain.Variant{
		{ID: v1, SKU: "SKU-A", WholesaleMinQty: intPtr(6), WholesaleMultiple: intPtr(6)},
		{ID: v2, SKU: "SKU-B", WholesaleMinQty: intPtr(12)},
		{ID: v3, SKU: "SKU-C"}, // no MOQ constraints
	}

	t.Run("all items valid", func(t *testing.T) {
		items := []domain.CartItem{
			{VariantID: v1, Quantity: 12},
			{VariantID: v2, Quantity: 24},
			{VariantID: v3, Quantity: 1},
		}
		violations := domain.ValidateWholesaleCart(items, variants)
		assert.Nil(t, violations)
	})

	t.Run("below minimum quantity", func(t *testing.T) {
		items := []domain.CartItem{
			{VariantID: v1, Quantity: 3},
		}
		violations := domain.ValidateWholesaleCart(items, variants)
		assert.Len(t, violations, 1)
		assert.Equal(t, v1, violations[0].VariantID)
		assert.Equal(t, 3, violations[0].Ordered)
		assert.Equal(t, 6, violations[0].Minimum)
	})

	t.Run("not a multiple", func(t *testing.T) {
		items := []domain.CartItem{
			{VariantID: v1, Quantity: 7},
		}
		violations := domain.ValidateWholesaleCart(items, variants)
		assert.Len(t, violations, 1)
		assert.Equal(t, 6, violations[0].Multiple)
	})

	t.Run("exact minimum passes", func(t *testing.T) {
		items := []domain.CartItem{
			{VariantID: v1, Quantity: 6},
			{VariantID: v2, Quantity: 12},
		}
		violations := domain.ValidateWholesaleCart(items, variants)
		assert.Nil(t, violations)
	})

	t.Run("unknown variant ignored", func(t *testing.T) {
		items := []domain.CartItem{
			{VariantID: uuid.New(), Quantity: 1},
		}
		violations := domain.ValidateWholesaleCart(items, variants)
		assert.Nil(t, violations)
	})

	t.Run("multiple violations returned", func(t *testing.T) {
		items := []domain.CartItem{
			{VariantID: v1, Quantity: 2},
			{VariantID: v2, Quantity: 5},
		}
		violations := domain.ValidateWholesaleCart(items, variants)
		assert.Len(t, violations, 2)
	})
}
