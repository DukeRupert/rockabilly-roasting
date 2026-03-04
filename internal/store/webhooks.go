package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/store/sqlcgen"
)

// WebhookStore provides database access for webhook event tracking.
type WebhookStore struct{}

// NewWebhookStore creates a new WebhookStore.
func NewWebhookStore() *WebhookStore {
	return &WebhookStore{}
}

// Create inserts a new webhook event. Returns (nil, nil) if the event already exists
// (ON CONFLICT DO NOTHING).
func (s *WebhookStore) Create(ctx context.Context, tx pgx.Tx, provider, eventID, eventType string, payload json.RawMessage) (*domain.WebhookEvent, error) {
	row, err := sqlcgen.New(tx).CreateWebhookEvent(ctx, sqlcgen.CreateWebhookEventParams{
		ID:        uuid.New(),
		Provider:  provider,
		EventID:   eventID,
		EventType: eventType,
		Payload:   payload,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("insert webhook event: %w", err)
	}
	return webhookEventFromRow(row), nil
}

// GetByProviderAndEventID returns a webhook event by provider and event ID.
func (s *WebhookStore) GetByProviderAndEventID(ctx context.Context, tx pgx.Tx, provider, eventID string) (*domain.WebhookEvent, error) {
	row, err := sqlcgen.New(tx).GetWebhookEventByProviderAndEventID(ctx, sqlcgen.GetWebhookEventByProviderAndEventIDParams{
		Provider: provider,
		EventID:  eventID,
	})
	if err != nil {
		return nil, fmt.Errorf("get webhook event: %w", err)
	}
	return webhookEventFromRow(row), nil
}

// MarkProcessed marks a webhook event as processed.
func (s *WebhookStore) MarkProcessed(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	if err := sqlcgen.New(tx).MarkWebhookEventProcessed(ctx, id); err != nil {
		return fmt.Errorf("mark webhook processed: %w", err)
	}
	return nil
}

// MarkFailed marks a webhook event as failed with a reason.
func (s *WebhookStore) MarkFailed(ctx context.Context, tx pgx.Tx, id uuid.UUID, reason *string) error {
	err := sqlcgen.New(tx).MarkWebhookEventFailed(ctx, sqlcgen.MarkWebhookEventFailedParams{
		ID:            id,
		FailureReason: reason,
	})
	if err != nil {
		return fmt.Errorf("mark webhook failed: %w", err)
	}
	return nil
}

// ListUnprocessed returns all unprocessed, non-failed webhook events.
func (s *WebhookStore) ListUnprocessed(ctx context.Context, tx pgx.Tx) ([]domain.WebhookEvent, error) {
	rows, err := sqlcgen.New(tx).ListUnprocessedWebhookEvents(ctx)
	if err != nil {
		return nil, fmt.Errorf("list unprocessed webhooks: %w", err)
	}
	events := make([]domain.WebhookEvent, len(rows))
	for i, r := range rows {
		events[i] = *webhookEventFromRow(r)
	}
	return events, nil
}

// --- Row converters ---

func webhookEventFromRow(r sqlcgen.WebhookEvent) *domain.WebhookEvent {
	return &domain.WebhookEvent{
		ID:            r.ID,
		Provider:      r.Provider,
		EventID:       r.EventID,
		EventType:     r.EventType,
		Payload:       r.Payload,
		ProcessedAt:   timestampFromPG(r.ProcessedAt),
		FailedAt:      timestampFromPG(r.FailedAt),
		FailureReason: r.FailureReason,
		CreatedAt:     r.CreatedAt,
	}
}
