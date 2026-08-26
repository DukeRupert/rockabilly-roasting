package admin

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func renderEquipmentList(t *testing.T, props EquipmentListProps) string {
	t.Helper()
	var sb strings.Builder
	require.NoError(t, EquipmentListContent(props).Render(context.Background(), &sb))
	return sb.String()
}

func renderEquipmentShow(t *testing.T, props EquipmentShowProps) string {
	t.Helper()
	var sb strings.Builder
	require.NoError(t, EquipmentShowContent(props).Render(context.Background(), &sb))
	return sb.String()
}

func sampleEquipment() domain.Equipment {
	return domain.Equipment{
		ID:           uuid.New(),
		CustomerID:   uuid.New(),
		Category:     domain.EquipmentCategoryEspressoMachine,
		Make:         "La Marzocco",
		Model:        "Linea PB",
		SerialNumber: "LM-99172",
		Ownership:    domain.EquipmentOwnershipCustomer,
		Status:       domain.EquipmentStatusActive,
	}
}

// Read-only staff must not be shown controls whose submit the server would
// refuse. The route is gated independently; this keeps the page honest.
func TestEquipmentListHidesAddForReadOnlyStaff(t *testing.T) {
	props := EquipmentListProps{
		Equipment: []domain.EquipmentWithCustomer{{Equipment: sampleEquipment(), CustomerName: "Bunker Coffee"}},
		CanWrite:  false,
	}
	html := renderEquipmentList(t, props)

	assert.NotContains(t, html, "Add a machine")
	assert.Contains(t, html, "Bunker Coffee")

	props.CanWrite = true
	assert.Contains(t, renderEquipmentList(t, props), "Add a machine")
}

// An empty register has to say which kind of empty it is, or staff cannot tell
// a filter that matched nothing from a shop with no machines on file.
func TestEquipmentListEmptyStatesDiffer(t *testing.T) {
	blank := renderEquipmentList(t, EquipmentListProps{CanWrite: true})
	assert.Contains(t, blank, "No machines on the register yet")
	assert.Contains(t, blank, "Add the first one")

	filtered := renderEquipmentList(t, EquipmentListProps{CanWrite: true, Search: "linea"})
	assert.Contains(t, filtered, "No machines match those filters")
	assert.NotContains(t, filtered, "Add the first one")
}

func TestEquipmentShowRendersEveryStatusAxis(t *testing.T) {
	e := sampleEquipment()
	e.Ownership = domain.EquipmentOwnershipLoaner
	warranty := time.Now().AddDate(1, 0, 0)
	e.WarrantyExpiresOn = &warranty

	html := renderEquipmentShow(t, EquipmentShowProps{
		Equipment:    e,
		CustomerName: "Bunker Coffee",
		CanWrite:     true,
	})

	// Rule 3 in docs/admin-detail-pages.md: every axis, unconditionally.
	assert.Contains(t, html, "In service")
	assert.Contains(t, html, "Espresso machine")
	assert.Contains(t, html, "Our loaner")
	assert.Contains(t, html, "Under warranty")
}

// Rule 4: an action area that can render nothing must explain itself, or a
// read-only staffer cannot tell a closed record from a broken page.
func TestEquipmentShowExplainsWhyThereAreNoActions(t *testing.T) {
	html := renderEquipmentShow(t, EquipmentShowProps{
		Equipment:    sampleEquipment(),
		CustomerName: "Bunker Coffee",
		CanWrite:     false,
	})

	assert.Contains(t, html, "read-only access")
	assert.NotContains(t, html, "Retire this machine")
	assert.NotContains(t, html, "Edit details")
}

func TestEquipmentShowHidesRetireOnAlreadyRetiredMachine(t *testing.T) {
	e := sampleEquipment()
	e.Status = domain.EquipmentStatusRetired

	html := renderEquipmentShow(t, EquipmentShowProps{
		Equipment:    e,
		CustomerName: "Bunker Coffee",
		CanWrite:     true,
	})

	assert.NotContains(t, html, "Retire this machine")
	// But it can still be brought back — retiring by mistake has to be undoable.
	assert.Contains(t, html, "Back in service")
}

// An unknown warranty is not a warranty. The page must not imply cover it
// cannot vouch for.
func TestEquipmentShowSaysNothingAboutAnUnknownWarranty(t *testing.T) {
	html := renderEquipmentShow(t, EquipmentShowProps{
		Equipment:    sampleEquipment(),
		CustomerName: "Bunker Coffee",
		CanWrite:     true,
	})

	assert.Contains(t, html, "Not recorded")
	assert.NotContains(t, html, "Under warranty")
}

func TestEquipmentFormDefaultsToTheCommonCase(t *testing.T) {
	values := EquipmentFormValuesFrom(nil)

	// Most machines a roaster services are the cafe's own espresso machine.
	assert.Equal(t, string(domain.EquipmentCategoryEspressoMachine), values.Category)
	assert.Equal(t, string(domain.EquipmentOwnershipCustomer), values.Ownership)
	assert.Empty(t, values.Make)
}

func TestEquipmentFormRoundTripsAStoredMachine(t *testing.T) {
	e := sampleEquipment()
	installed := time.Date(2024, 3, 14, 0, 0, 0, 0, time.UTC)
	e.InstalledOn = &installed
	addressID := uuid.New()
	e.AddressID = &addressID

	values := EquipmentFormValuesFrom(&e)

	assert.Equal(t, "La Marzocco", values.Make)
	assert.Equal(t, "2024-03-14", values.InstalledOn, "dates go back as an <input type=date> value")
	assert.Equal(t, addressID.String(), values.AddressID)
	// Blank, not the zero date — an unrecorded warranty must round-trip as
	// unrecorded rather than as 1 Jan year one.
	assert.Empty(t, values.WarrantyExpiresOn)
}

// A `date` has no time zone. pgx returns it as midnight UTC, and shifting that
// into a negative-offset zone lands on the previous day — a part ordered on the
// 25th was rendering as the 24th.
func TestDayLabelDoesNotShiftACalendarDate(t *testing.T) {
	stored := time.Date(2026, 8, 25, 0, 0, 0, 0, time.UTC)

	assert.Equal(t, "25 Aug 2026", dayLabel(&stored))
	assert.Equal(t, "—", dayLabel(nil))
}

// The moment-shaped values still belong in merchant time; the two helpers are
// deliberately different and must not be collapsed back together.
func TestEquipmentDateLabelStillConvertsAMoment(t *testing.T) {
	la, err := time.LoadLocation("America/Los_Angeles")
	require.NoError(t, err)
	moment := time.Date(2026, 8, 25, 3, 0, 0, 0, time.UTC) // 8pm on the 24th in LA

	assert.Equal(t, "24 Aug 2026", equipmentDateLabel(&moment, la))
}
