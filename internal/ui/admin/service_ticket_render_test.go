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
	"github.com/dukerupert/hiri/internal/platform/audit"
)

func renderTicketShow(t *testing.T, props ServiceTicketShowProps) string {
	t.Helper()
	var sb strings.Builder
	require.NoError(t, ServiceTicketShowContent(props).Render(context.Background(), &sb))
	return sb.String()
}

func sampleTicket() domain.ServiceTicket {
	return domain.ServiceTicket{
		ID:            uuid.New(),
		Number:        "SVC-3A9F2C1B04",
		CustomerID:    uuid.New(),
		Title:         "Group head leaking",
		Severity:      domain.ServiceSeverityDegraded,
		Status:        domain.ServiceTicketStatusInProgress,
		LastContactAt: time.Now(),
	}
}

func noteOf(kind domain.ServiceNoteKind, body string, at time.Time) domain.ServiceTicketNote {
	staffID := uuid.New()
	return domain.ServiceTicketNote{
		ID: uuid.New(), TicketID: uuid.New(), Kind: kind, Body: body,
		OccurredAt: at, StaffID: &staffID,
	}
}

func auditOf(action string, at time.Time) domain.AuditEntry {
	return domain.AuditEntry{
		ID: uuid.New(), Action: action, ResourceType: "service_ticket",
		ResourceID: uuid.New(), ActorType: domain.AuditActorTypeStaff,
		ActorName: "Logan", CreatedAt: at,
	}
}

// The merge is the point of the timeline: staff must not have to interleave
// what was said with what happened by eye.
func TestServiceTimelineMergesNotesAndAuditInOneOrder(t *testing.T) {
	now := time.Now()
	notes := []domain.ServiceTicketNote{
		noteOf(domain.ServiceNoteKindCall, "Rang Dana", now.Add(-time.Hour)),
	}
	entries := []domain.AuditEntry{
		auditOf(audit.AuditServiceTicketStatus, now.Add(-30*time.Minute)),
		auditOf(audit.AuditServiceTicketOpened, now.Add(-2*time.Hour)),
	}

	merged := ServiceTimelineEntries(notes, entries, func(domain.ServiceTicketNote) string { return "Logan" })

	require.Len(t, merged, 3)
	// Newest first, with the note sorted into position rather than appended.
	assert.Equal(t, audit.AuditServiceTicketStatus, merged[0].Action)
	assert.Equal(t, noteActionPrefix+string(domain.ServiceNoteKindCall), merged[1].Action)
	assert.Equal(t, audit.AuditServiceTicketOpened, merged[2].Action)
}

// A note logged on Thursday about Tuesday's call belongs on Tuesday.
func TestServiceTimelineUsesWhenItHappenedNotWhenItWasTyped(t *testing.T) {
	happened := time.Now().Add(-72 * time.Hour)
	n := noteOf(domain.ServiceNoteKindCall, "Tuesday's call", happened)
	n.CreatedAt = time.Now()

	merged := ServiceTimelineEntries([]domain.ServiceTicketNote{n}, nil,
		func(domain.ServiceTicketNote) string { return "Logan" })

	require.Len(t, merged, 1)
	assert.True(t, merged[0].CreatedAt.Equal(happened))
}

func TestServiceTimelineAttributesCustomerReportsToTheCustomer(t *testing.T) {
	customerUserID := uuid.New()
	n := noteOf(domain.ServiceNoteKindCustomerReport, "It's dead", time.Now())
	n.StaffID = nil
	n.CustomerUserID = &customerUserID

	merged := ServiceTimelineEntries([]domain.ServiceTicketNote{n}, nil,
		func(domain.ServiceTicketNote) string { return "Bunker Coffee" })

	require.Len(t, merged, 1)
	assert.Equal(t, domain.AuditActorTypeCustomer, merged[0].ActorType)
	assert.Equal(t, "Bunker Coffee", merged[0].ActorName)
}

// The service writes an audit record for every note. Rendering both would show
// each note twice.
func TestServiceTimelineDoesNotDoubleEveryNote(t *testing.T) {
	now := time.Now()
	entries := []domain.AuditEntry{
		auditOf(audit.AuditServiceTicketNoteAdded, now),
		auditOf(audit.AuditServiceTicketOpened, now.Add(-time.Hour)),
	}

	kept := dropDoubledNoteEvents(entries)

	require.Len(t, kept, 1)
	assert.Equal(t, audit.AuditServiceTicketOpened, kept[0].Action)
}

