package store

import (
	"encoding/json"
	"math"
	"math/big"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

// metadataFromJSON converts a JSON blob to a map. Returns nil for empty/invalid input.
func metadataFromJSON(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	return m
}

// metadataToJSON converts a map to a JSON blob suitable for INSERT params.
func metadataToJSON(m map[string]any) json.RawMessage {
	if m == nil {
		return json.RawMessage(`{}`)
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return b
}

// timestampFromPG converts a pgtype.Timestamptz to *time.Time. Returns nil if not valid.
func timestampFromPG(ts pgtype.Timestamptz) *time.Time {
	if !ts.Valid {
		return nil
	}
	return &ts.Time
}

// timestampToPG converts a *time.Time to pgtype.Timestamptz.
func timestampToPG(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{Valid: false}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// numericToFloat64 converts a pgtype.Numeric to float64. Returns 0 if not valid.
func numericToFloat64(n pgtype.Numeric) float64 {
	if !n.Valid {
		return 0
	}
	f, _ := n.Float64Value()
	return f.Float64
}

// numericToFloat64Ptr converts a pgtype.Numeric to *float64. Returns nil when
// the underlying column is NULL.
func numericToFloat64Ptr(n pgtype.Numeric) *float64 {
	if !n.Valid {
		return nil
	}
	f, _ := n.Float64Value()
	v := f.Float64
	return &v
}

// float64PtrToNumeric converts a *float64 to pgtype.Numeric. Nil produces an
// invalid (NULL) Numeric.
func float64PtrToNumeric(p *float64) pgtype.Numeric {
	if p == nil {
		return pgtype.Numeric{Valid: false}
	}
	return float64ToNumeric(*p)
}

// float64ToNumeric converts a float64 to pgtype.Numeric.
func float64ToNumeric(f float64) pgtype.Numeric {
	// Use big.Float for precise conversion
	bf := new(big.Float).SetFloat64(f)
	// Convert to big.Int with 4 decimal places of precision
	scale := int32(4)
	multiplier := new(big.Float).SetFloat64(math.Pow10(int(scale)))
	bf.Mul(bf, multiplier)
	bi, _ := bf.Int(nil)
	return pgtype.Numeric{
		Int:              bi,
		Exp:              -scale,
		Valid:            true,
		InfinityModifier: pgtype.Finite,
	}
}

// dateFromPG converts a pgtype.Date to *time.Time. Returns nil if not valid.
func dateFromPG(d pgtype.Date) *time.Time {
	if !d.Valid {
		return nil
	}
	t := d.Time
	return &t
}

// dateToPG converts a date string ("2024-01-15") to pgtype.Date.
func dateToPG(s *string) pgtype.Date {
	if s == nil || *s == "" {
		return pgtype.Date{Valid: false}
	}
	t, err := time.Parse("2006-01-02", *s)
	if err != nil {
		return pgtype.Date{Valid: false}
	}
	return pgtype.Date{Time: t, Valid: true}
}

// dateTimeToPG converts a *time.Time to pgtype.Date, keeping only the calendar
// date. Nil becomes SQL NULL.
//
// The wall-clock date is taken as-is rather than converted to UTC first: values
// reaching here are already midnight in the merchant's zone, and converting
// would push an evening-side date back a day.
func dateTimeToPG(t *time.Time) pgtype.Date {
	if t == nil {
		return pgtype.Date{Valid: false}
	}
	y, m, d := t.Date()
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

// weekdaysFromPG converts the smallint[] stored in shipping_config into
// time.Weekday values. Out-of-range entries are dropped rather than returned as
// nonsense weekdays — the column has no CHECK constraint, and a bad row should
// degrade to "no schedule" instead of resolving deliveries to a day that does
// not exist.
func weekdaysFromPG(days []int16) []time.Weekday {
	out := make([]time.Weekday, 0, len(days))
	for _, d := range days {
		if d < 0 || d > 6 {
			continue
		}
		out = append(out, time.Weekday(d))
	}
	return out
}

// weekdaysToPG converts time.Weekday values to the smallint[] representation,
// dropping duplicates and out-of-range days so the stored schedule is always
// canonical regardless of what the admin form submitted.
func weekdaysToPG(days []time.Weekday) []int16 {
	out := make([]int16, 0, len(days))
	for day := time.Sunday; day <= time.Saturday; day++ {
		for _, d := range days {
			if d == day {
				out = append(out, int16(day))
				break
			}
		}
	}
	return out
}

// int32PtrToIntPtr converts *int32 to *int.
func int32PtrToIntPtr(p *int32) *int {
	if p == nil {
		return nil
	}
	v := int(*p)
	return &v
}

// int32ToInt converts an *int32 to int. Returns 0 if nil.
func int32ToInt(p *int32) int {
	if p == nil {
		return 0
	}
	return int(*p)
}

// intPtrToInt32Ptr converts *int to *int32.
func intPtrToInt32Ptr(p *int) *int32 {
	if p == nil {
		return nil
	}
	v := int32(*p)
	return &v
}
