package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ladder mirrors the shape staff author on a wholesale price list: a base rung
// plus two volume breaks.
func ladder() TierLadder {
	return NewTierLadder([]PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 12, Amount: 1000},
		{MinQuantity: 24, Amount: 950},
	})
}

func TestNewTierLadderNormalizes(t *testing.T) {
	t.Run("sorts unsorted input", func(t *testing.T) {
		l := NewTierLadder([]PriceTier{
			{MinQuantity: 24, Amount: 950},
			{MinQuantity: 1, Amount: 1100},
			{MinQuantity: 12, Amount: 1000},
		})
		assert.Equal(t, []PriceTier{
			{MinQuantity: 1, Amount: 1100},
			{MinQuantity: 12, Amount: 1000},
			{MinQuantity: 24, Amount: 950},
		}, l.Rungs())
	})

	t.Run("collapses duplicate thresholds, last wins", func(t *testing.T) {
		l := NewTierLadder([]PriceTier{
			{MinQuantity: 12, Amount: 1000},
			{MinQuantity: 12, Amount: 900},
		})
		require.Len(t, l.Rungs(), 1)
		assert.Equal(t, 900, l.Rungs()[0].Amount)
	})

	t.Run("clamps base rung thresholds to 1", func(t *testing.T) {
		// A base price arrives as min_quantity NULL, which the store maps to 0.
		l := NewTierLadder([]PriceTier{{MinQuantity: 0, Amount: 1100}})
		require.Len(t, l.Rungs(), 1)
		assert.Equal(t, 1, l.Rungs()[0].MinQuantity)
	})

	t.Run("empty input yields empty ladder", func(t *testing.T) {
		assert.True(t, NewTierLadder(nil).IsEmpty())
		assert.True(t, NewTierLadder([]PriceTier{}).IsEmpty())
	})

	t.Run("Rungs returns a copy", func(t *testing.T) {
		l := ladder()
		l.Rungs()[0].Amount = 1
		assert.Equal(t, 1100, l.Rungs()[0].Amount)
	})
}

func TestTierLadderZeroValue(t *testing.T) {
	var l TierLadder
	assert.True(t, l.IsEmpty())
	assert.False(t, l.IsTiered())
	assert.Equal(t, 0, l.UnitPriceAt(50))
	assert.Empty(t, l.Rungs())

	_, ok := l.NextTier(1)
	assert.False(t, ok)
	_, ok = l.Upgrade(1, 1)
	assert.False(t, ok)
	_, ok = l.Drop(24, 1)
	assert.False(t, ok)
}

func TestUnitPriceAt(t *testing.T) {
	l := ladder()

	tests := []struct {
		name string
		qty  int
		want int
	}{
		{"below first break", 1, 1100},
		{"just below first break", 11, 1000 + 100},
		{"exactly at first break", 12, 1000},
		{"between breaks", 23, 1000},
		{"exactly at second break", 24, 950},
		{"well above top rung", 5000, 950},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, l.UnitPriceAt(tc.qty))
		})
	}
}

func TestUnitPriceAtIsTotal(t *testing.T) {
	// A ladder with no base rung must still price every quantity — the guarantee
	// that no quantity is ever unpriceable. Quantities below the lowest rung
	// resolve to that rung.
	l := NewTierLadder([]PriceTier{
		{MinQuantity: 12, Amount: 1000},
		{MinQuantity: 24, Amount: 950},
	})
	assert.Equal(t, 1000, l.UnitPriceAt(1))
	assert.Equal(t, 1000, l.UnitPriceAt(11))
	assert.Equal(t, 950, l.UnitPriceAt(24))
}

func TestSingleRungLadderBehavesAsFlatPrice(t *testing.T) {
	l := NewTierLadder([]PriceTier{{MinQuantity: 1, Amount: 1100}})
	assert.False(t, l.IsTiered())
	for _, qty := range []int{1, 12, 24, 1000} {
		assert.Equal(t, 1100, l.UnitPriceAt(qty))
	}
	_, ok := l.Upgrade(1, 1)
	assert.False(t, ok, "flat price has nothing to upgrade to")
	_, ok = l.Drop(100, 1)
	assert.False(t, ok, "flat price cannot lose a rung")
}

