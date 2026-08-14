package storefront

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func testLadder() domain.TierLadder {
	return domain.NewTierLadder([]domain.PriceTier{
		{MinQuantity: 1, Amount: 1100},
		{MinQuantity: 12, Amount: 1000},
		{MinQuantity: 24, Amount: 950},
	})
}

func TestLadderHint(t *testing.T) {
	assert.Equal(t, "12+ $10.00 · 24+ $9.50", ladderHint(testLadder()))

	t.Run("flat price renders nothing", func(t *testing.T) {
		flat := domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: 1100}})
		assert.Empty(t, ladderHint(flat), "callers drop the element entirely on empty")
		assert.Empty(t, ladderHint(domain.TierLadder{}))
	})
}

func TestLadderJSON(t *testing.T) {
	// Numbers only — safe in an attribute without further escaping.
	assert.Equal(t, "[[1,1100],[12,1000],[24,950]]", ladderJSON(testLadder()))
	assert.Equal(t, "[]", ladderJSON(domain.TierLadder{}))
}

func TestUpgradeFor(t *testing.T) {
	item := WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 23}

	u := upgradeFor(item)
	require.NotNil(t, u)
	assert.Equal(t, 1, u.AddQty)
	assert.Equal(t, 24, u.TargetQty)

	t.Run("nothing to say on the top rung", func(t *testing.T) {
		assert.Nil(t, upgradeFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 24}))
	})

	t.Run("nothing to say when the break is far off", func(t *testing.T) {
		assert.Nil(t, upgradeFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 4}))
	})

	t.Run("nothing to say on a flat price", func(t *testing.T) {
		flat := domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: 1100}})
		assert.Nil(t, upgradeFor(WholesaleCheckoutItem{Ladder: flat, Quantity: 5}))
	})

	t.Run("target respects the case multiple", func(t *testing.T) {
		six := 6
		l := domain.NewTierLadder([]domain.PriceTier{
			{MinQuantity: 1, Amount: 1100},
			{MinQuantity: 25, Amount: 950},
		})
		u := upgradeFor(WholesaleCheckoutItem{Ladder: l, Quantity: 24, Multiple: &six})
		require.NotNil(t, u)
		assert.Equal(t, 30, u.TargetQty, "24 -> 30 is one more case, not an unorderable 25")
		assert.Zero(t, u.TargetQty%6)
	})
}

func TestUpgradeLine(t *testing.T) {
	t.Run("leads with the total when moving up costs less overall", func(t *testing.T) {
		// 23 x $10.00 = $230.00 against 24 x $9.50 = $228.00.
		u := upgradeFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 23})
		require.NotNil(t, u)
		require.True(t, u.CostsLess())
		assert.Equal(t, "Add 1 more and pay $2.00 less — the 24+ price is $9.50 each.", upgradeLine(*u))
	})

	t.Run("leads with the rate when moving up costs more overall", func(t *testing.T) {
		u := upgradeFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 9})
		require.NotNil(t, u)
		require.False(t, u.CostsLess())
		assert.Equal(t, "Add 3 more to reach $10.00 each — $1.00 off every unit.", upgradeLine(*u))
	})
}

func TestPriceNoteFor(t *testing.T) {
	drop, ok := testLadder().Drop(24, 23)
	require.True(t, ok)

	t.Run("a drop outranks everything, and carries the way back", func(t *testing.T) {
		// Stepping one below a break makes both statements true at once, and
		// they overlap — the price the upgrade targets is the one just lost. One
		// sentence: what it costs now, and what undoes it.
		note := priceNoteFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 23, Drop: &drop})
		assert.Equal(t, PriceNoteDrop, note.Kind)
		assert.Equal(t, "Now $10.00 each — add 1 back to get $9.50 again.", note.Text)
	})

	t.Run("a drop out of reach of any rung just reports itself", func(t *testing.T) {
		// Dropped from 24 to 2 — far below every break, so there is no short way
		// back to offer and pretending otherwise would be noise.
		far, ok := testLadder().Drop(24, 2)
		require.True(t, ok)
		note := priceNoteFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 2, Drop: &far})
		assert.Equal(t, PriceNoteDrop, note.Kind)
		assert.Equal(t, "Now $11.00 each — you were getting $9.50 at 24+.", note.Text)
	})

	t.Run("a reachable break outranks the ladder", func(t *testing.T) {
		note := priceNoteFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 23})
		assert.Equal(t, PriceNoteUpgrade, note.Kind)
		assert.Contains(t, note.Text, "Add 1 more")
	})

	t.Run("the ladder is the fallback", func(t *testing.T) {
		note := priceNoteFor(WholesaleCheckoutItem{Ladder: testLadder(), Quantity: 2})
		assert.Equal(t, PriceNoteLadder, note.Kind)
		assert.Equal(t, "12+ $10.00 · 24+ $9.50", note.Text)
	})

	t.Run("a flat price says nothing at all", func(t *testing.T) {
		flat := domain.NewTierLadder([]domain.PriceTier{{MinQuantity: 1, Amount: 1100}})
		note := priceNoteFor(WholesaleCheckoutItem{Ladder: flat, Quantity: 5})
		assert.Equal(t, PriceNoteNone, note.Kind)
		assert.Empty(t, note.Text)
	})

	t.Run("sentences are ink, the price list is muted", func(t *testing.T) {
		// Neither note is coloured to signal itself. Amber cannot be read at this
		// size on paper, and rust means "link" everywhere else — so the upgrade
		// is marked by an amber glyph beside ink text, not by tinted words.
		assert.Equal(t, "text-ink", priceNoteClass(PriceNoteDrop))
		assert.Equal(t, "text-ink", priceNoteClass(PriceNoteUpgrade))
		assert.Equal(t, "text-ink-soft", priceNoteClass(PriceNoteLadder))
	})
}

func TestDropLine(t *testing.T) {
	d, ok := testLadder().Drop(24, 23)
	require.True(t, ok)
	// Past tense and after the fact — the reduction is already saved. No product
	// name: it renders on the line it describes.
	assert.Equal(t, "Now $10.00 each — you were getting $9.50 at 24+.", dropLine(d))
}

func TestOrderMultiple(t *testing.T) {
	six := 6
	zero := 0
	neg := -3
	assert.Equal(t, 6, orderMultiple(&six))
	assert.Equal(t, 1, orderMultiple(nil))
	assert.Equal(t, 1, orderMultiple(&zero))
	assert.Equal(t, 1, orderMultiple(&neg))
}
