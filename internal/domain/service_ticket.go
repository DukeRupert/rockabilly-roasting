package domain

import (
	"time"

	"github.com/google/uuid"
)

// Service tickets — one repair, one machine, one thread of conversation.
// Part of the equipment service module; see docs/equipment-service-module.md.

// ServiceSeverity is how badly the machine is behaving, in the words a cafe
// would actually use. Not a priority scale: nobody phones in a "P1".
type ServiceSeverity string

const (
	// ServiceSeverityDown — unusable. They cannot serve coffee on it right now.
	ServiceSeverityDown ServiceSeverity = "down"
	// ServiceSeverityDegraded — working, but wrong. Limping along.
	ServiceSeverityDegraded ServiceSeverity = "degraded"
	// ServiceSeverityRoutine — nothing is broken; scheduled or preventive work.
	ServiceSeverityRoutine ServiceSeverity = "routine"
)

// ServiceSeverities lists severities worst first, which is the order both the
// report form and the filter strip want.
func ServiceSeverities() []ServiceSeverity {
	return []ServiceSeverity{ServiceSeverityDown, ServiceSeverityDegraded, ServiceSeverityRoutine}
}

// Label is the human name for a severity.
func (s ServiceSeverity) Label() string {
	switch s {
	case ServiceSeverityDown:
		return "Machine down"
	case ServiceSeverityDegraded:
		return "Working badly"
	case ServiceSeverityRoutine:
		return "Routine"
	}
	return string(s)
}

// Valid reports whether s is a known severity.
func (s ServiceSeverity) Valid() bool {
	switch s {
	case ServiceSeverityDown, ServiceSeverityDegraded, ServiceSeverityRoutine:
		return true
	}
	return false
}

// ServiceTicketStatus is where the work has got to.
type ServiceTicketStatus string

const (
	ServiceTicketStatusNew             ServiceTicketStatus = "new"
	ServiceTicketStatusScheduled       ServiceTicketStatus = "scheduled"
	ServiceTicketStatusInProgress      ServiceTicketStatus = "in_progress"
	ServiceTicketStatusWaitingParts    ServiceTicketStatus = "waiting_parts"
	ServiceTicketStatusWaitingCustomer ServiceTicketStatus = "waiting_customer"
	ServiceTicketStatusResolved        ServiceTicketStatus = "resolved"
	ServiceTicketStatusCancelled       ServiceTicketStatus = "cancelled"
)

// ServiceTicketStatuses lists statuses in workflow order.
func ServiceTicketStatuses() []ServiceTicketStatus {
	return []ServiceTicketStatus{
		ServiceTicketStatusNew,
		ServiceTicketStatusScheduled,
		ServiceTicketStatusInProgress,
		ServiceTicketStatusWaitingParts,
		ServiceTicketStatusWaitingCustomer,
		ServiceTicketStatusResolved,
		ServiceTicketStatusCancelled,
	}
}

// OpenServiceTicketStatuses lists the statuses that count as unfinished work,
// in workflow order. The inverse of the ('resolved', 'cancelled') exclusion the
// store's open-ticket queries use — kept as one list so a status added later
// cannot be open to the database and closed to the gauges.
func OpenServiceTicketStatuses() []ServiceTicketStatus {
	out := make([]ServiceTicketStatus, 0, len(ServiceTicketStatuses()))
	for _, s := range ServiceTicketStatuses() {
		if s == ServiceTicketStatusResolved || s == ServiceTicketStatusCancelled {
			continue
		}
		out = append(out, s)
	}
	return out
}

// Label is the human name for a status.// Label is the human name for a status.
func (s ServiceTicketStatus) Label() string {
	switch s {
	case ServiceTicketStatusNew:
		return "New"
	case ServiceTicketStatusScheduled:
		return "Scheduled"
	case ServiceTicketStatusInProgress:
		return "In progress"
	case ServiceTicketStatusWaitingParts:
		return "Waiting on parts"
	case ServiceTicketStatusWaitingCustomer:
		return "Waiting on customer"
	case ServiceTicketStatusResolved:
		return "Resolved"
	case ServiceTicketStatusCancelled:
		return "Cancelled"
	}
	return string(s)
}

