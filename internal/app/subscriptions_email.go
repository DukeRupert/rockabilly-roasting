package app

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/auth"
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

// pastDueStageSpecs is the per-rung template, subject, and tag for the dunning
// email ladder, keyed by the stage constants in renewal.go. Escalation lives in
// the copy, not in the frequency: three messages over two weeks, each saying
// something the last one didn't.
var pastDueStageSpecs = map[int]struct {
	template string
	subject  string
	tag      string
}{
	dunningEmailFirst: {
		template: "subscription_past_due",
		subject:  "We couldn't charge your card",
		tag:      "subscription-past-due",
	},
	dunningEmailReminder: {
		template: "subscription_past_due_reminder",
		subject:  "Your coffee's on hold",
		tag:      "subscription-past-due-reminder",
	},
	dunningEmailFinal: {
		template: "subscription_past_due_final",
		subject:  "Last call on your subscription",
		tag:      "subscription-past-due-final",
	},
}

// SendPastDueEmail sends one rung of the past-due email ladder, asking the
// customer to put a working card on file. stage selects the notice; an
// unrecognised stage (including the zero value on a job enqueued before the
// ladder existed) falls back to the first notice, which is the safe default —
// its copy makes sense at any point in the window. Read → send → audit.
func (s *SubscriptionService) SendPastDueEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID, stage int) error {
	spec, ok := pastDueStageSpecs[stage]
	if !ok {
		spec = pastDueStageSpecs[dunningEmailFirst]
	}
	return s.sendSubscriptionLifecycleEmail(ctx, pool, subscriptionID, customerID, lifecycleEmailSpec{
		templateName: spec.template,
		subject:      spec.subject,
		tag:          spec.tag,
		auditAction:  audit.AuditEmailSubscriptionPastDue,
		actorName:    "subscription_past_due_worker",
		buildData: func(d lifecycleEmailData) any {
			return emailtemplates.SubscriptionPastDueData{
				CustomerName:  d.CustomerName,
				ProductName:   d.ProductName,
				PlanName:      d.PlanName,
				HardDecline:   d.Sub.DunningHardDeclined(),
				EndsOn:        DunningExpiresAt(d.Sub),
				StoreName:     s.email.StoreName,
				StoreURL:      s.email.BaseURL,
				AccountURL:    s.email.BaseURL + "/account/subscriptions",
				UpdateCardURL: s.updateCardURL(subscriptionID),
			}
		},
	})
}

// updateCardURL mints the one-click "fix your card" link. Empty when no signer
// is wired or the secret is unset — the templates fall back to the sign-in link
// rather than emailing a token that could never be verified.
func (s *SubscriptionService) updateCardURL(subscriptionID uuid.UUID) string {
	if s.orderActions == nil || !s.orderActions.Enabled() {
		return ""
	}
	token := s.orderActions.Sign(auth.OrderActionUpdateCard, subscriptionID, time.Now())
	if token == "" {
		return ""
	}
	return fmt.Sprintf("%s/subscriptions/update-card?t=%s",
		strings.TrimRight(s.email.BaseURL, "/"), url.QueryEscape(token))
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
		buildData: func(d lifecycleEmailData) any {
			return emailtemplates.SubscriptionCancelledData{
				CustomerName: d.CustomerName,
				ProductName:  d.ProductName,
				PlanName:     d.PlanName,
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
		buildData: func(d lifecycleEmailData) any {
			return emailtemplates.SubscriptionDunningEndedData{
				CustomerName: d.CustomerName,
				ProductName:  d.ProductName,
				PlanName:     d.PlanName,
				StoreName:    s.email.StoreName,
				StoreURL:     s.email.BaseURL,
				AccountURL:   s.email.BaseURL + "/account/subscriptions",
			}
		},
	})
}

