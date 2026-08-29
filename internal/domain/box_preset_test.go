package domain_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestSelectBoxForWeight_Empty(t *testing.T) {
	box, ok := domain.SelectBoxForWeight(nil, 10)
	assert.Nil(t, box)
	assert.False(t, ok)
}

func TestSelectBoxForWeight_SmallestThatFits(t *testing.T) {
	presets := []domain.BoxPreset{
		{Name: "small", MaxWeightOz: 8},
		{Name: "medium", MaxWeightOz: 32},
		{Name: "large", MaxWeightOz: 96},
	}
	tests := []struct {
		weight   float64
		wantName string
		wantOK   bool
	}{
		{1, "small", true},
		{8, "small", true}, // exact boundary fits
		{8.01, "medium", true},
		{32, "medium", true},
		{50, "large", true},
		{96, "large", true},
		{100, "large", false}, // exceeds largest — returns largest with ok=false
	}
	for _, tc := range tests {
		t.Run(tc.wantName, func(t *testing.T) {
			box, ok := domain.SelectBoxForWeight(presets, tc.weight)
			assert.NotNil(t, box)
			assert.Equal(t, tc.wantName, box.Name)
			assert.Equal(t, tc.wantOK, ok, "weight=%v", tc.weight)
		})
	}
}