// Valid reports whether s is a known status.
func (s ServiceTicketStatus) Valid() bool {
	switch s {
	case ServiceTicketStatusNew, ServiceTicketStatusScheduled, ServiceTicketStatusInProgress,
		ServiceTicketStatusWaitingParts, ServiceTicketStatusWaitingCustomer,
		ServiceTicketStatusResolved, ServiceTicketStatusCancelled:
		return true
	}
	return false
}

// Open reports whether the ticket is still live work. Everything that is not
// finished counts, including both waiting states — a ticket waiting on a part
// is still the shop's problem, and going quiet on one is exactly the failure
// the staleness flag exists to catch.
func (s ServiceTicketStatus) Open() bool {
	return s != ServiceTicketStatusResolved && s != ServiceTicketStatusCancelled
}

// ServiceNoteKind is what a timeline entry records.
type ServiceNoteKind string

const (
	// ServiceNoteKindNote — an internal working note. Not a communication.
	ServiceNoteKindNote  ServiceNoteKind = "note"
	ServiceNoteKindCall  ServiceNoteKind = "call"
	ServiceNoteKindEmail ServiceNoteKind = "email"
	ServiceNoteKindVisit ServiceNoteKind = "visit"
	// ServiceNoteKindCustomerReport — what the portal's "report a problem" form
	// writes, in the customer's own words.
	ServiceNoteKindCustomerReport ServiceNoteKind = "customer_report"
)

// Label is the human name for a note kind.
func (k ServiceNoteKind) Label() string {
	switch k {
	case ServiceNoteKindNote:
		return "Note"
	case ServiceNoteKindCall:
		return "Call"
	case ServiceNoteKindEmail:
		return "Email"
	case ServiceNoteKindVisit:
		return "Visit"
	case ServiceNoteKindCustomerReport:
		return "Reported by customer"
	}
	return string(k)
}

// Valid reports whether k is a known note kind.
func (k ServiceNoteKind) Valid() bool {
	switch k {
	case ServiceNoteKindNote, ServiceNoteKindCall, ServiceNoteKindEmail,
		ServiceNoteKindVisit, ServiceNoteKindCustomerReport:
		return true
	}
	return false
}

// IsContact reports whether an entry of this kind counts as talking to the
// customer, and so moves the ticket's LastContactAt.
//
// This is the rule the staleness flag rests on, which is why it lives here as
// one testable function rather than as a condition inlined at each call site.
// An internal note is deliberately not contact: writing "chased the supplier
// again" to yourself is not the same as telling the cafe anything, and letting
// it reset the clock would make the flag lie in precisely the case it exists
// to catch.
func (k ServiceNoteKind) IsContact() bool {
	switch k {
	case ServiceNoteKindCall, ServiceNoteKindEmail, ServiceNoteKindVisit, ServiceNoteKindCustomerReport:
		return true
	}
	return false
}

// ServicePartStatus tracks a part from "we need one" to "it is in the machine".
type ServicePartStatus string

const (
	ServicePartStatusNeeded    ServicePartStatus = "needed"
	ServicePartStatusOrdered   ServicePartStatus = "ordered"
	ServicePartStatusReceived  ServicePartStatus = "received"
	ServicePartStatusInstalled ServicePartStatus = "installed"
)

// ServicePartStatuses lists part statuses in workflow order.
func ServicePartStatuses() []ServicePartStatus {
	return []ServicePartStatus{
		ServicePartStatusNeeded,
		ServicePartStatusOrdered,
		ServicePartStatusReceived,
		ServicePartStatusInstalled,
	}
}

// Label is the human name for a part status.
func (s ServicePartStatus) Label() string {
	switch s {
	case ServicePartStatusNeeded:
		return "Needed"
	case ServicePartStatusOrdered:
		return "Ordered"
	case ServicePartStatusReceived:
		return "Received"
	case ServicePartStatusInstalled:
		return "Installed"
	}
	return string(s)
}

// Valid reports whether s is a known part status.
func (s ServicePartStatus) Valid() bool {
	switch s {
	case ServicePartStatusNeeded, ServicePartStatusOrdered,
		ServicePartStatusReceived, ServicePartStatusInstalled:
		return true
	}
	return false
}

// ServiceTimeKind separates the hours spent fixing from the hours spent driving.
type ServiceTimeKind string

const (
	ServiceTimeKindLabor  ServiceTimeKind = "labor"
	ServiceTimeKindTravel ServiceTimeKind = "travel"
)

