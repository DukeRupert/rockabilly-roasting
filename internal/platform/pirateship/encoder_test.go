package pirateship_test

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/pirateship"
)

func TestEncode_HasBOMAndCRLF(t *testing.T) {
	out, err := pirateship.Encode(nil)
	require.NoError(t, err)
	assert.True(t, bytes.HasPrefix(out, []byte{0xEF, 0xBB, 0xBF}), "expected UTF-8 BOM prefix")
	assert.Contains(t, string(out), "\r\n", "expected CRLF line endings")
}

func TestEncode_HeadersInExactOrder(t *testing.T) {
	out, err := pirateship.Encode(nil)
	require.NoError(t, err)

	// Strip BOM and parse the first row.
	body := bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	r := csv.NewReader(bytes.NewReader(body))
	headers, err := r.Read()
	require.NoError(t, err)

	want := []string{
		"Order ID", "Name", "Company", "Address", "Address 2",
		"City", "State", "Zipcode", "Country", "Email",
		"Weight", "Weight Unit", "Items",
	}
	assert.Equal(t, want, headers)
}

func TestEncode_SingleRow(t *testing.T) {
	rows := []pirateship.ExportRow{{
		OrderID:   "RR-1001",
		Name:      "Jane Smith",
		Address:   "742 Evergreen Terrace",
		City:      "Springfield",
		State:     "IL",
		Zipcode:   "62701",
		Country:   "US",
		Email:     "jane@example.com",
		WeightOz:  14.50,
		ItemsLine: "RB-12OZ-WHOLE",
	}}

	out, err := pirateship.Encode(rows)
	require.NoError(t, err)

	body := bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	require.NoError(t, err)
	require.Len(t, records, 2) // header + 1 row

	row := records[1]
	assert.Equal(t, "RR-1001", row[0])
	assert.Equal(t, "Jane Smith", row[1])
	assert.Equal(t, "", row[2]) // Company empty
	assert.Equal(t, "742 Evergreen Terrace", row[3])
	assert.Equal(t, "62701", row[7])
	assert.Equal(t, "US", row[8])
	assert.Equal(t, "14.50", row[10]) // formatted to 2dp
	assert.Equal(t, "oz", row[11])
}

func TestEncode_LeadingZeroZipPreserved(t *testing.T) {
	rows := []pirateship.ExportRow{{
		OrderID: "RR-1002",
		Name:    "Pat Jones",
		Address: "1 Beacon St",
		City:    "Boston",
		State:   "MA",
		Zipcode: "02115", // leading zero
		Country: "US",
		WeightOz: 12.0,
	}}

	out, err := pirateship.Encode(rows)
	require.NoError(t, err)

	// Round-trip via the csv reader to confirm the zip is intact.
	body := bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	require.NoError(t, err)
	assert.Equal(t, "02115", records[1][7])

	// Sanity: the raw bytes still contain the leading zero (no Excel-style
	// scientific notation or stripping). The csv writer does not quote
	// purely numeric strings, but it preserves them verbatim.
	assert.Contains(t, string(out), "02115")
}

func TestEncode_QuotesFieldsWithCommasAndQuotes(t *testing.T) {
	rows := []pirateship.ExportRow{{
		OrderID:  "RR-1003",
		Name:     `O'Brien, Patrick "Pat"`,
		Address:  "1 Main St, Apt 2",
		City:     "Anytown",
		State:    "CA",
		Zipcode:  "90210",
		Country:  "US",
		WeightOz: 8.0,
	}}

	out, err := pirateship.Encode(rows)
	require.NoError(t, err)

	// encoding/csv handles RFC 4180 quoting: commas and quotes inside fields
	// get the surrounding "..." with embedded quotes doubled.
	s := string(out)
	assert.Contains(t, s, `"O'Brien, Patrick ""Pat"""`)
	assert.Contains(t, s, `"1 Main St, Apt 2"`)

	// Round-trip: the reader should give us the original strings back.
	body := bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	records, err := csv.NewReader(bytes.NewReader(body)).ReadAll()
	require.NoError(t, err)
	assert.Equal(t, `O'Brien, Patrick "Pat"`, records[1][1])
	assert.Equal(t, "1 Main St, Apt 2", records[1][3])
}

func TestEncode_EmptyRowsStillWritesHeaders(t *testing.T) {
	out, err := pirateship.Encode([]pirateship.ExportRow{})
	require.NoError(t, err)

	body := bytes.TrimPrefix(out, []byte{0xEF, 0xBB, 0xBF})
	// One header line, no rows.
	lines := strings.Count(string(body), "\r\n")
	assert.Equal(t, 1, lines)
}