// SendSkipEmail tells the customer their next shipment has been skipped, when
// the following one bills, and how to put it back if the skip was a mistake.
// Sent for staff-initiated skips too — the customer should hear about a change
// to their schedule regardless of who made it. Read → send → audit.
//
// The undo link is the same shape as the switch-to-pickup link: a signed,
// expiring token that authorizes exactly one narrow, reversible change and
// nothing else. Without a signing secret the link is omitted and the template
// points at the account page instead of printing something unverifiable.
func (s *SubscriptionService) SendSkipEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID, skippedCount int) error {
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

	// The date the skip moved away from. Read from the undo snapshot so the
	// email states what actually changed; if the snapshot has already been
	// retired (a renewal or another change landed first) the mail still goes
	// out, just without the "was billing" comparison or the undo link.
	var previousOrderOn time.Time
	undoURL := ""
	if undo, ok := sub.SkipUndo(); ok && undo.AppliedNextOrderAt.Equal(sub.NextOrderAt) {
		previousOrderOn = undo.NextOrderAt
		undoURL = s.undoSkipURL(sub.ID)
	}

	html, text, err := s.email.Renderer.Render("subscription_skipped", emailtemplates.SubscriptionSkippedData{
		CustomerName:    customer.FirstName,
		ProductName:     productName,
		PlanName:        plan.Name,
		SkippedCount:    skippedCount,
		PreviousOrderOn: previousOrderOn,
		NextChargeOn:    sub.NextOrderAt,
		UndoURL:         undoURL,
		StoreName:       s.email.StoreName,
		StoreURL:        s.email.BaseURL,
		AccountURL:      s.email.BaseURL + "/account/subscriptions",
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_skipped", "failed").Inc()
		return fmt.Errorf("render subscription skipped template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Your next shipment is skipped",
		HTML:    html,
		Text:    text,
		Tag:     "subscription-skipped",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_skipped", "failed").Inc()
		return fmt.Errorf("send subscription skipped email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "subscription_skipped_worker",
			Action:       audit.AuditEmailSubscriptionSkipped,
			ResourceType: "subscription",
			ResourceID:   sub.ID,
		})
	}); err != nil {
		return fmt.Errorf("audit subscription skipped email sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("subscription_skipped", "sent").Inc()
	return nil
}

// SendSkipUndoneEmail tells the customer a skip has been reversed and their
// next shipment now bills sooner. Sent only for staff-initiated undos: a
// customer who undid it themselves — from the emailed link or their account —
// has already seen it confirmed on screen, and a second notice would be noise.
// skippedTo is passed in rather than read back, because undoing clears the
// snapshot that held it. Read → send → audit.
func (s *SubscriptionService) SendSkipUndoneEmail(ctx context.Context, pool *pgxpool.Pool, subscriptionID, customerID uuid.UUID, skippedTo time.Time) error {
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

	html, text, err := s.email.Renderer.Render("subscription_skip_undone", emailtemplates.SubscriptionSkipUndoneData{
		CustomerName: customer.FirstName,
		ProductName:  productName,
		PlanName:     plan.Name,
		SkippedTo:    skippedTo,
		NextChargeOn: sub.NextOrderAt,
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
		AccountURL:   s.email.BaseURL + "/account/subscriptions",
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_skip_undone", "failed").Inc()
		return fmt.Errorf("render subscription skip undone template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Your skipped shipment is back on",
		HTML:    html,
		Text:    text,
		Tag:     "subscription-skip-undone",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("subscription_skip_undone", "failed").Inc()
		return fmt.Errorf("send subscription skip undone email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "subscription_skip_undone_worker",
			Action:       audit.AuditEmailSubscriptionSkipUndone,
			ResourceType: "subscription",
			ResourceID:   sub.ID,
		})
	}); err != nil {
		return fmt.Errorf("audit subscription skip undone email sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("subscription_skip_undone", "sent").Inc()
	return nil
}

// undoSkipURL mints the one-click undo link. Empty when no signer is wired or
// the secret is unset — callers omit the link rather than emailing one that
// could never be verified.
func (s *SubscriptionService) undoSkipURL(subscriptionID uuid.UUID) string {
	if s.orderActions == nil || !s.orderActions.Enabled() {
		return ""
	}
	token := s.orderActions.Sign(auth.OrderActionUndoSkip, subscriptionID, time.Now())
	if token == "" {
		return ""
	}
	return fmt.Sprintf("%s/subscriptions/undo-skip?t=%s",
		strings.TrimRight(s.email.BaseURL, "/"), url.QueryEscape(token))
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
	buildData    func(lifecycleEmailData) any
}

// lifecycleEmailData is what the loader resolved before rendering. The
// subscription is included because the past-due ladder needs more than display
// names — it reads the dunning state to decide its copy and to mint a one-click
// card link.
type lifecycleEmailData struct {
	CustomerName string
	ProductName  string
	PlanName     string
	Sub          *domain.Subscription
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

	html, text, err := s.email.Renderer.Render(spec.templateName, spec.buildData(lifecycleEmailData{
		CustomerName: customer.FirstName,
		ProductName:  productName,
		PlanName:     plan.Name,
		Sub:          sub,
	}))
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