func TestServiceTimelineLabelsEveryTicketAction(t *testing.T) {
	for action, want := range map[string]string{
		audit.AuditServiceTicketOpened:    "Ticket opened",
		audit.AuditServiceTicketAssigned:  "Assigned",
		audit.AuditServiceTicketStatus:    "Status changed",
		audit.AuditServiceTicketResolved:  "Resolved",
		audit.AuditServiceTicketReopened:  "Reopened",
		audit.AuditServiceTicketCancelled: "Cancelled",
	} {
		assert.Equal(t, want, serviceTicketEventLabel(action))
	}

	// Notes are labelled by what kind of contact they were.
	assert.Equal(t, "Call", serviceTicketEventLabel(noteActionPrefix+"call"))
	assert.Equal(t, "Reported by customer", serviceTicketEventLabel(noteActionPrefix+"customer_report"))

	// A customer reporting a dead machine is the one entry that should shout.
	assert.Equal(t, "bg-rr-red", serviceTicketEventMarker(noteActionPrefix+"customer_report"))
}

// The banner is the whole reason the module beats a spreadsheet, so it has to
// appear exactly when the ticket has gone quiet and not otherwise.
func TestTicketShowBannerOnlyWhenQuiet(t *testing.T) {
	fresh := renderTicketShow(t, ServiceTicketShowProps{
		Ticket: sampleTicket(), CustomerName: "Bunker Coffee", CanWrite: true,
	})
	assert.NotContains(t, fresh, "This ticket has gone quiet")

	quiet := sampleTicket()
	quiet.LastContactAt = time.Now().Add(-30 * 24 * time.Hour)
	html := renderTicketShow(t, ServiceTicketShowProps{
		Ticket: quiet, CustomerName: "Bunker Coffee", CanWrite: true, Stale: true,
	})
	assert.Contains(t, html, "This ticket has gone quiet")
	assert.Contains(t, html, "Bunker Coffee")
}

// The composer is what moves the contact clock, so read-only staff must not be
// shown it — and must be told why the rail is empty.
func TestTicketShowHidesComposerFromReadOnlyStaff(t *testing.T) {
	html := renderTicketShow(t, ServiceTicketShowProps{
		Ticket: sampleTicket(), CustomerName: "Bunker Coffee", CanWrite: false,
	})

	assert.NotContains(t, html, "Add to the timeline")
	assert.NotContains(t, html, "Mark resolved")
	assert.Contains(t, html, "read-only access")
}

// Rule 4: a closed ticket's empty action area has to explain itself, and offer
// the way back.
func TestTicketShowExplainsAClosedTicket(t *testing.T) {
	closed := sampleTicket()
	closed.Status = domain.ServiceTicketStatusResolved

	html := renderTicketShow(t, ServiceTicketShowProps{
		Ticket: closed, CustomerName: "Bunker Coffee", CanWrite: true,
	})

	assert.Contains(t, html, "Reopen")
	assert.NotContains(t, html, "Cancel this ticket")
	assert.NotContains(t, html, "Mark resolved")
}

// Terminal moves live in their own cards, so they are never one click away from
// a routine status change.
func TestNextStatusesExcludeTheTerminalOnes(t *testing.T) {
	next := nextStatuses(domain.ServiceTicketStatusNew)

	for _, s := range next {
		assert.True(t, s.Open(), "%s should not be offered beside routine moves", s)
		assert.NotEqual(t, domain.ServiceTicketStatusNew, s, "the current status is not a move")
	}
	assert.NotEmpty(t, next)
}

func TestFormatMinutesReadsLikeATechWouldSayIt(t *testing.T) {
	assert.Equal(t, "45m", formatMinutes(45))
	assert.Equal(t, "2h", formatMinutes(120))
	assert.Equal(t, "1h 30m", formatMinutes(90))
}

// A "0" in a tab badge reads as a number to check rather than nothing to do.
func TestServiceNavBadgeHidesAtZero(t *testing.T) {
	tabs := ServiceNav{}.tabs()
	require.Len(t, tabs, 5)
	for _, tab := range tabs {
		assert.Equal(t, 0, tab.Count, "%s", tab.Label)
	}

	withWork := ServiceNav{StaleCount: 3, OverdueMaintenance: 5}.tabs()
	assert.Equal(t, 3, withWork[0].Count, "the count rides on the tab that owns the fix")
	assert.Equal(t, serviceTabTickets, withWork[0].Href)
	assert.Equal(t, 5, withWork[2].Count, "overdue maintenance rides on the Maintenance tab")
	assert.Equal(t, serviceTabMaintenance, withWork[2].Href)
}
