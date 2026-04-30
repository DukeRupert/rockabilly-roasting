package app_test

import (
	"errors"
	"math"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
)

// almostEqual compares floats with a small epsilon — gram-to-ounce conversion
// produces non-terminating decimals, so exact equality fails legitimately.
func almostEqual(t *testing.T, want, got float64) {
	t.Helper()
	if math.Abs(want-got) > 1e-9 {
		t.Fatalf("expected %.12f, got %.12f", want, got)
	}
}

// intPtr returns a pointer to v. Variant.WeightGrams is *int in the domain.
func intPtr(v int) *int { return &v }

func TestCalculateShipmentWeightOz_SingleItem(t *testing.T) {
	variantID := uuid.New()
	items := []domain.LineItem{
		{ID: uuid.New(), VariantID: variantID, Quantity: 1},
	}
	weights := map[uuid.UUID]*int{variantID: intPtr(340)} // 12 oz bag

	got, err := app.CalculateShipmentWeightOz(items, weights, 0)
	assert.NoError(t, err)
	almostEqual(t, 340.0/28.349523125, got)
}

func TestCalculateShipmentWeightOz_MultipleItemsWithQuantities(t *testing.T) {
	v1, v2 := uuid.New(), uuid.New()
	items := []domain.LineItem{
		{ID: uuid.New(), VariantID: v1, Quantity: 2},
		{ID: uuid.New(), VariantID: v2, Quantity: 3},
	}
	weights := map[uuid.UUID]*int{
		v1: intPtr(340), // 12 oz bag × 2 = 680 g
		v2: intPtr(454), // 16 oz bag × 3 = 1362 g
	}

	got, err := app.CalculateShipmentWeightOz(items, weights, 0)
	assert.NoError(t, err)
	almostEqual(t, (680.0+1362.0)/28.349523125, got)
}

func TestCalculateShipmentWeightOz_TareAdded(t *testing.T) {
	variantID := uuid.New()
	items := []domain.LineItem{
		{ID: uuid.New(), VariantID: variantID, Quantity: 1},
	}
	weights := map[uuid.UUID]*int{variantID: intPtr(340)}

	got, err := app.CalculateShipmentWeightOz(items, weights, 2.5)
	assert.NoError(t, err)
	almostEqual(t, 340.0/28.349523125+2.5, got)
}

func TestCalculateShipmentWeightOz_EmptyItemsReturnsTareOnly(t *testing.T) {
	got, err := app.CalculateShipmentWeightOz(nil, map[uuid.UUID]*int{}, 4.0)
	assert.NoError(t, err)
	almostEqual(t, 4.0, got)
}

func TestCalculateShipmentWeightOz_EmptyItemsZeroTare(t *testing.T) {
	got, err := app.CalculateShipmentWeightOz(nil, map[uuid.UUID]*int{}, 0)
	assert.NoError(t, err)
	almostEqual(t, 0, got)
}

func TestCalculateShipmentWeightOz_NilWeightReturnsSentinel(t *testing.T) {
	v1, v2 := uuid.New(), uuid.New()
	items := []domain.LineItem{
		{ID: uuid.New(), VariantID: v1, Quantity: 1},
		{ID: uuid.New(), VariantID: v2, Quantity: 1},
	}
	weights := map[uuid.UUID]*int{
		v1: intPtr(340),
		v2: nil, // missing weight
	}

	_, err := app.CalculateShipmentWeightOz(items, weights, 0)
	assert.ErrorIs(t, err, app.ErrShipmentWeightUnknown)
}

func TestCalculateShipmentWeightOz_MissingVariantReturnsSentinel(t *testing.T) {
	v1 := uuid.New()
	items := []domain.LineItem{
		{ID: uuid.New(), VariantID: v1, Quantity: 1},
	}
	// v1 not present in the map at all.
	_, err := app.CalculateShipmentWeightOz(items, map[uuid.UUID]*int{}, 0)
	assert.ErrorIs(t, err, app.ErrShipmentWeightUnknown)
}

func TestCalculateShipmentWeightOz_NonPositiveQuantityErrors(t *testing.T) {
	v1 := uuid.New()
	items := []domain.LineItem{
		{ID: uuid.New(), VariantID: v1, Quantity: 0},
	}
	weights := map[uuid.UUID]*int{v1: intPtr(340)}

	_, err := app.CalculateShipmentWeightOz(items, weights, 0)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, app.ErrShipmentWeightUnknown))
}

func TestCalculateShipmentWeightOz_ConversionFactor(t *testing.T) {
	// 28.349523125 g = 1 oz exactly. A single 28349.523125 mg variant should
	// not work because the field is int grams, but 28350 g = 1000.0168 oz
	// (one part in 60,000) — we just confirm that the divisor is correct
	// to many decimal places.
	v1 := uuid.New()
	items := []domain.LineItem{
		{ID: uuid.New(), VariantID: v1, Quantity: 1},
	}
	weights := map[uuid.UUID]*int{v1: intPtr(28350)} // 1 oz ≈ 28.35 g, so 28350 g ≈ 1000 oz

	got, err := app.CalculateShipmentWeightOz(items, weights, 0)
	assert.NoError(t, err)
	// 28350 / 28.349523125 ≈ 1000.01682
	almostEqual(t, 28350.0/28.349523125, got)
	// Sanity: it really is in the 1000-oz neighborhood.
	if got < 999 || got > 1001 {
		t.Fatalf("expected ~1000 oz, got %f", got)
	}
}
