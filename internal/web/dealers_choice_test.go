package web

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/ui/storefront"
)

func TestLabelRatesErrorMessage_DealersChoice(t *testing.T) {
	err := fmt.Errorf("prepare label: %w", app.ErrDealersChoicePicksMissing)
	assert.Equal(t, "Save the dealer's choice picks on this order before buying a label.", labelRatesErrorMessage(err))
}

func TestDealersChoiceOffer(t *testing.T) {
	sizes, decafChoice := dealersChoiceOffer([]domain.PickRule{
		{VariantID: uuid.New(), PickCount: 6},
		{VariantID: uuid.New(), PickCount: 3},
		{VariantID: uuid.New(), PickCount: 3, ExcludeDecaf: true},
	})
	assert.Equal(t, []int{3, 6}, sizes)
	assert.True(t, decafChoice, "sold with and without decaf")

	_, decafChoice = dealersChoiceOffer([]domain.PickRule{{PickCount: 4}})
	assert.False(t, decafChoice, "no no-decaf variant, so there is no decaf choice to offer")

	_, decafChoice = dealersChoiceOffer([]domain.PickRule{{PickCount: 4, ExcludeDecaf: true}})
	assert.False(t, decafChoice, "every variant is no-decaf, so there is nothing to choose")
}

func TestHasGrindOption(t *testing.T) {
	grind := storefront.OptionWithValues{Option: domain.ProductOption{Name: "Grind"}, Values: []domain.ProductOptionValue{{Value: "Drip"}}}
	pack := storefront.OptionWithValues{Option: domain.ProductOption{Name: "Pack"}, Values: []domain.ProductOptionValue{{Value: "3 Bags"}}}
	assert.True(t, hasGrindOption([]storefront.OptionWithValues{pack, grind}))
	assert.False(t, hasGrindOption([]storefront.OptionWithValues{pack}))
}
