package web

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
)

func TestLabelRatesErrorMessage_DealersChoice(t *testing.T) {
	err := fmt.Errorf("prepare label: %w", app.ErrDealersChoicePicksMissing)
	assert.Equal(t, "Save the dealer's choice picks on this order before buying a label.", labelRatesErrorMessage(err))
}

func TestDealersChoiceOffer(t *testing.T) {
	rules := map[uuid.UUID]domain.PickRule{
		uuid.New(): {PickCount: 6},
		uuid.New(): {PickCount: 3},
		uuid.New(): {PickCount: 3, ExcludeDecaf: true},
	}
	sizes, noDecaf := dealersChoiceOffer(rules)
	assert.Equal(t, []int{3, 6}, sizes)
	assert.True(t, noDecaf)

	_, noDecaf = dealersChoiceOffer(map[uuid.UUID]domain.PickRule{uuid.New(): {PickCount: 4}})
	assert.False(t, noDecaf, "no no-decaf variant, so the page must not offer it")
}
