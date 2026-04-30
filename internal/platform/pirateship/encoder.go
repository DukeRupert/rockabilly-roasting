// Package pirateship handles CSV format conversion for the Pirate Ship
// round-trip — encoding orders for upload, and decoding the tracking export
// that comes back. No DB, no business logic; pure format work.
package pirateship

import (
	"bytes"
	"encoding/csv"
	"fmt"
)

// ProviderCSV is the value stored in `shipments.provider` for shipments
// created from a Pirate Ship CSV import. Distinguishes them from EasyPost
// labels purchased through Hiri.
const ProviderCSV = "pirate_ship_csv"

// utf8BOM is prepended to exports so Excel renders non-ASCII characters
// correctly when staff opens the file before re-uploading to Pirate Ship.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ExportRow is one row of the Pirate Ship import CSV. Field order here is
// not significant — the column ordering is fixed in Encode().
type ExportRow struct {
	OrderID    string  // orders.number, the customer-facing identifier
	Name       string  // shipping address first + last, space-joined
	Company    string  // optional
	Address    string  // shipping line 1
	Address2   string  // optional
	City       string
	State      string
	Zipcode    string // string to preserve leading zeros (e.g. "02115")
	Country    string // 2-letter code, defaults to "US" upstream
	Email      string // optional
	WeightOz   float64
	ItemsLine  string // optional; comma-separated SKUs for the Rubber Stamp
}

// exportHeaders is the Pirate Ship column ordering. Do not reorder.
var exportHeaders = []string{
	"Order ID",
	"Name",
	"Company",
	"Address",
	"Address 2",
	"City",
	"State",
	"Zipcode",
	"Country",
	"Email",
	"Weight",
	"Weight Unit",
	"Items",
}

// Encode renders the rows as a Pirate-Ship-compatible CSV: UTF-8 with BOM,
// CRLF line endings, RFC 4180 quoting via encoding/csv. Zipcodes are written
// as-is so leading zeros survive the spreadsheet round-trip.
func Encode(rows []ExportRow) ([]byte, error) {
	var buf bytes.Buffer
	buf.Write(utf8BOM)

	w := csv.NewWriter(&buf)
	w.UseCRLF = true

	if err := w.Write(exportHeaders); err != nil {
		return nil, fmt.Errorf("write csv headers: %w", err)
	}
	for _, r := range rows {
		record := []string{
			r.OrderID,
			r.Name,
			r.Company,
			r.Address,
			r.Address2,
			r.City,
			r.State,
			r.Zipcode,
			r.Country,
			r.Email,
			fmt.Sprintf("%.2f", r.WeightOz),
			"oz",
			r.ItemsLine,
		}
		if err := w.Write(record); err != nil {
			return nil, fmt.Errorf("write csv row %s: %w", r.OrderID, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flush csv: %w", err)
	}
	return buf.Bytes(), nil
}
