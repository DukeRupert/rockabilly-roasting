package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pacific is the merchant zone the cutoff is actually evaluated in. Loading it
// (rather than using a fixed offset) is the point of several cases below: a
// fixed offset would hide the DST bugs they exist to catch.
func pacific(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	return loc
}

// monThuAt9 is the client's live configuration: van runs Monday and Thursday,
// order by 9am to make that day's run.
func monThuAt9() ShippingConfig {
	return ShippingConfig{
		LocalDeliveryEnabled:       true,
		LocalDeliveryWeekdays:      []time.Weekday{time.Monday, time.Thursday},
		LocalDeliveryCutoffMinutes: 9 * 60,
	}
}

func TestNextDeliveryDate(t *testing.T) {
	loc := pacific(t)
	cfg := monThuAt9()

	// August 2026: 10th is a Monday, 13th a Thursday, 17th the next Monday.
	at := func(month time.Month, day, hour, min int) time.Time {
		return time.Date(2026, month, day, hour, min, 0, 0, loc)
	}

	tests := []struct {
		name      string
		placedAt  time.Time
		wantMonth time.Month
		wantDay   int
	}{
		{"monday well before cutoff makes today's run", at(time.August, 10, 6, 30), time.August, 10},
		{"monday one minute before cutoff still makes it", at(time.August, 10, 8, 59), time.August, 10},
		{"monday exactly at cutoff misses it", at(time.August, 10, 9, 0), time.August, 13},
		{"monday after cutoff rolls to thursday", at(time.August, 10, 9, 1), time.August, 13},
		{"monday evening rolls to thursday", at(time.August, 10, 22, 0), time.August, 13},
		{"tuesday rolls to thursday regardless of hour", at(time.August, 11, 5, 0), time.August, 13},
		{"tuesday late evening still thursday", at(time.August, 11, 23, 59), time.August, 13},
		{"wednesday rolls to thursday", at(time.August, 12, 14, 0), time.August, 13},
		{"thursday before cutoff makes today's run", at(time.August, 13, 8, 0), time.August, 13},
		{"thursday after cutoff rolls to next monday", at(time.August, 13, 9, 30), time.August, 17},
		{"friday rolls to monday", at(time.August, 14, 10, 0), time.August, 17},
		{"saturday rolls to monday", at(time.August, 15, 12, 0), time.August, 17},
		{"sunday rolls to monday", at(time.August, 16, 20, 0), time.August, 17},
		{"sunday before dawn still rolls to monday", at(time.August, 16, 2, 0), time.August, 17},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cfg.NextDeliveryDate(tc.placedAt, loc)
			require.True(t, ok)

			assert.Equal(t, tc.wantDay, got.Day())
			assert.Equal(t, tc.wantMonth, got.Month())
			assert.Equal(t, 2026, got.Year())

			// The result is a date, so it must land on local midnight — anything
			// else would store the wrong day once truncated to a `date` column.
			assert.Equal(t, 0, got.Hour())
			assert.Equal(t, 0, got.Minute())
			assert.Equal(t, loc, got.Location())

			// Whatever comes back must actually be a day the van runs.
			assert.True(t, cfg.DeliversOn(got.Weekday()),
				"resolved %s, which is not a delivery weekday", got.Weekday())
		})
	}
}

