package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// SubscriptionService contains business logic for subscriptions.
type SubscriptionService struct {
	subscriptions *store.SubscriptionStore
	orders        *store.OrderStore
	audit         *audit.AuditWriter
	metrics       *metrics.Registry
}

// NewSubscriptionService creates a new SubscriptionService.
func NewSubscriptionService(
	subscriptions *store.SubscriptionStore,
	orders *store.OrderStore,
	audit *audit.AuditWriter,
	metrics *metrics.Registry,
) *SubscriptionService {
	return &SubscriptionService{
		subscriptions: subscriptions,
		orders:        orders,
		audit:         audit,
		metrics:       metrics,
	}
}

// GetSubscription returns a subscription by ID.
func (s *SubscriptionService) GetSubscription(ctx context.Context, tx pgx.Tx, id uuid.UUID) (*domain.Subscription, error) {
	sub, err := s.subscriptions.GetByID(ctx, tx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("get subscription: %w", err)
	}
	return sub, nil
}

// ListSubscriptions returns subscriptions matching the given filter.
func (s *SubscriptionService) ListSubscriptions(ctx context.Context, tx pgx.Tx, f store.SubscriptionFilter) ([]domain.Subscription, error) {
	subs, err := s.subscriptions.List(ctx, tx, f)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions: %w", err)
	}
	return subs, nil
}

// ListPlans returns all subscription plans.
func (s *SubscriptionService) ListPlans(ctx context.Context, tx pgx.Tx) ([]domain.SubscriptionPlan, error) {
	plans, err := s.subscriptions.ListPlans(ctx, tx)
	if err != nil {
		return nil, fmt.Errorf("list subscription plans: %w", err)
	}
	return plans, nil
}
