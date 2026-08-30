package jobs

import "time"

// DailySchedule implements river.PeriodicSchedule for a fixed local
// time-of-day, every day.
//
// The daily sibling of WeeklySchedule, and it exists for the same reason:
// river.PeriodicInterval(24*time.Hour) anchors to process start, so the hour a
// "daily" job runs at is whatever time the last deploy happened to be. That is
// tolerable for a job that only reads. It is not tolerable for the maintenance
// sweep, which opens real customer tickets — the module's documentation
// promises covered work books itself "overnight", and an interval anchored to a
// mid-afternoon deploy would instead do it mid-afternoon, every day, until the
// next restart moved it somewhere else.
//
// Location is applied to the target time, so "02:00 America/Denver" stays 02:00
// local through both MST and MDT.
type DailySchedule struct {
	Hour   int
	Minute int
	Loc    *time.Location
}

// NewDailySchedule builds a schedule firing every day at the given local time.
func NewDailySchedule(hour, minute int, loc *time.Location) *DailySchedule {
	if loc == nil {
		loc = time.UTC
	}
	return &DailySchedule{Hour: hour, Minute: minute, Loc: loc}
}

// Next returns the next occurrence of the configured time strictly after
// current.
func (s *DailySchedule) Next(current time.Time) time.Time {
	local := current.In(s.Loc)

	next := time.Date(local.Year(), local.Month(), local.Day(), s.Hour, s.Minute, 0, 0, s.Loc)
	// Already gone today: roll to tomorrow rather than firing immediately, so a
	// restart after the daily run does not repeat it. AddDate on a wall-clock
	// date keeps the local hour fixed across a DST boundary.
	if !next.After(local) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}