// The cutoff is a wall-clock rule ("9am"), not an elapsed-hours rule, so it has
// to hold its local hour across a DST transition rather than sliding by one.
func TestNextDeliveryDateAcrossDSTTransitions(t *testing.T) {
	loc := pacific(t)
	cfg := monThuAt9()

	t.Run("spring forward weekend", func(t *testing.T) {
		// DST begins Sunday 2026-03-08. An order that Sunday evening is for
		// Monday the 9th — the clock change in between must not skip a day.
		got, ok := cfg.NextDeliveryDate(time.Date(2026, time.March, 8, 20, 0, 0, 0, loc), loc)
		require.True(t, ok)
		assert.Equal(t, time.March, got.Month())
		assert.Equal(t, 9, got.Day())
		assert.Equal(t, 0, got.Hour(), "must still be local midnight after the clocks moved")
	})

	t.Run("cutoff still bites the morning after springing forward", func(t *testing.T) {
		// Monday 2026-03-09, 9:30am local — the day after the transition. If the
		// comparison were done in UTC or via 24h arithmetic this would read as
		// 8:30 and wrongly make the same-day run.
		got, ok := cfg.NextDeliveryDate(time.Date(2026, time.March, 9, 9, 30, 0, 0, loc), loc)
		require.True(t, ok)
		assert.Equal(t, 12, got.Day(), "should roll to Thursday the 12th")
	})

	t.Run("fall back weekend", func(t *testing.T) {
		// DST ends Sunday 2026-11-01.
		got, ok := cfg.NextDeliveryDate(time.Date(2026, time.November, 1, 20, 0, 0, 0, loc), loc)
		require.True(t, ok)
		assert.Equal(t, 2, got.Day())
		assert.Equal(t, 0, got.Hour())
	})
}

// Placement times arrive as UTC from Postgres; the cutoff must be judged in the
// merchant's zone, not the instant's own.
func TestNextDeliveryDateConvertsIntoMerchantZone(t *testing.T) {
	loc := pacific(t)
	cfg := monThuAt9()

	// 2026-08-10 15:30 UTC is 08:30 Pacific — a Monday, before the cutoff.
	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.August, 10, 15, 30, 0, 0, time.UTC), loc)
	require.True(t, ok)
	assert.Equal(t, 10, got.Day(), "08:30 Pacific should make Monday's run")

	// 2026-08-10 16:30 UTC is 09:30 Pacific — same Monday, past the cutoff.
	got, ok = cfg.NextDeliveryDate(time.Date(2026, time.August, 10, 16, 30, 0, 0, time.UTC), loc)
	require.True(t, ok)
	assert.Equal(t, 13, got.Day(), "09:30 Pacific should roll to Thursday")

	// Late Monday Pacific is already Tuesday in UTC. Judging the date in UTC
	// would skip Monday's window entirely and still land on Thursday by luck —
	// but the reverse case (early Monday UTC) would be wrong, so assert the
	// zone conversion directly rather than trusting the coincidence.
	got, ok = cfg.NextDeliveryDate(time.Date(2026, time.August, 11, 3, 0, 0, 0, time.UTC), loc)
	require.True(t, ok)
	assert.Equal(t, 13, got.Day(), "Monday 20:00 Pacific is past cutoff → Thursday")
}

func TestNextDeliveryDateUnschedulable(t *testing.T) {
	loc := pacific(t)
	now := time.Date(2026, time.August, 11, 10, 0, 0, 0, loc)

	t.Run("no weekdays configured", func(t *testing.T) {
		cfg := ShippingConfig{LocalDeliveryEnabled: true, LocalDeliveryCutoffMinutes: 540}
		_, ok := cfg.NextDeliveryDate(now, loc)
		assert.False(t, ok)
		assert.False(t, cfg.HasDeliverySchedule())
	})

	t.Run("local delivery switched off", func(t *testing.T) {
		cfg := monThuAt9()
		cfg.LocalDeliveryEnabled = false
		_, ok := cfg.NextDeliveryDate(now, loc)
		assert.False(t, ok)
		assert.False(t, cfg.HasDeliverySchedule())
	})

	t.Run("nil location falls back to UTC rather than panicking", func(t *testing.T) {
		cfg := monThuAt9()
		got, ok := cfg.NextDeliveryDate(time.Date(2026, time.August, 11, 10, 0, 0, 0, time.UTC), nil)
		require.True(t, ok)
		assert.Equal(t, 13, got.Day())
	})
}

