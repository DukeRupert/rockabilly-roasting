package domain

import (
	"time"

	"github.com/google/uuid"
)

// QBCredentials holds the encrypted OAuth2 tokens for a QuickBooks Online connection.
// Token fields are encrypted at rest — decryption happens in the platform/quickbooks package.
type QBCredentials struct {
	ID               uuid.UUID
	TenantID         uuid.UUID
	RealmID          string
	AccessToken      string // encrypted
	RefreshToken     string // encrypted
	AccessExpiresAt  time.Time
	RefreshExpiresAt time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
