package domain

import (
	"time"

	"github.com/google/uuid"
)

// WebhookEvent tracks inbound webhook events for idempotency.
type WebhookEvent struct {
	ID            uuid.UUID
	Provider      string
	EventID       string
	EventType     string
	Payload       []byte
	ProcessedAt   *time.Time
	FailedAt      *time.Time
	FailureReason *string
	CreatedAt     time.Time
}
