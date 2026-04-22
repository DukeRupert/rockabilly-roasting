package app

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// SendConfirmationEmail sends a subscription-active confirmation email. Uses
// the three-phase pattern (read → send → audit).
func (s *SubscriptionService) SendConfirmationEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID) error {
	var (
		sub         *domain.Subscription
		customer    *domain.Customer
		plan        *domain.SubscriptionPlan
		productName = "Product"
	)

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		sub, err = s.subscriptions.GetByIDAsStaff(ctx, tx, subscriptionID)
		if err != nil {
			return fmt.Errorf("get subscription %s: %w", subscriptionID, err)
		}
		customer, err = s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		plan, err = s.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
		if err != nil {
			return fmt.Errorf("get plan %s: %w", sub.PlanID, err)
		}
		if variant, err := s.catalog.GetVariantByID(ctx, tx, sub.VariantID); err == nil {
			if product, err := s.catalog.GetProductByID(ctx, tx, variant.ProductID); err == nil {
				productName = product.Title
			}
		}
		return nil
	}); err != nil {
		return err
	}

	html, text, err := s.email.Renderer.Render("subscription_confirm", emailtemplates.SubscriptionConfirmData{
		CustomerName: customer.FirstName,
		PlanName:     plan.Name,
		ProductName:  productName,
		Quantity:     sub.Quantity,
		Interval:     string(plan.Interval),
		UnitPrice:    0, // price is on the order, not the subscription
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_confirm", "failed").Inc()
		return fmt.Errorf("render subscription confirm template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Your subscription is active",
		HTML:    html,
		Text:    text,
		Tag:     "subscription-confirm",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_confirm", "failed").Inc()
		return fmt.Errorf("send subscription confirm email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "subscription_confirm_worker",
			Action:       audit.AuditEmailSubscriptionConfirmed,
			ResourceType: "subscription",
			ResourceID:   sub.ID,
		})
	}); err != nil {
		return fmt.Errorf("audit subscription confirm sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("subscription_confirm", "sent").Inc()
	return nil
}
