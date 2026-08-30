package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// dueRow builds a pending occurrence due on a day, with a fortnight's notice.
func dueRow(due time.Time) domain.MaintenanceDueRow {
	return domain.MaintenanceDueRow{
		MaintenanceDue: domain.MaintenanceDue{
			DueOn:  due,
			Status: domain.MaintenanceStatusPending,
		},
		IntervalDays: 90,
		LeadDays:     14,
	}
}

func TestMaintenanceUrgency(t *testing.T) {
	today := day(2026, time.September, 1)

	tests := []struct {
		name string
		due  time.Time
		want domain.MaintenanceUrgency
	}{
		{"yesterday is overdue", day(2026, time.August, 31), domain.MaintenanceOverdue},
		{"today is due, not late", today, domain.MaintenanceDueSoon},
		{"inside the lead window", day(2026, time.September, 10), domain.MaintenanceDueSoon},
		{"the last day of the window still counts", day(2026, time.September, 15), domain.MaintenanceDueSoon},
		{"a day past it is merely upcoming", day(2026, time.September, 16), domain.MaintenanceUpcoming},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, dueRow(tc.due).Urgency(today))
		})
	}
}

// A task due today must read as due for the whole of today. The regression this
// guards is comparing a `date` (midnight UTC) against an instant, which flips a
// row to overdue the moment the clock passes midnight in the wrong zone.
func TestMaintenanceUrgencyIgnoresTimeOfDay(t *testing.T) {
	row := dueRow(day(2026, time.September, 1))
	lateInTheDay := time.Date(2026, time.September, 1, 23, 45, 0, 0, time.UTC)

	assert.Equal(t, domain.MaintenanceDueSoon, row.Urgency(lateInTheDay),
		"a task due today is not late until tomorrow")
}

func TestMaintenanceDaysUntil(t *testing.T) {
	today := day(2026, time.September, 1)

	assert.Equal(t, 0, dueRow(today).DaysUntil(today))
	assert.Equal(t, 7, dueRow(day(2026, time.September, 8)).DaysUntil(today))
	assert.Equal(t, -11, dueRow(day(2026, time.August, 21)).DaysUntil(today),
		"overdue counts backwards, which is what the row says out loud")
}

func TestWarrantyAtRisk(t *testing.T) {
	today := day(2026, time.September, 1)
	stillCovered := day(2027, time.January, 1)
	expired := day(2026, time.January, 1)

	overdue := func() domain.MaintenanceDueRow {
		r := dueRow(day(2026, time.August, 1))
		r.WarrantyRequired = true
		r.EquipmentWarrantyEnds = &stillCovered
		return r
	}

	t.Run("overdue warranty task inside the warranty", func(t *testing.T) {
		assert.True(t, overdue().WarrantyAtRisk(today))
	})

	t.Run("not yet overdue is not at risk", func(t *testing.T) {
		r := overdue()
		r.DueOn = day(2026, time.September, 10)
		assert.False(t, r.WarrantyAtRisk(today), "a task inside its window is still on time")
	})

	t.Run("the warranty has already run out", func(t *testing.T) {
		r := overdue()
		r.EquipmentWarrantyEnds = &expired
		assert.False(t, r.WarrantyAtRisk(today), "there is no cover left to lose")
	})

	t.Run("no warranty was ever recorded", func(t *testing.T) {
		r := overdue()
		r.EquipmentWarrantyEnds = nil
		assert.False(t, r.WarrantyAtRisk(today), "an unknown warranty is not a warranty")
	})

	t.Run("the task does not affect the warranty", func(t *testing.T) {
		r := overdue()
		r.WarrantyRequired = false
		assert.False(t, r.WarrantyAtRisk(today))
	})

	t.Run("already done", func(t *testing.T) {
		r := overdue()
		r.Status = domain.MaintenanceStatusCompleted
		assert.False(t, r.WarrantyAtRisk(today))
	})
}

func TestMaintenanceCoverage(t *testing.T) {
	today := day(2026, time.September, 1)
	lastMonth := day(2026, time.August, 1)
	nextYear := day(2027, time.September, 1)

	t.Run("no contract", func(t *testing.T) {
		assert.False(t, dueRow(today).Covered(today))
	})

	t.Run("open-ended contract", func(t *testing.T) {
		r := dueRow(today)
		r.UnderContract = true
		assert.True(t, r.Covered(today))
	})

	t.Run("contract still running", func(t *testing.T) {
		r := dueRow(today)
		r.UnderContract = true
		r.ContractEndsOn = &nextYear
		assert.True(t, r.Covered(today))
	})

	t.Run("contract has lapsed", func(t *testing.T) {
		r := dueRow(today)
		r.UnderContract = true
		r.ContractEndsOn = &lastMonth
		assert.False(t, r.Covered(today), "a contract that has run out is not a contract")
	})

	t.Run("the last day of the contract is covered", func(t *testing.T) {
		r := dueRow(today)
		r.UnderContract = true
		end := today
		r.ContractEndsOn = &end
		assert.True(t, r.Covered(today))
	})
}

