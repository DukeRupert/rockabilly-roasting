package domain

import (
	"time"

	"github.com/google/uuid"
)

// SessionActorType identifies the type of actor in a session.
type SessionActorType string

const (
	SessionActorTypeCustomer SessionActorType = "customer"
	SessionActorTypeStaff    SessionActorType = "staff"
	// SessionActorTypeCustomerUser is an additional login on a wholesale
	// account. ActorID is a customer_users.id, NOT a customers.id — resolve it
	// to the owning account before scoping any data access.
	SessionActorTypeCustomerUser SessionActorType = "customer_user"
)

// Session represents an authenticated session.
type Session struct {
	ID         uuid.UUID
	ActorType  SessionActorType
	ActorID    uuid.UUID
	TokenHash  string
	IPAddress  *string
	UserAgent  *string
	LastSeenAt time.Time
	ExpiresAt  time.Time
	CreatedAt  time.Time
}

// ResetToken represents a password reset token.
type ResetToken struct {
	ID        uuid.UUID
	ActorType SessionActorType
	ActorID   uuid.UUID
	TokenHash string
	ExpiresAt time.Time
	UsedAt    *time.Time
	CreatedAt time.Time
}

// EmailVerification represents an email verification token.
type EmailVerification struct {
	ID         uuid.UUID
	CustomerID uuid.UUID
	TokenHash  string
	ExpiresAt  time.Time
	VerifiedAt *time.Time
	CreatedAt  time.Time
}