func TestNextTier(t *testing.T) {
	l := ladder()

	next, ok := l.NextTier(1)
	require.True(t, ok)
	assert.Equal(t, 12, next.MinQuantity)

	next, ok = l.NextTier(12)
	require.True(t, ok)
	assert.Equal(t, 24, next.MinQuantity, "at a rung, next is the one above it")

	_, ok = l.NextTier(24)
	assert.False(t, ok, "top rung has no next")
	_, ok = l.NextTier(9999)
	assert.False(t, ok)
}

func TestUpgradeWithinProximity(t *testing.T) {
	l := ladder()

	// Threshold for the 12-rung is max(3, ceil(0.10*12)=2) = 3, so 9..11 nudge.
	for _, qty := range []int{9, 10, 11} {
		u, ok := l.Upgrade(qty, 1)
		require.True(t, ok, "qty %d should nudge toward the 12 break", qty)
		assert.Equal(t, 12, u.TargetQty)
		assert.Equal(t, 12-qty, u.AddQty)
		assert.Equal(t, 100, u.UnitSavingCents)
	}

	u, ok := l.Upgrade(8, 1)
	assert.False(t, ok, "4 units short of the 12 break is outside the threshold")
	assert.Zero(t, u)
}

func TestUpgradeProximityScalesWithRungSize(t *testing.T) {
	// On a deep rung the percentage takes over from the floor: threshold for a
	// 100-unit break is max(3, ceil(0.10*100)=10) = 10.
	l := NewTierLadder([]PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 100, Amount: 800},
	})

	u, ok := l.Upgrade(90, 1)
	require.True(t, ok, "10 units short of a 100 break is within 10%")
	assert.Equal(t, 10, u.AddQty)

	_, ok = l.Upgrade(89, 1)
	assert.False(t, ok, "11 units short is outside the threshold")
}

func TestUpgradeOnTopRungDeclines(t *testing.T) {
	_, ok := ladder().Upgrade(24, 1)
	assert.False(t, ok)
	_, ok = ladder().Upgrade(500, 1)
	assert.False(t, ok)
}

func TestUpgradeRespectsOrderMultiple(t *testing.T) {
	// A variant sold in sixes cannot land on 25, so a 25-unit break must round
	// up to 30 — the nudge has to be actionable.
	l := NewTierLadder([]PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 25, Amount: 950},
	})

	u, ok := l.Upgrade(24, 6)
	require.True(t, ok)
	assert.Equal(t, 30, u.TargetQty, "target rounds up to a valid multiple")
	assert.Equal(t, 6, u.AddQty)
	assert.Equal(t, 0, u.TargetQty%6)

	t.Run("multiple below 1 means unconstrained", func(t *testing.T) {
		for _, m := range []int{0, 1, -3} {
			u, ok := l.Upgrade(24, m)
			require.True(t, ok)
			assert.Equal(t, 25, u.TargetQty)
		}
	})
}

func TestUpgradeProximityWidensToOneOrderIncrement(t *testing.T) {
	// The raw threshold for a 25-unit break is 3. Rounding up to a six-pack puts
	// the target 6 units away, which a units-only check would reject — but one
	// more case is exactly the move worth suggesting, so the window widens to
	// the increment.
	l := NewTierLadder([]PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 25, Amount: 950},
	})

	u, ok := l.Upgrade(24, 6)
	require.True(t, ok)
	assert.Equal(t, 6, u.AddQty, "one more case")

	t.Run("still declines beyond one increment", func(t *testing.T) {
		// 18 is two cases short of 30; the window covers one.
		_, ok := l.Upgrade(18, 6)
		assert.False(t, ok)
	})
}

func TestUpgradeRoundingIntoAFurtherRungPricesTheTarget(t *testing.T) {
	// Rounding 12 up to 20 overshoots past the 20 rung, so the quoted price must
	// be the one actually charged at 20 — not the 12 rung's price.
	l := NewTierLadder([]PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 12, Amount: 1000},
		{MinQuantity: 20, Amount: 900},
	})

	u, ok := l.Upgrade(10, 20)
	require.True(t, ok)
	assert.Equal(t, 20, u.TargetQty)
	assert.Equal(t, 900, u.TargetUnitPrice, "priced at the target, not the next rung")
	assert.Equal(t, 200, u.UnitSavingCents)
}

func TestUpgradeDeclinesInvertedRung(t *testing.T) {
	// Data-entry mistake: the higher rung costs more. The ladder keeps it so
	// admin validation can flag it, but the nudge must never advertise it.
	l := NewTierLadder([]PriceTier{
		{MinQuantity: 1, Amount: 900},
		{MinQuantity: 12, Amount: 1100},
	})
	_, ok := l.Upgrade(11, 1)
	assert.False(t, ok)
}