// Label is the human name for a time kind.
func (k ServiceTimeKind) Label() string {
	switch k {
	case ServiceTimeKindLabor:
		return "Labour"
	case ServiceTimeKindTravel:
		return "Travel"
	}
	return string(k)
}

// Valid reports whether k is a known time kind.
func (k ServiceTimeKind) Valid() bool {
	switch k {
	case ServiceTimeKindLabor, ServiceTimeKindTravel:
		return true
	}
	return false
}

// ServiceTicket is one piece of work on one machine.
type ServiceTicket struct {
	ID          uuid.UUID
	Number      string
	CustomerID  uuid.UUID
	EquipmentID *uuid.UUID
	AddressID   *uuid.UUID
	Title       string
	Description string
	Severity    ServiceSeverity
	Status      ServiceTicketStatus
	// OpenedByStaffID and OpenedByCustomerUserID are mutually exclusive in
	// practice: a ticket is raised either by the shop or by the cafe.
	OpenedByStaffID        *uuid.UUID
	OpenedByCustomerUserID *uuid.UUID
	AssignedStaffID        *uuid.UUID
	ScheduledFor           *time.Time
	ResolvedAt             *time.Time
	Resolution             string
	Billable               bool
	// LastContactAt moves only on communication — see ServiceNoteKind.IsContact.
	// Never nil: it starts at creation time so staleness is a plain comparison.
	LastContactAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// StaleSince reports whether an open ticket has gone quiet — no contact with
// the customer since the cutoff.
//
// Resolved and cancelled tickets are never stale, however long ago they were
// closed: silence on finished work is the correct outcome, not a failure.
func (t ServiceTicket) StaleSince(cutoff time.Time) bool {
	if !t.Status.Open() {
		return false
	}
	return t.LastContactAt.Before(cutoff)
}

// DefaultStaleContactWindow is how long an open ticket may go without anyone
// talking to the customer before it is flagged.
//
// Seven days is a guess — a considered one, but a guess. It is a constant here
// rather than a settings row because nothing has run long enough to argue with
// it yet; the sweep job in step 6 makes it configurable, and until then one
// place holds the number.
const DefaultStaleContactWindow = 7 * 24 * time.Hour

// ServiceTicketNote is one entry on a ticket's timeline.
type ServiceTicketNote struct {
	ID         uuid.UUID
	TicketID   uuid.UUID
	Kind       ServiceNoteKind
	Body       string
	OccurredAt time.Time
	// StaffID or CustomerUserID identifies the author. Both nil means the entry
	// was written by a background job.
	StaffID         *uuid.UUID
	CustomerUserID  *uuid.UUID
	CustomerVisible bool
	CreatedAt       time.Time
}

// ServicePart is one part needed, ordered, received, or fitted.
type ServicePart struct {
	ID       uuid.UUID
	TicketID uuid.UUID
	// VariantID links to the catalog for the minority of shops that stock
	// common parts. Nil for anything ordered ad hoc from a distributor, which
	// is most of them.
	VariantID     *uuid.UUID
	Name          string
	PartNumber    string
	Supplier      string
	Quantity      int
	UnitCostCents int
	Status        ServicePartStatus
	OrderedOn     *time.Time
	ReceivedOn    *time.Time
	InstalledOn   *time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// TotalCostCents is what the line came to.
func (p ServicePart) TotalCostCents() int { return p.UnitCostCents * p.Quantity }

// ServiceTimeEntry is one stint of work, recorded after the fact.
type ServiceTimeEntry struct {
	ID          uuid.UUID
	TicketID    uuid.UUID
	StaffID     uuid.UUID
	Kind        ServiceTimeKind
	Minutes     int
	PerformedOn time.Time
	Billable    bool
	Note        string
	CreatedAt   time.Time
}

// ServiceTotals is the roll-up a ticket page and an account report both need:
// what the parts cost and where the hours went.
type ServiceTotals struct {
	PartsCostCents int
	LaborMinutes   int
	TravelMinutes  int
	// BillableMinutes counts both kinds, since a shop that bills travel bills
	// it by the minute like anything else.
	BillableMinutes int
}

// TotalMinutes is every minute recorded against the ticket, billable or not.
func (t ServiceTotals) TotalMinutes() int { return t.LaborMinutes + t.TravelMinutes }
