package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

// AnnouncementAudience is who a staff-composed notice goes to.
type AnnouncementAudience string

const (
	// AnnouncementAudienceAll is every emailable customer, retail and wholesale.
	AnnouncementAudienceAll AnnouncementAudience = "all"
	// AnnouncementAudienceRetail is retail customers who have actually bought.
	AnnouncementAudienceRetail AnnouncementAudience = "retail"
	// AnnouncementAudienceWholesale is approved wholesale accounts.
	AnnouncementAudienceWholesale AnnouncementAudience = "wholesale"
)

// Valid reports whether a is one of the known audiences. Used to reject a
// hand-edited form value before it reaches the audience query.
func (a AnnouncementAudience) Valid() bool {
	switch a {
	case AnnouncementAudienceAll, AnnouncementAudienceRetail, AnnouncementAudienceWholesale:
		return true
	}
	return false
}

// Label is the audience as staff see it in the admin.
func (a AnnouncementAudience) Label() string {
	switch a {
	case AnnouncementAudienceAll:
		return "All customers"
	case AnnouncementAudienceRetail:
		return "Retail customers"
	case AnnouncementAudienceWholesale:
		return "Wholesale accounts"
	}
	return string(a)
}

// AnnouncementStatus is where a notice is in its lifecycle.
type AnnouncementStatus string

const (
	// AnnouncementStatusScheduled is composed and waiting for its send time.
	// This is the only status that can be cancelled.
	AnnouncementStatusScheduled AnnouncementStatus = "scheduled"
	// AnnouncementStatusSending means the dispatcher has resolved the audience
	// and queued the individual sends.
	AnnouncementStatusSending AnnouncementStatus = "sending"
	// AnnouncementStatusSent means every send was queued and the dispatcher
	// finished. Individual messages may still be in flight — per-recipient
	// results live in the audit log.
	AnnouncementStatusSent AnnouncementStatus = "sent"
	// AnnouncementStatusCancelled means staff pulled it before it dispatched.
	AnnouncementStatusCancelled AnnouncementStatus = "cancelled"
)

// Announcement is one staff-composed notice to a customer audience.
type Announcement struct {
	ID       uuid.UUID
	Subject  string
	Body     string
	Audience AnnouncementAudience
	Status   AnnouncementStatus
	// ScheduledAt is set even for an immediate send (it holds the moment the
	// send was requested), so ordering and the cancel window never have to
	// branch on a nil.
	ScheduledAt time.Time
	SentAt      *time.Time
	// RecipientCount is what the dispatcher actually resolved, nil until it
	// runs. The compose screen's estimate is not stored — it would go stale.
	RecipientCount *int
	CreatedBy      *uuid.UUID
	CreatedByName  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Cancellable reports whether staff can still pull this notice. Once the
// dispatcher has started fanning out, mail is already leaving and there is
// nothing honest to undo.
func (a *Announcement) Cancellable() bool {
	return a.Status == AnnouncementStatusScheduled
}

// AnnouncementRecipient is one account on an announcement's audience list.
// One row per *customer*, not per address — teammates on a wholesale account
// are resolved at send time so a preference changed since the fan-out is
// honoured.
type AnnouncementRecipient struct {
	CustomerID  uuid.UUID
	Email       string
	CompanyName *string
	FirstName   string
	LastName    string
	AccountType AccountType
}

// DisplayName is the label staff recognize the recipient by — company name for
// a wholesale account, then the contact's name, then the address.
func (r AnnouncementRecipient) DisplayName() string {
	if r.CompanyName != nil && strings.TrimSpace(*r.CompanyName) != "" {
		return strings.TrimSpace(*r.CompanyName)
	}
	if name := strings.TrimSpace(r.FirstName + " " + r.LastName); name != "" {
		return name
	}
	return r.Email
}