// The whole contract split: covered work books itself, uncovered work gets sold.
func TestBookableAndSellable(t *testing.T) {
	today := day(2026, time.September, 1)

	covered := func() domain.MaintenanceDueRow {
		r := dueRow(day(2026, time.September, 5))
		r.UnderContract = true
		return r
	}

	t.Run("covered work inside the window is bookable", func(t *testing.T) {
		assert.True(t, covered().BookableOn(today))
		assert.False(t, covered().NeedsSelling(today), "nobody needs ringing about work they already pay for")
	})

	t.Run("uncovered work is never booked automatically", func(t *testing.T) {
		r := covered()
		r.UnderContract = false
		assert.False(t, r.BookableOn(today),
			"opening a ticket would commit the shop to a visit nobody agreed to pay for")
		assert.True(t, r.NeedsSelling(today))
	})

	t.Run("already booked", func(t *testing.T) {
		r := covered()
		ticket := domain.SystemActor.ID
		r.TicketID = &ticket
		assert.False(t, r.BookableOn(today))
	})

	t.Run("too far out", func(t *testing.T) {
		r := covered()
		r.DueOn = day(2026, time.December, 1)
		assert.False(t, r.BookableOn(today))
		assert.False(t, r.NeedsSelling(today), "the call list is work that is actually near")
	})

	t.Run("retired machine", func(t *testing.T) {
		r := covered()
		r.EquipmentStatus = domain.EquipmentStatusRetired
		assert.False(t, r.BookableOn(today), "a machine that is gone is not getting a visit")
	})
}

// Completion re-anchors the schedule to when the work happened. A machine
// serviced three weeks late is not due again three weeks early.
func TestNextDueAfterCompletion(t *testing.T) {
	completed := day(2026, time.September, 20)

	assert.Equal(t, day(2026, time.December, 19), domain.NextDueAfterCompletion(completed, 90))
}

// Skipping keeps the original cadence — one missed backflush must not shift
// every future one forward.
func TestNextDueAfterSkip(t *testing.T) {
	t.Run("keeps the cadence", func(t *testing.T) {
		due := day(2026, time.September, 1)
		today := day(2026, time.September, 5)

		assert.Equal(t, day(2026, time.October, 1), domain.NextDueAfterSkip(due, today, 30),
			"the next one is an interval after the date it was due, not after today")
	})

	t.Run("a badly overdue skip lands in the future", func(t *testing.T) {
		due := day(2026, time.January, 1)
		today := day(2026, time.September, 1)

		next := domain.NextDueAfterSkip(due, today, 30)

		assert.True(t, next.After(today),
			"skipping must clear the row — a skip that produced another overdue one could never be worked off")
		assert.Equal(t, day(2026, time.September, 28), next)
	})
}

// The first occurrence is deliberately not pushed into the future: assigning a
// plan with an old anchor is how a shop discovers a machine is overdue.
func TestFirstDueOn(t *testing.T) {
	anchor := day(2024, time.March, 1)

	assert.Equal(t, day(2025, time.March, 1), domain.FirstDueOn(anchor, 365),
		"an anchor two years back produces an overdue first occurrence, which is the finding")
}

func TestServicePlanTaskIntervalLabel(t *testing.T) {
	tests := []struct {
		days int
		want string
	}{
		{7, "Weekly"},
		{30, "Monthly"},
		{90, "Quarterly"},
		{365, "Yearly"},
		{45, "Every 45 days"},
		{1, "Daily"},
	}

	for _, tc := range tests {
		task := domain.ServicePlanTask{IntervalDays: tc.days}
		assert.Equal(t, tc.want, task.IntervalLabel())
	}
}

func TestEquipmentServicePlanLive(t *testing.T) {
	ended := time.Date(2026, time.August, 1, 12, 0, 0, 0, time.UTC)

	assert.True(t, domain.EquipmentServicePlan{}.Live())
	assert.False(t, domain.EquipmentServicePlan{EndedAt: &ended}.Live())
}
