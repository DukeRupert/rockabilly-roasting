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
	// RateCents is the hourly cost this entry was booked at, captured when it
	// was written. Nil means uncosted: the hour was logged before the shop had
	// a rate, and the reports count its minutes and none of its money.
	//
	// A settings change never touches this. The hour was bought at the rate of
	// the day, and that is a fact about the past — see migration 083.
	RateCents *int
}

// CostCents is what this entry cost, rounded to the nearest cent. Zero for an
// uncosted entry, which is honest: nobody has said what that hour was worth.
func (e ServiceTimeEntry) CostCents() int {
	if e.RateCents == nil {
		return 0
	}
	return costMinutes(e.Minutes, *e.RateCents)
}

// Costed reports whether this entry carries a rate.
func (e ServiceTimeEntry) Costed() bool { return e.RateCents != nil }

// ServiceTotals is the roll-up a ticket page and an account report both need:
// what the parts cost and where the hours went.
type ServiceTotals struct {
	PartsCostCents int
	LaborMinutes   int
	TravelMinutes  int
	// BillableMinutes counts both kinds, since a shop that bills travel bills
	// it by the minute like anything else.
	BillableMinutes int
	// LaborCostCents is what the hours cost, summed from the rate stamped on
	// each entry rather than computed from whatever the shop charges today.
	// That is the point of the snapshot: this number does not move when
	// somebody edits the rate in Settings.
	LaborCostCents int
	// UncostedMinutes are minutes on entries carrying no rate — logged before
	// the shop set one. Carried so a surface can say "and 4h we never priced"
	// instead of quietly counting those hours as free.
	UncostedMinutes int
}

// TotalMinutes is every minute recorded against the ticket, billable or not.
func (t ServiceTotals) TotalMinutes() int { return t.LaborMinutes + t.TravelMinutes }

// ServiceCostSummary is what a machine or an account has cost over a window:
// the same roll-up as a single ticket, widened.
//
// It embeds ServiceTotals rather than restating its fields so the two can never
// disagree. A machine page reporting $40 while the ticket beside it says $58
// would read as a bug in the money, which is the worst kind to have.
type ServiceCostSummary struct {
	ServiceTotals
	// PartCount is how many part lines went into the cost, so "$340" can be
	// read as "$340 across twelve parts" rather than left as a bare number.
	PartCount int
	// Visits is the number of distinct tickets that carried any of this work.
	// Not the number of tickets raised: a ticket that was only ever a phone
	// call is not a visit, and counting it would flatter the hours-per-visit
	// figure this exists to expose.
	Visits int
}

// Any reports whether anything at all was recorded in the window. Used to tell
// "nothing has been spent on this" from "we have not started tracking yet",
// which are the same zero and different sentences.
func (s ServiceCostSummary) Any() bool {
	return s.PartsCostCents > 0 || s.TotalMinutes() > 0 || s.Visits > 0
}

// MinutesPerVisit is the average length of a visit, in minutes, or zero when
// there have been none. The number that says whether a machine is a quick
// call-out or an afternoon every time.
func (s ServiceCostSummary) MinutesPerVisit() int {
	if s.Visits == 0 {
		return 0
	}
	return s.TotalMinutes() / s.Visits
}

// ServiceLaborRates is what an hour of the crew's time costs the shop.
//
// Cost, not price. These feed reports that say they measure what work cost
// rather than what it earned; a charge-out rate here would quietly turn a cost
// report into a revenue one. Billing a ticket out will want its own charge rate
// when it is built.
//
// Both are nil until somebody sets them, and nil is a real state the reports
// respect — there is no rate a shop can be assumed to have, and a zero would
// render as "$0.00 of labour", which looks measured.
type ServiceLaborRates struct {
	// LaborCentsPerHour is the loaded cost of a technician's hour.
	LaborCentsPerHour *int
	// TravelCentsPerHour is the cost of an hour on the road. Nil falls back to
	// the labour rate: travel counted at the tech's rate overstates nothing
	// that was not genuinely somebody's hour.
	TravelCentsPerHour *int
}

// Set reports whether a labour rate has been configured. Without one there is
// no money figure to compute, and every surface that would show one hides it
// instead of printing a zero.
func (r ServiceLaborRates) Set() bool {
	return r.LaborCentsPerHour != nil && *r.LaborCentsPerHour > 0
}

// TravelRate is the rate to cost travel at, falling back to labour.
func (r ServiceLaborRates) TravelRate() int {
	if r.TravelCentsPerHour != nil {
		return *r.TravelCentsPerHour
	}
	if r.LaborCentsPerHour != nil {
		return *r.LaborCentsPerHour
	}
	return 0
}

// LaborRate is the rate to cost labour at.
func (r ServiceLaborRates) LaborRate() int {
	if r.LaborCentsPerHour != nil {
		return *r.LaborCentsPerHour
	}
	return 0
}

// costMinutes converts minutes at an hourly rate into cents, rounded to the
// nearest cent.
//
// Rounded rather than truncated because these figures are summed: a report over
// two hundred time entries that lost a fraction of a cent on each would drift
// visibly away from the same numbers computed any other way, and the first
// person to notice would be checking the arithmetic by hand.
func costMinutes(minutes, centsPerHour int) int {
	if minutes <= 0 || centsPerHour <= 0 {
		return 0
	}
	return (minutes*centsPerHour + 30) / 60
}

// TotalCostCents is the whole cost of the work — parts plus the hours as they
// were booked.
//
// No rate argument: every hour carries the rate it was bought at, so this is a
// sum of recorded facts rather than a calculation against today's settings.
func (s ServiceTotals) TotalCostCents() int {
	return s.PartsCostCents + s.LaborCostCents
}