// A single-day schedule must roll a full week rather than returning the same
// day it just disqualified.
func TestNextDeliveryDateSingleWeekdayRollsAWeek(t *testing.T) {
	loc := pacific(t)
	cfg := ShippingConfig{
		LocalDeliveryEnabled:       true,
		LocalDeliveryWeekdays:      []time.Weekday{time.Monday},
		LocalDeliveryCutoffMinutes: 9 * 60,
	}

	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.August, 10, 11, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 17, got.Day(), "past Monday's cutoff should mean the following Monday")
	assert.Equal(t, time.Monday, got.Weekday())
}

func TestDeliveryDaysLabel(t *testing.T) {
	tests := []struct {
		name     string
		weekdays []time.Weekday
		want     string
	}{
		{"none", nil, ""},
		{"one", []time.Weekday{time.Monday}, "Mondays"},
		{"two", []time.Weekday{time.Monday, time.Thursday}, "Mondays and Thursdays"},
		{"three", []time.Weekday{time.Monday, time.Wednesday, time.Friday}, "Mondays, Wednesdays, and Fridays"},
		{"normalizes to week order", []time.Weekday{time.Thursday, time.Monday}, "Mondays and Thursdays"},
		{"sunday sorts first", []time.Weekday{time.Saturday, time.Sunday}, "Sundays and Saturdays"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := ShippingConfig{LocalDeliveryWeekdays: tc.weekdays}
			assert.Equal(t, tc.want, cfg.DeliveryDaysLabel())
		})
	}
}

func TestCutoffLabel(t *testing.T) {
	tests := []struct {
		minutes int
		want    string
	}{
		{0, "12am"},
		{9 * 60, "9am"},
		{9*60 + 30, "9:30am"},
		{11*60 + 5, "11:05am"},
		{12 * 60, "12pm"},
		{13 * 60, "1pm"},
		{16*60 + 45, "4:45pm"},
		{23 * 60, "11pm"},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			cfg := ShippingConfig{LocalDeliveryCutoffMinutes: tc.minutes}
			assert.Equal(t, tc.want, cfg.CutoffLabel())
		})
	}
}

func TestDeliveryDateLabel(t *testing.T) {
	loc := pacific(t)
	assert.Equal(t, "Thursday, August 13",
		DeliveryDateLabel(time.Date(2026, time.August, 13, 0, 0, 0, 0, loc)))
	assert.Equal(t, "Monday, September 7",
		DeliveryDateLabel(time.Date(2026, time.September, 7, 0, 0, 0, 0, loc)))
}

// laborDay2026 is Monday, September 7th 2026 — a real scheduled run day for the
// Mon/Thu route, and the case that prompted this feature. Tuesday the 8th is
// where the shop moves it to.
func laborDay2026(loc *time.Location) (holiday, movedTo time.Time) {
	return time.Date(2026, time.September, 7, 0, 0, 0, 0, loc),
		time.Date(2026, time.September, 8, 0, 0, 0, 0, loc)
}

