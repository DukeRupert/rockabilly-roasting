package app

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func TestEffectivePaymentTermsDays(t *testing.T) {
	days := func(n int) *int { return &n }

	tests := []struct {
		name     string
		customer *domain.Customer
		want     int
	}{
		{"nil customer defaults to net-7", nil, 7},
		{"unset terms default to net-7", &domain.Customer{}, 7},
		{"zero terms default to net-7", &domain.Customer{PaymentTermsDays: days(0)}, 7},
		{"negative terms default to net-7", &domain.Customer{PaymentTermsDays: days(-3)}, 7},
		{"explicit net-14 wins", &domain.Customer{PaymentTermsDays: days(14)}, 14},
		{"explicit net-30 wins", &domain.Customer{PaymentTermsDays: days(30)}, 30},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, EffectivePaymentTermsDays(tt.customer))
		})
	}
}

func TestOverdueReminderStageFor(t *testing.T) {
	tests := []struct {
		name        string
		daysPastDue int
		want        int
	}{
		{"not yet due", -1, 0},
		{"due day itself counts as first reminder", 0, 1},
		{"day 1 past due", 1, 1},
		{"day 6 still first stage", 6, 1},
		{"week 2", 7, 2},
		{"week 3", 14, 3},
		{"week 4", 21, 4},
		{"capped at four reminders", 28, 4},
		{"still capped months later", 90, 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, overdueReminderStageFor(tt.daysPastDue))
		})
	}
}
