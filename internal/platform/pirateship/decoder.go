package pirateship

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// ErrMissingOrderIDColumn is returned when Decode cannot find an "Order ID"
// header in the uploaded CSV. The whole file is rejected — without that
// column we have no way to associate tracking with orders.
var ErrMissingOrderIDColumn = errors.New("pirateship: csv has no \"Order ID\" column")

// TrackingRow is one row of a Pirate Ship tracking export. Fields not present
// in the source CSV land here as their zero values; callers are responsible
// for treating those as missing data when applying the row to a shipment.
type TrackingRow struct {
	OrderID         string
	TrackingNumber  string
	CarrierName     string
	ServiceName     string
	PostageCostCents int
	ShipDate        *time.Time
	// LineNumber is the 1-indexed position of this row in the source file
	// (header row excluded). Useful for error reporting back to the operator.
	LineNumber int
}

// dateFormats lists the layouts Decode will try on Pirate Ship "Ship Date"
// values, in priority order. Pirate Ship ships dates in either ISO format or
// US-locale `M/D/YYYY`; we accept both rather than guessing.
var dateFormats = []string{
	"2006-01-02",
	"1/2/2006",
	"01/02/2006",
	"2006-01-02 15:04:05",
	"1/2/2006 15:04",
	time.RFC3339,
}

// Decode reads a Pirate Ship tracking export and returns one TrackingRow per
// data row. Header matching is case-insensitive and tolerates extra columns;
// the only hard requirement is an "Order ID" column. Rows with malformed
// numeric or date fields fall back to zero values rather than aborting the
// whole import — the call site decides how strict to be per row.
func Decode(r io.Reader) ([]TrackingRow, error) {
	cr := csv.NewReader(r)
	cr.FieldsPerRecord = -1 // tolerate ragged rows
	cr.TrimLeadingSpace = true

	headers, err := cr.Read()
	if err == io.EOF {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read header row: %w", err)
	}

	idx := indexHeaders(headers)
	if _, ok := idx["order id"]; !ok {
		return nil, ErrMissingOrderIDColumn
	}

	var out []TrackingRow
	lineNo := 0
	for {
		record, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read row %d: %w", lineNo+1, err)
		}
		lineNo++

		row := TrackingRow{LineNumber: lineNo}
		row.OrderID = strings.TrimSpace(field(record, idx, "order id"))
		if row.OrderID == "" {
			// A blank Order ID makes the row useless; skip it silently rather
			// than fail the whole file. Pirate Ship sometimes leaves trailing
			// blank rows in exports.
			continue
		}

		row.TrackingNumber = strings.TrimSpace(field(record, idx, "tracking number", "tracking", "tracking #"))
		row.CarrierName = strings.TrimSpace(field(record, idx, "carrier", "provider"))
		row.ServiceName = strings.TrimSpace(field(record, idx, "service", "service name", "carrier service"))

		if costStr := strings.TrimSpace(field(record, idx, "postage cost", "cost", "postage", "amount paid")); costStr != "" {
			row.PostageCostCents = parseDollarsToCents(costStr)
		}

		if dateStr := strings.TrimSpace(field(record, idx, "ship date", "shipped on", "date")); dateStr != "" {
			if t := parseDate(dateStr); !t.IsZero() {
				row.ShipDate = &t
			}
		}

		out = append(out, row)
	}
	return out, nil
}

// indexHeaders builds a lowercase header → column-index map. Duplicate
// headers (case-insensitive) keep the leftmost column.
func indexHeaders(headers []string) map[string]int {
	idx := make(map[string]int, len(headers))
	for i, h := range headers {
		key := strings.ToLower(strings.TrimSpace(h))
		if key == "" {
			continue
		}
		if _, exists := idx[key]; !exists {
			idx[key] = i
		}
	}
	return idx
}

// field returns the value of the first matching header (case-insensitive),
// or empty string if none of the candidates are present.
func field(record []string, idx map[string]int, candidates ...string) string {
	for _, c := range candidates {
		if i, ok := idx[strings.ToLower(c)]; ok && i < len(record) {
			return record[i]
		}
	}
	return ""
}

// parseDollarsToCents converts a dollar string ("$3.45", "3.45", "3") into
// integer cents. Any leading currency symbol or whitespace is stripped.
// Returns 0 on parse failure — callers can decide whether to error.
func parseDollarsToCents(s string) int {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	// Round half-away-from-zero to avoid 0.295 → 29 truncation.
	cents := f * 100
	if cents >= 0 {
		return int(cents + 0.5)
	}
	return int(cents - 0.5)
}

// parseDate attempts each known layout in order and returns the first match.
// Returns the zero Time if no layout fits.
func parseDate(s string) time.Time {
	for _, layout := range dateFormats {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
