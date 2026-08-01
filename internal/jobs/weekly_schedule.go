package jobs

import "time"

// WeeklySchedule implements river.PeriodicSchedule for a fixed weekday and
// local time-of-day.
//
// River ships only PeriodicInterval, which drifts relative to the wall clock
// and knows nothing about time zones — a 168h interval anchored at boot would
// land on a different weekday after every deploy, and would slide an hour
// twice a year across a DST boundary. The old rr service used gocron for
// exactly this; this type is the equivalent in ~30 lines with no new
// dependency.
//
// Location is applied to the target time, so "Friday 10:00 America/Denver"
// stays 10:00 local through both MST and MDT.
type WeeklySchedule struct {
	Weekday time.Weekday
	Hour    int
	Minute  int
	Loc     *time.Location
}

// NewWeeklySchedule builds a schedule firing weekly at the given local time.
func NewWeeklySchedule(weekday time.Weekday, hour, minute int, loc *time.Location) *WeeklySchedule {
	if loc == nil {
		loc = time.UTC
	}
	return &WeeklySchedule{Weekday: weekday, Hour: hour, Minute: minute, Loc: loc}
}

// Next returns the next occurrence of the configured weekday and time strictly
// after current.
func (s *WeeklySchedule) Next(current time.Time) time.Time {
	local := current.In(s.Loc)

	// Candidate: the target time on the current local day.
	next := time.Date(local.Year(), local.Month(), local.Day(), s.Hour, s.Minute, 0, 0, s.Loc)

	// Advance to the target weekday. If today already is that weekday but the
	// time has passed, roll a full week rather than firing immediately —
	// otherwise a restart on Friday afternoon would send a second batch.
	daysAhead := (int(s.Weekday) - int(next.Weekday()) + 7) % 7
	if daysAhead == 0 && !next.After(local) {
		daysAhead = 7
	}
	// AddDate on a wall-clock date keeps the local hour fixed across DST.
	return next.AddDate(0, 0, daysAhead)
}