// AnyCost reports whether there is a money figure worth showing. False on a
// shop that has never set a rate, where every hour is uncosted and the parts
// column already says everything the money column would.
func (s ServiceTotals) AnyCost() bool {
	return s.LaborCostCents > 0
}

// FullyCosted reports whether every hour in this summary carries a rate. When
// false the money figure is a floor, not a total, and the surfaces showing it
// say so rather than letting it read as complete.
func (s ServiceTotals) FullyCosted() bool {
	return s.UncostedMinutes == 0
}

// ServiceCostWindow is one period of a cost roll-up, ready to render.
type ServiceCostWindow struct {
	// Label is the period in the words the card prints — "Last 90 days".
	Label string
	// Since is the start of the window, zero for all time. Carried so a caller
	// can say what was measured without re-deriving it.
	Since   time.Time
	Summary ServiceCostSummary
}

// ServiceAccountCost is one row of the cross-account table: what servicing one
// customer has taken over a period.
//
// The row the whole report exists for is the one where the hours are large and
// the machines are few — an account absorbing a tech's week for two grinders is
// unprofitable in a way no single ticket ever looks.
type ServiceAccountCost struct {
	CustomerID   uuid.UUID
	CustomerName string
	// MachineCount is how many machines the account has on the register, so a
	// big number can be read against how much there is to look after. A chain
	// with nine machines costing more than a single-site cafe is not a finding.
	MachineCount int
	// LastWorkOn is the most recent day anything was recorded — the difference
	// between an account that is expensive and one that was expensive.
	LastWorkOn *time.Time
	Summary    ServiceCostSummary
}

// ServiceAccountCostSort is how the cross-account table is ranked.
//
// Parts are in cents and work is in minutes, so blending them needs an hourly
// cost. Until a shop records one there is no single money figure to sort by,
// and inventing a rate would put a made-up number at the top of a report meant
// to settle arguments — so the reader picks which of the two scarce things to
// rank by. Once hours carry a rate, ServiceAccountCostByCost blends them and
// leads; both numbers stay in the table either way.
type ServiceAccountCostSort string

const (
	// ServiceAccountCostByHours ranks by time spent. The fallback ranking, and
	// the default on a shop with no costs to rank by: for a crew of two, hours
	// are the thing there is least of.
	//
	// It carries a real value rather than the empty string. An empty one cannot
	// be told apart from an absent query parameter, and a link that omits the
	// parameter reads as "give me the default" — which is how the Hours tab
	// came to return the cost ranking.
	ServiceAccountCostByHours ServiceAccountCostSort = "hours"
	// ServiceAccountCostByParts ranks by parts spend — the money that actually
	// left the building.
	ServiceAccountCostByParts ServiceAccountCostSort = "parts"
	// ServiceAccountCostByVisits ranks by how often somebody had to go out,
	// which catches the account that is death by a thousand twenty-minute
	// call-outs.
	ServiceAccountCostByVisits ServiceAccountCostSort = "visits"
	// ServiceAccountCostByCost ranks by parts plus costed hours — the single
	// figure the other three exist to approximate. Only available once a labour
	// rate is set; without one it falls back to hours, because a ranking by a
	// number nobody supplied would be a ranking by parts spend wearing a
	// misleading label.
	ServiceAccountCostByCost ServiceAccountCostSort = "cost"
)

// Valid reports whether s is a known sort. The empty string is deliberately
// not valid: it means "no sort was asked for", which is a different question
// with a different answer.
func (s ServiceAccountCostSort) Valid() bool {
	switch s {
	case ServiceAccountCostByHours, ServiceAccountCostByParts,
		ServiceAccountCostByVisits, ServiceAccountCostByCost:
		return true
	}
	return false
}

// ServiceAccountReport is the cross-account view: every account that took
// service work in a period, ranked, with the shop's own total beside it.
type ServiceAccountReport struct {
	Rows []ServiceAccountCost
	// Total is the sum of the rows shown. Summed over the returned rows rather
	// than asked for separately, so the footer can never disagree with the
	// column above it.
	Total ServiceCostSummary
	// Truncated says the limit bit and the table is not the whole picture. A
	// bounded list that does not say it is bounded reads as complete.
	Truncated bool
	// Limit is the cap that bit, so the page names the real number rather than
	// repeating it as a literal that can drift from the constant.
	Limit int
	// Since is the start of the window, zero for all time.
	Since time.Time
	Sort  ServiceAccountCostSort
	// CanCost says there is money to rank and report on: either an hour
	// somewhere carries a rate, or the shop has set one for the next. Decided
	// before the query, because a cost ranking with nothing to rank collapses
	// to ordering by parts spend alone — which is exactly the misleading label
	// this report was built to avoid.
	CanCost bool
	// Rates are what the *next* hour will cost. Since the snapshot moved the
	// rate onto each entry these no longer price anything in this report; they
	// are carried so the page can name the current rate in its footnote, and so
	// a shop that has just set one sees the money column appear before any hour
	// has been logged at it.
	Rates ServiceLaborRates
}

// ShowCost reports whether the money column belongs on the table.
//
// The same question as CanCost, and deliberately the same answer: a table that
// offered a Cost ranking without a Cost column, or the reverse, would be
// describing two different reports.
func (r ServiceAccountReport) ShowCost() bool { return r.CanCost }

// AnyServiceCost reports whether any window in a roll-up holds anything, which
// is how a card tells "nothing has been spent here" from "there is nothing to
// show". Reading that off the widest window is a thing a caller could get
// wrong, so it is done once here.
func AnyServiceCost(windows []ServiceCostWindow) bool {
	for _, w := range windows {
		if w.Summary.Any() {
			return true
		}
	}
	return false
}