// TestNextDeliveryDateHonoursPostponement covers the whole point of the
// feature: a holiday run answers on the day it actually happens, from every
// vantage point a customer or staffer might ask from.
//
// Without this, Labor Day is quoted at checkout, promised in the confirmation
// email, and printed on the dashboard cutoff strip — for a day the shop is shut.
func TestNextDeliveryDateHonoursPostponement(t *testing.T) {
	loc := pacific(t)
	holiday, movedTo := laborDay2026(loc)

	cfg := monThuAt9()
	cfg.DeliveryPostponements = []DeliveryPostponement{
		{OriginalDate: holiday, MovedTo: movedTo, Note: "Labor Day"},
	}

	at := func(month time.Month, day, hour, min int) time.Time {
		return time.Date(2026, month, day, hour, min, 0, 0, loc)
	}

	tests := []struct {
		name     string
		placedAt time.Time
		wantDay  int
	}{
		// Approaching the holiday, the answer is already the moved date — a
		// customer ordering Friday must not be promised Monday.
		{"friday before the holiday", at(time.September, 4, 10, 0), 8},
		{"saturday before", at(time.September, 5, 12, 0), 8},
		{"sunday before", at(time.September, 6, 20, 0), 8},

		// On the holiday itself there is no run, but the moved one is tomorrow.
		// The cutoff must not apply here: the van is not loading today.
		{"labor day morning, before what would have been the cutoff", at(time.September, 7, 6, 0), 8},
		{"labor day after the old cutoff", at(time.September, 7, 11, 0), 8},
		{"labor day evening", at(time.September, 7, 22, 0), 8},

		// Tuesday is the run. This is the case the forward-only search could
		// never answer: Tuesday is not a delivery weekday and the scheduled day
		// is behind us.
		{"tuesday before cutoff makes the moved run", at(time.September, 8, 8, 30), 8},
		{"tuesday one minute before cutoff still makes it", at(time.September, 8, 8, 59), 8},

		// Once the moved run's cutoff passes, the next scheduled day resumes.
		{"tuesday at cutoff misses it", at(time.September, 8, 9, 0), 10},
		{"tuesday afternoon rolls to thursday", at(time.September, 8, 15, 0), 10},
		{"wednesday rolls to thursday", at(time.September, 9, 9, 0), 10},

		// The following week is untouched — a postponement moves one run, not
		// the schedule.
		{"thursday after the holiday week", at(time.September, 10, 9, 30), 14},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := cfg.NextDeliveryDate(tc.placedAt, loc)
			require.True(t, ok)
			assert.Equal(t, tc.wantDay, got.Day(), "got %s", got.Format("Mon 2006-01-02"))
			assert.Equal(t, time.September, got.Month())
			assert.Equal(t, 0, got.Hour(), "must be local midnight")
			assert.Equal(t, loc, got.Location())
		})
	}
}

// A postponement must not leak into weeks it does not name. The regression this
// guards is a lookback that matches on weekday rather than on the calendar date.
func TestNextDeliveryDatePostponementIsOneRunOnly(t *testing.T) {
	loc := pacific(t)
	holiday, movedTo := laborDay2026(loc)

	cfg := monThuAt9()
	cfg.DeliveryPostponements = []DeliveryPostponement{
		{OriginalDate: holiday, MovedTo: movedTo},
	}

	// The Monday a week after the holiday is an ordinary run.
	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 14, 7, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 14, got.Day(), "the next Monday is not postponed")

	// And the Monday a week before it was too.
	got, ok = cfg.NextDeliveryDate(time.Date(2026, time.August, 31, 7, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 31, got.Day(), "the previous Monday is not postponed")
}

// Two runs moved in the same window must each answer for themselves, and the
// nearest one wins. Collecting candidates rather than returning the first hit
// is what makes this work — postponement breaks the assumption that scanning
// days in order yields run dates in order.
func TestNextDeliveryDateMultiplePostponements(t *testing.T) {
	loc := pacific(t)
	cfg := monThuAt9()
	cfg.DeliveryPostponements = []DeliveryPostponement{
		// Thursday Sep 10 pushed to Friday Sep 11.
		{OriginalDate: time.Date(2026, time.September, 10, 0, 0, 0, 0, loc),
			MovedTo: time.Date(2026, time.September, 11, 0, 0, 0, 0, loc)},
		// Monday Sep 7 pushed to Tuesday Sep 8.
		{OriginalDate: time.Date(2026, time.September, 7, 0, 0, 0, 0, loc),
			MovedTo: time.Date(2026, time.September, 8, 0, 0, 0, 0, loc)},
	}

	// Asked on the holiday, the nearer moved run (Tuesday) wins even though the
	// other postponement appears first in the slice.
	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 7, 12, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 8, got.Day())

	// Past Tuesday's cutoff, the second moved run is next — Friday, not the
	// Thursday it was scheduled for.
	got, ok = cfg.NextDeliveryDate(time.Date(2026, time.September, 8, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 11, got.Day())
}

