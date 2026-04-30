package pirateship_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/platform/pirateship"
)

func TestDecode_MinimumColumns(t *testing.T) {
	in := "Order ID,Tracking Number,Carrier,Service,Postage Cost,Ship Date\n" +
		"RR-1001,9400111202555842761523,USPS,Ground Advantage,4.85,2026-04-30\n"

	rows, err := pirateship.Decode(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rows, 1)

	row := rows[0]
	assert.Equal(t, "RR-1001", row.OrderID)
	assert.Equal(t, "9400111202555842761523", row.TrackingNumber)
	assert.Equal(t, "USPS", row.CarrierName)
	assert.Equal(t, "Ground Advantage", row.ServiceName)
	assert.Equal(t, 485, row.PostageCostCents)
	require.NotNil(t, row.ShipDate)
	assert.Equal(t, 2026, row.ShipDate.Year())
	assert.Equal(t, time.April, row.ShipDate.Month())
	assert.Equal(t, 30, row.ShipDate.Day())
	assert.Equal(t, 1, row.LineNumber)
}

func TestDecode_MissingOrderIDFailsHard(t *testing.T) {
	in := "Tracking Number,Carrier,Ship Date\n" +
		"9400111202555842761523,USPS,2026-04-30\n"

	_, err := pirateship.Decode(strings.NewReader(in))
	require.Error(t, err)
	assert.True(t, errors.Is(err, pirateship.ErrMissingOrderIDColumn),
		"expected ErrMissingOrderIDColumn, got %v", err)
}

func TestDecode_HeaderMatchingIsCaseInsensitive(t *testing.T) {
	in := "ORDER ID,tracking number,CARRIER,Service,postage cost,SHIP DATE\n" +
		"RR-1002,1Z999AA1,UPS,Ground,12.34,2026-04-15\n"

	rows, err := pirateship.Decode(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "RR-1002", rows[0].OrderID)
	assert.Equal(t, "1Z999AA1", rows[0].TrackingNumber)
	assert.Equal(t, "UPS", rows[0].CarrierName)
	assert.Equal(t, 1234, rows[0].PostageCostCents)
}

func TestDecode_ExtraColumnsIgnored(t *testing.T) {
	in := "Order ID,Internal Note,Tracking Number,Some Extra,Ship Date\n" +
		"RR-1003,first ship,1Z999AA1,blah,2026-04-15\n"

	rows, err := pirateship.Decode(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "RR-1003", rows[0].OrderID)
	assert.Equal(t, "1Z999AA1", rows[0].TrackingNumber)
}

func TestDecode_DollarSignAndCommasInCost(t *testing.T) {
	in := "Order ID,Tracking Number,Postage Cost\n" +
		"RR-1004,A,$1,234.50\n"

	// Note: the comma is inside a quoted field, otherwise it would split.
	in = "Order ID,Tracking Number,Postage Cost\n" +
		`RR-1004,A,"$1,234.50"` + "\n"

	rows, err := pirateship.Decode(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 123450, rows[0].PostageCostCents)
}

func TestDecode_USDateFormat(t *testing.T) {
	in := "Order ID,Tracking Number,Ship Date\n" +
		"RR-1005,A,4/15/2026\n"

	rows, err := pirateship.Decode(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.NotNil(t, rows[0].ShipDate)
	assert.Equal(t, 2026, rows[0].ShipDate.Year())
	assert.Equal(t, time.April, rows[0].ShipDate.Month())
	assert.Equal(t, 15, rows[0].ShipDate.Day())
}

func TestDecode_BlankRowsSkipped(t *testing.T) {
	in := "Order ID,Tracking Number\n" +
		"RR-1001,A\n" +
		",\n" +
		"RR-1002,B\n"

	rows, err := pirateship.Decode(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Equal(t, "RR-1001", rows[0].OrderID)
	assert.Equal(t, "RR-1002", rows[1].OrderID)
}

func TestDecode_EmptyFile(t *testing.T) {
	rows, err := pirateship.Decode(strings.NewReader(""))
	require.NoError(t, err)
	assert.Nil(t, rows)
}

func TestDecode_RaggedRows(t *testing.T) {
	// Pirate Ship has been observed exporting rows with fewer columns than
	// the header (trailing empty values trimmed). Decode tolerates this.
	in := "Order ID,Tracking Number,Carrier,Ship Date\n" +
		"RR-1001,A,USPS\n" + // missing Ship Date
		"RR-1002,B,USPS,2026-04-15\n"

	rows, err := pirateship.Decode(strings.NewReader(in))
	require.NoError(t, err)
	require.Len(t, rows, 2)
	assert.Nil(t, rows[0].ShipDate)
	assert.NotNil(t, rows[1].ShipDate)
}
