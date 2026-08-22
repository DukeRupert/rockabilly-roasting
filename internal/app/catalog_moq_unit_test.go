package app

import (
	"errors"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/google/uuid"

	"github.com/dukerupert/hiri/internal/domain"
)

func qty(n int) *int { return &n }

func TestValidateWholesaleMOQ(t *testing.T) {
	tests := []struct {
		name     string
		minQty   *int
		multiple *int
		wantErr  bool
	}{
		{"no constraints", nil, nil, false},
		{"minimum only", qty(6), nil, false},
		{"multiple only", nil, qty(6), false},
		{"minimum equal to multiple", qty(6), qty(6), false},
		{"minimum is a multiple", qty(12), qty(6), false},
		{"multiple of one accepts any minimum", qty(7), qty(1), false},

		{"zero minimum", qty(0), nil, true},
		{"negative minimum", qty(-5), nil, true},
		{"zero multiple", nil, qty(0), true},
		{"negative multiple", nil, qty(-2), true},

		// The subtle one. Both constraints are applied in sequence at checkout,
		// so min 10 with multiple 4 makes 10 itself unorderable and silently
		// moves the real floor to 12.
		{"minimum not divisible by multiple", qty(10), qty(4), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateWholesaleMOQ(tc.minQty, tc.multiple)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalidWholesaleMOQ),
				"error should wrap ErrInvalidWholesaleMOQ so respond.go maps it to 400, got %v", err)
		})
	}
}

// Anything validateWholesaleMOQ accepts must leave a quantity that
// domain.ValidateWholesaleCart also accepts — otherwise staff could save a rule
// that makes the variant unorderable at every quantity.
//
// The floor is not simply the minimum: with a multiple and no minimum, the
// smallest orderable quantity is the multiple itself, and with both set it is
// the minimum rounded up to the next multiple.
func TestAcceptedMOQRulesHaveAnOrderableQuantity(t *testing.T) {
	cases := []struct{ minQty, multiple *int }{
		{nil, nil}, {qty(6), nil}, {nil, qty(6)},
		{qty(6), qty(6)}, {qty(12), qty(6)}, {qty(7), qty(1)},
	}

	for _, c := range cases {
		require.NoError(t, validateWholesaleMOQ(c.minQty, c.multiple))

		floor := 1
		if c.minQty != nil {
			floor = *c.minQty
		}
		if c.multiple != nil && floor%*c.multiple != 0 {
			floor += *c.multiple - floor%*c.multiple
		}

		variant := domain.Variant{
			ID:                uuid.New(),
			WholesaleMinQty:   c.minQty,
			WholesaleMultiple: c.multiple,
		}
		violations := domain.ValidateWholesaleCart(
			[]domain.CartItem{{VariantID: variant.ID, Quantity: floor}},
			[]domain.Variant{variant},
		)
		assert.Empty(t, violations,
			"smallest orderable quantity %d should pass its own rules (min=%s multiple=%s)",
			floor, fmtQty(c.minQty), fmtQty(c.multiple))
	}
}

func fmtQty(v *int) string {
	if v == nil {
		return "none"
	}
	return strconv.Itoa(*v)
}
