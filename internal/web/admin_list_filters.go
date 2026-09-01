package web

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// Filter parsing shared by the admin list pages (orders, audit log). The date
// presets and the amount parser live here rather than in one list's file
// because the pages are meant to read the same way — "30 days" has to mean the
// same 30 days on every screen, and a second copy is how that stops being true.

// normalizeDateRange clamps ?range= to a known date preset. "custom" means the
// from/to fields drive the bounds instead. Anything unrecognized falls back to
// no date filter rather than erroring.
func normalizeDateRange(v string) string {
	switch v {
	case "today", "7d", "30d", "month", "custom":
		return v
	default:
		return ""
	}
}

// listDateBounds resolves the date filter into a half-open [from, to] pair.
//
// Boundaries are computed in the merchant's timezone, not UTC: "today" has to
// mean the shop's today, or a staffer in Denver looking at a Los Angeles shop
// sees rows drop off the list hours early. `now` is a parameter so the
// behaviour is testable without freezing the clock.
func listDateBounds(rangeKey, fromRaw, toRaw string, tz *time.Location, now time.Time) (*time.Time, *time.Time) {
	if tz == nil {
		tz = time.UTC
	}
	local := now.In(tz)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)

	switch rangeKey {
	case "today":
		return ptrTo(startOfToday), nil
	case "7d":
		return ptrTo(startOfToday.AddDate(0, 0, -6)), nil
	case "30d":
		return ptrTo(startOfToday.AddDate(0, 0, -29)), nil
	case "month":
		return ptrTo(time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, tz)), nil
	case "custom":
		var from, to *time.Time
		if t, err := time.ParseInLocation("2006-01-02", fromRaw, tz); err == nil {
			from = ptrTo(t)
		}
		if t, err := time.ParseInLocation("2006-01-02", toRaw, tz); err == nil {
			// The user means the whole of the end day, so bound at its last
			// instant rather than midnight — otherwise "to: today" silently
			// excludes everything that happened today.
			to = ptrTo(t.AddDate(0, 0, 1).Add(-time.Nanosecond))
		}
		return from, to
	}
	return nil, nil
}

// minRawDate echoes a custom date input back to the form, but only when the
// custom range is active — otherwise a stale from/to would show under a preset.
func minRawDate(raw, rangeKey string) string {
	if rangeKey != "custom" {
		return ""
	}
	if _, err := time.Parse("2006-01-02", raw); err != nil {
		return ""
	}
	return raw
}

// parseDollarFilter reads a dollar amount into cents. It returns the parsed
// value (nil when absent or unparseable) alongside the raw string, so the form
// can echo back exactly what the user typed instead of silently blanking it.
func parseDollarFilter(raw string) (*int, string) {
	raw = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "$"))
	if raw == "" {
		return nil, ""
	}
	dollars, err := strconv.ParseFloat(raw, 64)
	if err != nil || dollars < 0 {
		return nil, raw
	}
	cents := int(math.Round(dollars * 100))
	return &cents, raw
}
