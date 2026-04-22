package app

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store"
)

// WebhookService persists inbound webhook events for idempotency and audit.
// The webhook_events table is itself the audit record for webhooks — no separate
// audit_log entries are recorded here.
type WebhookService struct {
	events *store.WebhookStore
}

// NewWebhookService creates a new WebhookService.
func NewWebhookService(events *store.WebhookStore) *WebhookService {
	return &WebhookService{events: events}
}

// PersistEvent inserts a new webhook event. If the provider+eventID pair already
// exists, it returns (nil, nil) — callers must treat a nil event as "duplicate,
// skip processing".
func (s *WebhookService) PersistEvent(ctx context.Context, tx pgx.Tx, provider, eventID, eventType string, payload json.RawMessage) (*domain.WebhookEvent, error) {
	event, err := s.events.Create(ctx, tx, provider, eventID, eventType, payload)
	if err != nil {
		return nil, fmt.Errorf("persist webhook event: %w", err)
	}
	return event, nil
}

// MarkProcessed marks a webhook event as successfully processed.
func (s *WebhookService) MarkProcessed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := s.events.MarkProcessed(ctx, tx, id); err != nil {
		return fmt.Errorf("mark webhook processed: %w", err)
	}
	return nil
}

// MarkFailed marks a webhook event as failed with a reason string.
func (s *WebhookService) MarkFailed(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason string) error {
	if err := s.events.MarkFailed(ctx, tx, id, &reason); err != nil {
		return fmt.Errorf("mark webhook failed: %w", err)
	}
	return nil
}
