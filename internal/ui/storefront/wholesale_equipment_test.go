package storefront

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func renderEquipment(t *testing.T, props WholesaleEquipmentProps) string {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, WholesaleEquipmentContent(props).Render(context.Background(), &buf))
	return buf.String()
}

func equipmentProps() WholesaleEquipmentProps {
	machineID := uuid.New()
	serviced := time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	return WholesaleEquipmentProps{
		Customer:    &domain.Customer{Email: "cafe@example.test"},
		CompanyName: "Blue Heron Cafe",
		Machines: []WholesaleMachineView{{
			Equipment: domain.Equipment{
				ID:           machineID,
				Category:     domain.EquipmentCategoryEspressoMachine,
				Make:         "La Marzocco",
				Model:        "Linea PB",
				SerialNumber: "LM-99201",
				Status:       domain.EquipmentStatusActive,
			},
			LastServiced: &serviced,
			OpenTickets: []WholesaleTicketView{{
				Ticket: domain.ServiceTicket{
					Number: "SVC-ABC123",
					Title:  "La Marzocco Linea PB — machine down",
					Status: domain.ServiceTicketStatusWaitingParts,
				},
				Notes: []domain.ServiceTicketNote{
					{Kind: domain.ServiceNoteKindEmail, Body: "Pump is on order, should land Thursday."},
					{Kind: domain.ServiceNoteKindCustomerReport, Body: "No pressure at all this morning."},
				},
			}},
		}},
	}
}

// The page has one job beyond listing machines: show the customer that their
// message was seen and what has happened since. That means the ticket's status
// and both sides of the conversation have to be on the page.
func TestWholesaleEquipmentShowsTheConversation(t *testing.T) {
	html := renderEquipment(t, equipmentProps())

	assert.Contains(t, html, "La Marzocco Linea PB")
	assert.Contains(t, html, "LM-99201")
	assert.Contains(t, html, "Jun 14, 2026", "the last service date is the question the page opens with")
	assert.Contains(t, html, "SVC-ABC123")
	assert.Contains(t, html, "Waiting on parts")
	assert.Contains(t, html, "No pressure at all this morning.")
	assert.Contains(t, html, "Pump is on order, should land Thursday.")
	// Their own words are labelled as theirs — that attribution is the receipt.
	assert.Contains(t, html, "You told us")
	assert.Contains(t, html, "/wholesale/account/equipment/")
}

// A machine nobody has been called out to must read as "not yet", not as a date
// in antiquity — a zero time formatted straight would say "Jan 1, 1".
func TestWholesaleEquipmentUnservicedMachineReadsAsNotYet(t *testing.T) {
	props := equipmentProps()
	props.Machines[0].LastServiced = nil
	props.Machines[0].OpenTickets = nil

	html := renderEquipment(t, props)

	assert.Contains(t, html, "Not yet")
	assert.NotContains(t, html, "Jan 1, 1")
}

// A shop we hold no equipment for gets a plain explanation, not an empty page
// with a report form on nothing.
func TestWholesaleEquipmentEmptyState(t *testing.T) {
	html := renderEquipment(t, WholesaleEquipmentProps{
		Customer:    &domain.Customer{Email: "cafe@example.test"},
		CompanyName: "Blue Heron Cafe",
	})

	assert.Contains(t, html, "Nothing on the books")
	assert.NotContains(t, html, "Report a problem")
}

// After a failed submit the form reopens on the machine it was filled in for,
// carrying back what was typed — and stays shut on every other machine, so the
// page does not come back with every form on it flung open.
func TestWholesaleEquipmentRepopulatesTheFailedReport(t *testing.T) {
	props := equipmentProps()
	other := domain.Equipment{ID: uuid.New(), Category: domain.EquipmentCategoryGrinder, Make: "Mahlkonig", Status: domain.EquipmentStatusActive}
	props.Machines = append(props.Machines, WholesaleMachineView{Equipment: other})
	props.ReportingID = props.Machines[0].Equipment.ID.String()
	props.ReportDown = true
	props.ReportTime = "Before 10am"

	html := renderEquipment(t, props)

	assert.Contains(t, html, "Before 10am")
	assert.Contains(t, html, "{ open: true }", "the reported machine's form reopens")
	assert.Contains(t, html, "{ open: false }", "the other machine's stays shut")
}