func TestUpgradeCostsLess(t *testing.T) {
	l := ladder()

	t.Run("all-units repricing can lower the line total outright", func(t *testing.T) {
		// 23 x $11.00 = $253.00, but 24 x $9.50 = $228.00.
		u, ok := l.Upgrade(23, 1)
		require.True(t, ok)
		assert.Equal(t, 23*1000-24*950, u.TotalSavingCents)
		assert.True(t, u.CostsLess(), "one more unit costs $25 less overall")
	})

	t.Run("shallow break still saves per unit but costs more overall", func(t *testing.T) {
		shallow := NewTierLadder([]PriceTier{
			{MinQuantity: 1, Amount: 1000},
			{MinQuantity: 12, Amount: 999},
		})
		u, ok := shallow.Upgrade(9, 1)
		require.True(t, ok)
		assert.Positive(t, u.UnitSavingCents)
		assert.False(t, u.CostsLess())
		assert.Negative(t, u.TotalSavingCents, "buying 3 more units costs more")
	})
}

func TestUpgradeRejectsNonPositiveQuantity(t *testing.T) {
	for _, qty := range []int{0, -1} {
		_, ok := ladder().Upgrade(qty, 1)
		assert.False(t, ok)
	}
}

func TestDrop(t *testing.T) {
	l := ladder()

	t.Run("falling below a rung raises the unit price", func(t *testing.T) {
		d, ok := l.Drop(24, 23)
		require.True(t, ok)
		assert.Equal(t, 24, d.FromQty)
		assert.Equal(t, 23, d.ToQty)
		assert.Equal(t, 950, d.FromUnitPrice)
		assert.Equal(t, 1000, d.ToUnitPrice)
		assert.Equal(t, 50, d.UnitLossCents)
		assert.Equal(t, 24, d.LostTierMinQty, "names the rung left behind")
	})

	t.Run("crossing two rungs at once", func(t *testing.T) {
		d, ok := l.Drop(30, 5)
		require.True(t, ok)
		assert.Equal(t, 950, d.FromUnitPrice)
		assert.Equal(t, 1100, d.ToUnitPrice)
		assert.Equal(t, 150, d.UnitLossCents)
	})

	t.Run("reduction within a rung is silent", func(t *testing.T) {
		_, ok := l.Drop(30, 24)
		assert.False(t, ok)
		_, ok = l.Drop(23, 12)
		assert.False(t, ok)
	})

	t.Run("increases are not drops", func(t *testing.T) {
		_, ok := l.Drop(12, 24)
		assert.False(t, ok)
		_, ok = l.Drop(12, 12)
		assert.False(t, ok)
	})

	t.Run("dropping to zero is a removal, not a reprice", func(t *testing.T) {
		_, ok := l.Drop(24, 0)
		assert.False(t, ok)
	})
}

func TestDropAndUnitPriceAgree(t *testing.T) {
	// The guarantee the whole feature rests on: what a drop reports and what the
	// cart charges come from the same rungs.
	l := ladder()
	for from := 1; from <= 40; from++ {
		for to := 1; to < from; to++ {
			d, ok := l.Drop(from, to)
			if !ok {
				assert.LessOrEqual(t, l.UnitPriceAt(to), l.UnitPriceAt(from),
					"no drop reported for %d->%d, so price must not have risen", from, to)
				continue
			}
			assert.Equal(t, l.UnitPriceAt(from), d.FromUnitPrice)
			assert.Equal(t, l.UnitPriceAt(to), d.ToUnitPrice)
		}
	}
}

func TestUpgradeAndUnitPriceAgree(t *testing.T) {
	l := ladder()
	for qty := 1; qty <= 40; qty++ {
		u, ok := l.Upgrade(qty, 1)
		if !ok {
			continue
		}
		assert.Equal(t, l.UnitPriceAt(qty), u.CurrentUnitPrice,
			"nudge at qty %d must quote the price the cart charges", qty)
		assert.Equal(t, l.UnitPriceAt(u.TargetQty), u.TargetUnitPrice,
			"nudge at qty %d must quote the price the target will charge", qty)
		assert.Equal(t, u.CurrentUnitPrice-u.TargetUnitPrice, u.UnitSavingCents)
		assert.Equal(t, qty+u.AddQty, u.TargetQty)
	}
}
