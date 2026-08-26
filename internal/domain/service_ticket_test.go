package domain_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/dukerupert/hiri/internal/domain"
)

// IsContact is the rule the whole staleness flag rests on, so it gets pinned
// case by case rather than trusted to a switch nobody re-reads.
func TestServiceNoteKindIsContact(t *testing.T) {
	contact := []domain.ServiceNoteKind{
		domain.ServiceNoteKindCall,
		domain.ServiceNoteKindEmail,
		domain.ServiceNoteKindVisit,
		domain.ServiceNoteKindCustomerReport,
	}
	for _, k := range contact {
		assert.True(t, k.IsContact(), "%s reaches the customer", k)
	}

	// The one that matters: writing "chased the supplier again" to yourself is
	// not telling the cafe anything. If an internal note reset the clock, the
	// flag would go quiet in exactly the case it exists to catch — a ticket
	// somebody is busy on and nobody has phoned about.
	assert.False(t, domain.ServiceNoteKindNote.IsContact())
}

func TestServiceTicketStatusOpen(t *testing.T) {
	open := []domain.ServiceTicketStatus{
		domain.ServiceTicketStatusNew,
		domain.ServiceTicketStatusScheduled,
		domain.ServiceTicketStatusInProgress,
		// Both waiting states are still the shop's problem.
		domain.ServiceTicketStatusWaitingParts,
		domain.ServiceTicketStatusWaitingCustomer,
	}
	for _, s := range open {
		assert.True(t, s.Open(), "%s is unfinished work", s)
	}

	assert.False(t, domain.ServiceTicketStatusResolved.Open())
	assert.False(t, domain.ServiceTicketStatusCancelled.Open())
}

func TestServiceTicketStaleSince(t *testing.T) {
	cutoff := time.Now().Add(-7 * 24 * time.Hour)
	quiet := cutoff.Add(-24 * time.Hour)
	recent := time.Now()

	assert.True(t, domain.ServiceTicket{
		Status:        domain.ServiceTicketStatusWaitingParts,
		LastContactAt: quiet,
	}.StaleSince(cutoff), "an open ticket nobody has rung about is stale")

	assert.False(t, domain.ServiceTicket{
		Status:        domain.ServiceTicketStatusWaitingParts,
		LastContactAt: recent,
	}.StaleSince(cutoff))

	// Silence on finished work is the correct outcome, not a failure. Without
	// this, every ticket ever closed would eventually light up the queue.
	assert.False(t, domain.ServiceTicket{
		Status:        domain.ServiceTicketStatusResolved,
		LastContactAt: quiet,
	}.StaleSince(cutoff))

	assert.False(t, domain.ServiceTicket{
		Status:        domain.ServiceTicketStatusCancelled,
		LastContactAt: quiet,
	}.StaleSince(cutoff))
}

func TestServiceEnumsValidateAgainstTheirDatabaseCheck(t *testing.T) {
	// These lists have to match the CHECK constraints in migration 074. A value
	// that passes here and fails there is a 500 in front of a staff member.
	for _, s := range domain.ServiceTicketStatuses() {
		assert.True(t, s.Valid(), "%s", s)
		assert.NotEmpty(t, s.Label())
	}
	for _, s := range domain.ServiceSeverities() {
		assert.True(t, s.Valid(), "%s", s)
	}
	for _, s := range domain.ServicePartStatuses() {
		assert.True(t, s.Valid(), "%s", s)
	}

	assert.False(t, domain.ServiceTicketStatus("closed").Valid(),
		"a status the database would reject must not pass validation")
	assert.False(t, domain.ServiceSeverity("p1").Valid(),
		"severity is the cafe's words, not a priority scale")
	assert.False(t, domain.ServiceNoteKind("sms").Valid())
	assert.False(t, domain.ServiceTimeKind("overtime").Valid())
}

func TestServicePartTotalCost(t *testing.T) {
	part := domain.ServicePart{Quantity: 3, UnitCostCents: 425}
	assert.Equal(t, 1275, part.TotalCostCents())
}

func TestServiceTotalsTotalMinutes(t *testing.T) {
	totals := domain.ServiceTotals{LaborMinutes: 90, TravelMinutes: 45, BillableMinutes: 90}
	assert.Equal(t, 135, totals.TotalMinutes(),
		"driving time counts toward what the job cost, whether or not it is billed")
}
