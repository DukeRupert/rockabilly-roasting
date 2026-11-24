package domain

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pickPool(n int) []PickCandidate {
	pool := make([]PickCandidate, n)
	for i := range pool {
		pool[i] = PickCandidate{ProductID: uuid.New(), Title: string(rune('A' + i))}
	}
	return pool
}

func countDistinct(ids []uuid.UUID) int {
	seen := map[uuid.UUID]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	return len(seen)
}

func TestSuggestPicks_DeterministicPerSeed(t *testing.T) {
	pool := pickPool(8)
	seed := uuid.New()

	a := SuggestPicks(seed, 4, pool, nil)
	b := SuggestPicks(seed, 4, pool, nil)
	assert.Equal(t, a, b, "same seed and pool must give the same picks, so a reload does not reshuffle")

	// A different seed should, across a handful of tries, give a different
	// hand; otherwise every customer gets the same box.
	differs := false
	for range 10 {
		if c := SuggestPicks(uuid.New(), 4, pool, nil); !assert.ObjectsAreEqual(a, c) {
			differs = true
			break
		}
	}
	assert.True(t, differs, "suggestions should vary between line items")
}

func TestSuggestPicks_NoRepeatsUntilPoolExhausted(t *testing.T) {
	pool := pickPool(4)

	got := SuggestPicks(uuid.New(), 4, pool, nil)
	assert.Equal(t, 4, countDistinct(got), "a 4-bag box from 4 coffees uses each once")

	got = SuggestPicks(uuid.New(), 6, pool, nil)
	require.Len(t, got, 6)
	assert.Equal(t, 4, countDistinct(got), "a 6-bag box from 4 coffees uses all 4 before repeating")
}

func TestSuggestPicks_AvoidsPreviousBoxWhenPossible(t *testing.T) {
	pool := pickPool(6)
	avoid := map[uuid.UUID]bool{pool[0].ProductID: true, pool[1].ProductID: true, pool[2].ProductID: true}

	for range 20 {
		got := SuggestPicks(uuid.New(), 3, pool, avoid)
		for _, id := range got {
			assert.False(t, avoid[id], "with three fresh coffees on the shelf, last box's coffees stay out")
		}
	}

	// Five bags but only three fresh coffees: all three fresh ones go in, and
	// the remaining two come from last box rather than repeating a fresh one.
	got := SuggestPicks(uuid.New(), 5, pool, avoid)
	fresh, stale := 0, 0
	for _, id := range got {
		if avoid[id] {
			stale++
		} else {
			fresh++
		}
	}
	assert.Equal(t, 3, fresh)
	assert.Equal(t, 2, stale)
	assert.Equal(t, 5, countDistinct(got))
}

func TestSuggestPicks_EmptyInputs(t *testing.T) {
	assert.Nil(t, SuggestPicks(uuid.New(), 3, nil, nil))
	assert.Nil(t, SuggestPicks(uuid.New(), 0, pickPool(3), nil))
}

func TestFilterPickCandidates_ExcludesDecafAndSorts(t *testing.T) {
	decaf := PickCandidate{ProductID: uuid.New(), Title: "Swiss Water Decaf", IsDecaf: true}
	pool := []PickCandidate{
		{ProductID: uuid.New(), Title: "rebel blend"},
		decaf,
		{ProductID: uuid.New(), Title: "Cloud 9"},
	}

	got := FilterPickCandidates(pool, PickRule{ExcludeDecaf: true})
	require.Len(t, got, 2)
	assert.Equal(t, "Cloud 9", got[0].Title)
	assert.Equal(t, "rebel blend", got[1].Title)

	got = FilterPickCandidates(pool, PickRule{})
	assert.Len(t, got, 3, "decaf stays in when the pack allows it")
}
