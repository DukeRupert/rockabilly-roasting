package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func machineFixture(customerID uuid.UUID, make string) domain.Equipment {
	return domain.Equipment{
		ID:         uuid.New(),
		CustomerID: customerID,
		Category:   domain.EquipmentCategoryEspressoMachine,
		Make:       make,
		Status:     domain.EquipmentStatusActive,
	}
}

func ticketFixture(customerID uuid.UUID, equipmentID *uuid.UUID, number string) domain.ServiceTicket {
	return domain.ServiceTicket{
		ID:          uuid.New(),
		Number:      number,
		CustomerID:  customerID,
		EquipmentID: equipmentID,
		Title:       "Group head leaking",
		Severity:    domain.ServiceSeverityDegraded,
		Status:      domain.ServiceTicketStatusNew,
	}
}

// Every live ticket has to end up somewhere the customer can see it. Filing them
// under their machine is the readable arrangement; the ones with nowhere to sit
// are the case worth pinning down, because silently dropping them would mean a
// cafe ringing up to ask about a ticket the page never showed them.
func TestGroupMachineTicketsFilesEveryTicket(t *testing.T) {
	customer := uuid.New()
	espresso := machineFixture(customer, "La Marzocco")
	grinder := machineFixture(customer, "Mahlkonig")
	retired := uuid.New() // a machine ListForCustomer leaves out

	onEspresso := ticketFixture(customer, &espresso.ID, "SVC-A")
	onRetired := ticketFixture(customer, &retired, "SVC-B")
	accountWide := ticketFixture(customer, nil, "SVC-C")

	serviced := time.Now().Add(-30 * 24 * time.Hour)
	views, orphans := groupMachineTickets(
		[]domain.Equipment{espresso, grinder},
		map[uuid.UUID]time.Time{espresso.ID: serviced},
		[]domain.ServiceTicket{onEspresso, onRetired, accountWide},
		map[uuid.UUID][]domain.ServiceTicketNote{
			onEspresso.ID: {{Kind: domain.ServiceNoteKindCustomerReport, Body: "No pressure."}},
		},
	)

	require.Len(t, views, 2)
	assert.Equal(t, espresso.ID, views[0].Equipment.ID)
	require.Len(t, views[0].OpenTickets, 1)
	assert.Equal(t, "SVC-A", views[0].OpenTickets[0].Ticket.Number)
	require.Len(t, views[0].OpenTickets[0].Notes, 1)
	require.NotNil(t, views[0].LastServiced)
	assert.Equal(t, serviced, *views[0].LastServiced)

	// A machine nobody has been called out to carries no date rather than a
	// zero one, so the page can say "not yet" instead of a year in antiquity.
	assert.Empty(t, views[1].OpenTickets)
	assert.Nil(t, views[1].LastServiced)

	// The retired machine's ticket and the account-wide one both survive.
	numbers := []string{orphans[0].Ticket.Number, orphans[1].Ticket.Number}
	assert.ElementsMatch(t, []string{"SVC-B", "SVC-C"}, numbers)
}

// A cafe with machines and nothing wrong is the ordinary case, and it must not
// produce an "other open jobs" block with nothing in it.
func TestGroupMachineTicketsQuietAccount(t *testing.T) {
	customer := uuid.New()
	machine := machineFixture(customer, "La Marzocco")

	views, orphans := groupMachineTickets(
		[]domain.Equipment{machine},
		map[uuid.UUID]time.Time{},
		nil,
		map[uuid.UUID][]domain.ServiceTicketNote{},
	)

	require.Len(t, views, 1)
	assert.Empty(t, views[0].OpenTickets)
	assert.Empty(t, orphans)
}

// The module gate on the portal hangs entirely on ServeMux preferring the two
// literal equipment patterns over the /wholesale/account/{path...} catch-all
// they sit beside. If that precedence went the other way the catch-all would
// swallow both routes and the gate would simply never run — a shop with the
// module off would serve a portal page for a feature it does not have. Worth
// pinning, because nothing else in the code would look wrong.
func TestEquipmentRoutesBeatTheAccountCatchAll(t *testing.T) {
	mux := http.NewServeMux()
	hit := ""
	route := func(pattern, name string) {
		mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) { hit = name })
	}
	route("GET /wholesale/account/{path...}", "catchall")
	route("POST /wholesale/account/{path...}", "catchall")
	route("GET /wholesale/account/equipment", "equipment")
	route("POST /wholesale/account/equipment/{id}/report", "report")

	for _, tc := range []struct{ method, path, want string }{
		{http.MethodGet, "/wholesale/account/equipment", "equipment"},
		{http.MethodPost, "/wholesale/account/equipment/" + uuid.New().String() + "/report", "report"},
		{http.MethodGet, "/wholesale/account/orders", "catchall"},
	} {
		hit = ""
		mux.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(tc.method, tc.path, nil))
		assert.Equal(t, tc.want, hit, "%s %s", tc.method, tc.path)
	}
}
