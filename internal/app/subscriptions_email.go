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
		IntervalDays: intervalDays(plan.Interval, plan.IntervalCount),
		NextChargeOn: sub.NextOrderAt,
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
		AccountURL:   s.email.BaseURL + "/account/subscriptions",
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

// SendPastDueEmail sends a payment-failed / past-due notice asking the customer
// to update their card. Read → send → audit.
func (s *SubscriptionService) SendPastDueEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID) error {
	return s.sendSubscriptionLifecycleEmail(ctx, pool, subscriptionID, customerID, lifecycleEmailSpec{
		templateName: "subscription_past_due",
		subject:      "We couldn't charge your card",
		tag:          "subscription-past-due",
		auditAction:  audit.AuditEmailSubscriptionPastDue,
		actorName:    "subscription_past_due_worker",
		buildData: func(customerName, productName, planName string) any {
			return emailtemplates.SubscriptionPastDueData{
				CustomerName: customerName,
				ProductName:  productName,
				PlanName:     planName,
				StoreName:    s.email.StoreName,
				StoreURL:     s.email.BaseURL,
				AccountURL:   s.email.BaseURL + "/account/subscriptions",
			}
		},
	})
}

// SendCancellationEmail sends a confirmation that a subscription was cancelled.
// Read → send → audit.
func (s *SubscriptionService) SendCancellationEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID) error {
	return s.sendSubscriptionLifecycleEmail(ctx, pool, subscriptionID, customerID, lifecycleEmailSpec{
		templateName: "subscription_cancelled",
		subject:      "Subscription cancelled",
		tag:          "subscription-cancelled",
		auditAction:  audit.AuditEmailSubscriptionCancelled,
		actorName:    "subscription_cancelled_worker",
		buildData: func(customerName, productName, planName string) any {
			return emailtemplates.SubscriptionCancelledData{
				CustomerName: customerName,
				ProductName:  productName,
				PlanName:     planName,
				StoreName:    s.email.StoreName,
				StoreURL:     s.email.BaseURL,
				AccountURL:   s.email.BaseURL + "/account/subscriptions",
			}
		},
	})
}

// SendDunningEndedEmail tells the customer their subscription has ended because
// the renewal charge never recovered (dunning exhausted). Distinct from a
// customer-initiated cancellation — the copy acknowledges the failed card and
// points them at restarting. Read → send → audit.
func (s *SubscriptionService) SendDunningEndedEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID) error {
	return s.sendSubscriptionLifecycleEmail(ctx, pool, subscriptionID, customerID, lifecycleEmailSpec{
		templateName: "subscription_dunning_ended",
		subject:      "Your subscription has ended",
		tag:          "subscription-ended",
		auditAction:  audit.AuditEmailSubscriptionEnded,
		actorName:    "subscription_ended_worker",
		buildData: func(customerName, productName, planName string) any {
			return emailtemplates.SubscriptionDunningEndedData{
				CustomerName: customerName,
				ProductName:  productName,
				PlanName:     planName,
				StoreName:    s.email.StoreName,
				StoreURL:     s.email.BaseURL,
				AccountURL:   s.email.BaseURL + "/account/subscriptions",
			}
		},
	})
}

// lifecycleEmailSpec captures the per-template specifics for past-due and
// cancellation emails — they share the same read-shape (sub + customer + plan +
// product) and the same three-phase send pattern, so the loader is hoisted
// into one helper.
type lifecycleEmailSpec struct {
	templateName string
	subject      string
	tag          string
	auditAction  string
	actorName    string
	buildData    func(customerName, productName, planName string) any
}

func (s *SubscriptionService) sendSubscriptionLifecycleEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID, spec lifecycleEmailSpec) error {
	var (
		sub         *domain.Subscription
		customer    *domain.Customer
		plan        *domain.SubscriptionPlan
		productName = "your subscription"
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

	html, text, err := s.email.Renderer.Render(spec.templateName, spec.buildData(customer.FirstName, productName, plan.Name))
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues(spec.templateName, "failed").Inc()
		return fmt.Errorf("render %s template: %w", spec.templateName, err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: spec.subject,
		HTML:    html,
		Text:    text,
		Tag:     spec.tag,
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues(spec.templateName, "failed").Inc()
		return fmt.Errorf("send %s email: %w", spec.templateName, err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    spec.actorName,
			Action:       spec.auditAction,
			ResourceType: "subscription",
			ResourceID:   sub.ID,
		})
	}); err != nil {
		return fmt.Errorf("audit %s sent: %w", spec.templateName, err)
	}

	s.metrics.EmailsSent.WithLabelValues(spec.templateName, "sent").Inc()
	return nil
}
