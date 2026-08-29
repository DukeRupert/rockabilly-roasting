package admin

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

// One button per row, showing the one obvious next move. A part that is fitted
// has nowhere left to go.
func TestPartRowsOfferOneMoveEach(t *testing.T) {
	parts := []domain.ServicePart{
		{ID: uuid.New(), Status: domain.ServicePartStatusNeeded},
		{ID: uuid.New(), Status: domain.ServicePartStatusOrdered},
		{ID: uuid.New(), Status: domain.ServicePartStatusReceived},
		{ID: uuid.New(), Status: domain.ServicePartStatusInstalled},
	}

	rows := PartRowsFrom(parts)
	require.Len(t, rows, 4)

	assert.Equal(t, domain.ServicePartStatusOrdered, rows[0].NextStatus)
	assert.Equal(t, domain.ServicePartStatusReceived, rows[1].NextStatus)
	assert.Equal(t, domain.ServicePartStatusInstalled, rows[2].NextStatus)
	assert.Equal(t, domain.ServicePartStatus(""), rows[3].NextStatus,
		"a fitted part is the end of the line")
}

// The button says what you are recording, not what state it lands in.
func TestPartAdvanceLabelsReadAsActions(t *testing.T) {
	assert.Equal(t, "Ordered it", partAdvanceLabel(domain.ServicePartStatusOrdered))
	assert.Equal(t, "It arrived", partAdvanceLabel(domain.ServicePartStatusReceived))
	assert.Equal(t, "Fitted it", partAdvanceLabel(domain.ServicePartStatusInstalled))
	assert.Empty(t, partAdvanceLabel(""))
}

// The ordered → arrived → fitted trail is what the feature was asked for, so a
// partly-filled trail still shows what it has.
func TestPartDatesLineShowsWhateverIsKnown(t *testing.T) {
	ordered := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	received := time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC)

	assert.Empty(t, partDatesLine(domain.ServicePart{}, time.UTC),
		"a part nobody has ordered yet has no trail to show")

	half := partDatesLine(domain.ServicePart{OrderedOn: &ordered}, time.UTC)
	assert.Contains(t, half, "ordered 10 Aug 2026")
	assert.NotContains(t, half, "arrived")

	full := partDatesLine(domain.ServicePart{OrderedOn: &ordered, ReceivedOn: &received}, time.UTC)
	assert.Contains(t, full, "ordered 10 Aug 2026")
	assert.Contains(t, full, "arrived 17 Aug 2026")
}

func TestPartSourceLineJoinsWhatIsThere(t *testing.T) {
	assert.Equal(t, "LM-GK-8 · Espresso Parts",
		partSourceLine(domain.ServicePart{PartNumber: "LM-GK-8", Supplier: "Espresso Parts"}))
	assert.Equal(t, "LM-GK-8", partSourceLine(domain.ServicePart{PartNumber: "LM-GK-8"}))
	assert.Equal(t, "Espresso Parts", partSourceLine(domain.ServicePart{Supplier: "Espresso Parts"}))
	assert.Empty(t, partSourceLine(domain.ServicePart{}))
}

// Read-only staff see the record and none of the controls.
func TestTicketShowHidesPartAndTimeControlsFromReadOnlyStaff(t *testing.T) {
	part := domain.ServicePart{ID: uuid.New(), Name: "Group head gasket", Quantity: 2, UnitCostCents: 425, Status: domain.ServicePartStatusOrdered}
	entry := domain.ServiceTimeEntry{ID: uuid.New(), Minutes: 90, Kind: domain.ServiceTimeKindLabor, PerformedOn: time.Now()}

	props := ServiceTicketShowProps{
		Ticket:       sampleTicket(),
		CustomerName: "Bunker Coffee",
		Parts:        PartRowsFrom([]domain.ServicePart{part}),
		TimeEntries:  []TimeEntryRow{{Entry: entry, StaffName: "Logan"}},
		Totals:       domain.ServiceTotals{PartsCostCents: 850, LaborMinutes: 90},
		CanWrite:     false,
	}
	html := renderTicketShow(t, props)

	// The facts stay visible.
	assert.Contains(t, html, "Group head gasket")
	assert.Contains(t, html, "1h 30m")
	// The controls do not.
	assert.NotContains(t, html, "Ordered it")
	assert.NotContains(t, html, "Remove")
	assert.NotContains(t, html, "Log</button>")

	props.CanWrite = true
	writable := renderTicketShow(t, props)
	assert.Contains(t, writable, "It arrived")
	assert.Contains(t, writable, "Remove")
}

func TestTicketShowEmptyPartsAndHoursSayNothingIsThere(t *testing.T) {
	html := renderTicketShow(t, ServiceTicketShowProps{
		Ticket: sampleTicket(), CustomerName: "Bunker Coffee", CanWrite: true,
	})

	assert.Contains(t, html, "No parts on this ticket")
	assert.Contains(t, html, "No time logged on this ticket")
}

func TestHoursLineSeparatesLoggedFromBillable(t *testing.T) {
	props := ServiceTicketShowProps{
		Totals: domain.ServiceTotals{LaborMinutes: 90, TravelMinutes: 45, BillableMinutes: 90},
	}
	// Travel counts toward what the job cost even when it is not billed.
	assert.Equal(t, "2h 15m logged · 1h 30m billable", props.hoursLine())

	assert.Equal(t, "No time logged yet.", ServiceTicketShowProps{}.hoursLine())
}