// A run moved onto another scheduled run day collapses into it, rather than
// producing two answers or an earlier one than either.
func TestNextDeliveryDatePostponedOntoAnotherRunDay(t *testing.T) {
	loc := pacific(t)
	cfg := monThuAt9()
	// Monday Sep 7 pushed all the way to Thursday Sep 10, which already runs.
	cfg.DeliveryPostponements = []DeliveryPostponement{
		{OriginalDate: time.Date(2026, time.September, 7, 0, 0, 0, 0, loc),
			MovedTo: time.Date(2026, time.September, 10, 0, 0, 0, 0, loc)},
	}

	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 7, 12, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 10, got.Day(), "both runs land on the same day")
}

// Postponement dates arrive from a Postgres `date` column, which the driver
// hands back in UTC. The comparison has to be by calendar date, not by instant,
// or a merchant west of Greenwich matches the wrong day.
func TestNextDeliveryDatePostponementFromUTCDate(t *testing.T) {
	loc := pacific(t)
	cfg := monThuAt9()
	cfg.DeliveryPostponements = []DeliveryPostponement{{
		OriginalDate: time.Date(2026, time.September, 7, 0, 0, 0, 0, time.UTC),
		MovedTo:      time.Date(2026, time.September, 8, 0, 0, 0, 0, time.UTC),
	}}

	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 6, 12, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 8, got.Day())
	assert.Equal(t, loc, got.Location(), "the answer is rebuilt in the merchant zone")
	assert.Equal(t, 0, got.Hour())
}

// A plain weekday schedule guarantees the next run inside a week, and the
// forward search window was sized to that. Postponement voids the guarantee:
// push two consecutive runs far enough out and the true next run sits beyond
// day seven, where the old window never looked — so the function answered with
// a run eleven days later than the real one, over-promising the wait at
// checkout and misdating every order placed in between.
func TestNextDeliveryDateFindsRunBeyondAWeek(t *testing.T) {
	loc := pacific(t)
	cfg := monThuAt9()

	// Both runs in the week of the 14th pushed to the far end of their windows.
	cfg.DeliveryPostponements = []DeliveryPostponement{
		{OriginalDate: time.Date(2026, time.September, 14, 0, 0, 0, 0, loc),
			MovedTo: time.Date(2026, time.September, 25, 0, 0, 0, 0, loc)},
		{OriginalDate: time.Date(2026, time.September, 17, 0, 0, 0, 0, loc),
			MovedTo: time.Date(2026, time.September, 30, 0, 0, 0, 0, loc)},
	}

	// Asked on Thursday the 10th after its cutoff. The 14th and 17th are both
	// pushed past it, so the real next run is Monday the 21st — which is eleven
	// days out and only reachable because the window now reaches that far.
	got, ok := cfg.NextDeliveryDate(time.Date(2026, time.September, 10, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 21, got.Day(), "got %s", got.Format("Mon 2006-01-02"))
	assert.Equal(t, time.September, got.Month())
}

// NextDeliveryRun returns the run's identity alongside its date. Order
// placement stores both, because once two runs can share a day the date alone
// cannot say which run an order rides.
func TestNextDeliveryRunReportsScheduledDay(t *testing.T) {
	loc := pacific(t)
	holiday, movedTo := laborDay2026(loc)

	cfg := monThuAt9()
	cfg.DeliveryPostponements = []DeliveryPostponement{{OriginalDate: holiday, MovedTo: movedTo}}

	// Ordering the Friday before the holiday: promised Tuesday, riding Monday's run.
	scheduled, effective, ok := cfg.NextDeliveryRun(time.Date(2026, time.September, 4, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, 7, scheduled.Day(), "the run is still Monday's")
	assert.Equal(t, 8, effective.Day(), "it just goes out on Tuesday")

	// With nothing postponed the two agree.
	plain := monThuAt9()
	scheduled, effective, ok = plain.NextDeliveryRun(time.Date(2026, time.September, 4, 10, 0, 0, 0, loc), loc)
	require.True(t, ok)
	assert.Equal(t, scheduled, effective)
	assert.Equal(t, 7, scheduled.Day())
}
