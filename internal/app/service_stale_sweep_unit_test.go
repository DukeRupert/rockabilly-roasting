package app

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
)

func staleTicket(number string, severity domain.ServiceSeverity, quietFor time.Duration) domain.ServiceTicket {
	return domain.ServiceTicket{
		Number:        number,
		Severity:      severity,
		LastContactAt: time.Now().Add(-quietFor),
	}
}

func numbersOf(tickets []domain.ServiceTicket) []string {
	out := make([]string, 0, len(tickets))
	for _, t := range tickets {
		out = append(out, t.Number)
	}
	return out
}

// The store hands these over quietest-first. That is the right tiebreak but the
// wrong headline: a machine that is not running beats one that is limping, even
// if the limping one has been ignored for longer.
func TestOrderStaleForDigest_DownFirstThenQuietest(t *testing.T) {
	day := 24 * time.Hour
	// Quietest-first, as ListStale returns them.
	in := []domain.ServiceTicket{
		staleTicket("A", domain.ServiceSeverityRoutine, 30*day),
		staleTicket("B", domain.ServiceSeverityDegraded, 20*day),
		staleTicket("C", domain.ServiceSeverityDown, 15*day),
		staleTicket("D", domain.ServiceSeverityDown, 9*day),
		staleTicket("E", domain.ServiceSeverityRoutine, 8*day),
	}

	got := orderStaleForDigest(in, 0)

	// Down tickets lead, and within each group the original quietest-first
	// order survives — that is what makes the sort stable rather than merely
	// correct on this input.
	assert.Equal(t, []string{"C", "D", "A", "B", "E"}, numbersOf(got))
}

// The caller still needs the full slice for the total and the "showing N of M"
// line, so the input must come back untouched.
func TestOrderStaleForDigest_DoesNotMutateCaller(t *testing.T) {
	day := 24 * time.Hour
	in := []domain.ServiceTicket{
		staleTicket("A", domain.ServiceSeverityRoutine, 30*day),
		staleTicket("B", domain.ServiceSeverityDown, 2*day),
	}
	before := numbersOf(in)

	got := orderStaleForDigest(in, 1)

	assert.Equal(t, before, numbersOf(in), "input slice was reordered")
	assert.Len(t, got, 1)
	assert.Equal(t, "B", got[0].Number, "the cap must keep the promoted ticket, not the first one")
}

func TestOrderStaleForDigest_LimitAndEmpty(t *testing.T) {
	day := 24 * time.Hour
	in := []domain.ServiceTicket{
		staleTicket("A", domain.ServiceSeverityRoutine, 5*day),
		staleTicket("B", domain.ServiceSeverityRoutine, 4*day),
		staleTicket("C", domain.ServiceSeverityRoutine, 3*day),
	}

	assert.Len(t, orderStaleForDigest(in, 2), 2)
	// A zero limit means "no cap", matching ListStale's own limit convention.
	assert.Len(t, orderStaleForDigest(in, 0), 3)
	assert.Len(t, orderStaleForDigest(in, 10), 3)
	assert.Empty(t, orderStaleForDigest(nil, 5))
}

// A module that is off must cost nothing and, above all, must not mail anyone.
// This is the guard that lets the worker be registered on every instance
// regardless of which ones actually service machines.
func TestSweepStaleTickets_NoOpWhenModuleDisabled(t *testing.T) {
	// A service with no stores, no pool and no mailer: if the module gate does
	// not return first, this panics rather than quietly passing.
	svc := &ServiceTicketService{}

	require.NoError(t, svc.SweepStaleTickets(t.Context(), nil, time.Now()))
}

// Every open status is published, including ones the grouped query omitted
// because they are empty — otherwise a status that clears out keeps reporting
// its last value forever.
func TestOpenServiceTicketStatuses_CoversEveryUnfinishedStatus(t *testing.T) {
	open := domain.OpenServiceTicketStatuses()

	assert.NotContains(t, open, domain.ServiceTicketStatusResolved)
	assert.NotContains(t, open, domain.ServiceTicketStatusCancelled)
	assert.Contains(t, open, domain.ServiceTicketStatusNew)
	assert.Contains(t, open, domain.ServiceTicketStatusWaitingParts)
	assert.Len(t, open, len(domain.ServiceTicketStatuses())-2)
}
