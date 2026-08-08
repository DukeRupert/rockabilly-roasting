package domain

import (
	"time"

	"github.com/google/uuid"
)

// CustomerUserRole is the permission level of an additional login on a
// wholesale account. v1 has exactly one role and performs no role checks — the
// type exists so that introducing real permissions later does not require
// reshaping stored rows.
type CustomerUserRole string

const (
	// CustomerUserRoleMember is the only role in v1: full access to the
	// account's portal, same as the primary login.
	CustomerUserRoleMember CustomerUserRole = "member"
)

// CustomerUser is an additional person who can sign in to a wholesale account.
// It is a login, not an account: orders, carts, addresses, subscriptions and
// pricing are all keyed on CustomerID, never on the CustomerUser's own ID.
//
// The account's primary login is NOT represented here — it lives on the
// customers row itself (see migration 063).
type CustomerUser struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	Email      string
	// PasswordHash is nil until the invitee redeems their invite and sets one.
	PasswordHash *string
	Name         string
	Role         CustomerUserRole
	// ReceivesNotifications opts this user into the account's transactional
	// mail. Defaults to false so inviting a teammate never silently redirects
	// order confirmations or billing email.
	ReceivesNotifications bool
	InvitedAt             time.Time
	LastLoginAt           *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// HasAcceptedInvite reports whether the user has completed setup by choosing a
// password. Pending invitees cannot sign in.
func (u *CustomerUser) HasAcceptedInvite() bool {
	return u.PasswordHash != nil
}

// DisplayName returns the user's name, falling back to their email address so
// the team list never renders a blank row for someone who was invited before
// they told us their name.
func (u *CustomerUser) DisplayName() string {
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}
