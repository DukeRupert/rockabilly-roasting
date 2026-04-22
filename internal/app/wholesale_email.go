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

// SendApplicationNotice emails staff when a new wholesale application is
// submitted. Recipient is EmailEnv.StaffEmail.
func (s *WholesaleService) SendApplicationNotice(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID) error {
	var customer *domain.Customer
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c
		return nil
	}); err != nil {
		return err
	}

	companyName := "Unknown"
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	reviewURL := fmt.Sprintf("%s/admin/wholesale", s.email.BaseURL)
	html, text, err := s.email.Renderer.Render("wholesale_application", emailtemplates.WholesaleApplicationData{
		CompanyName:   companyName,
		CustomerEmail: customer.Email,
		ReviewURL:     reviewURL,
		StoreName:     s.email.StoreName,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_application", "failed").Inc()
		return fmt.Errorf("render wholesale application template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      s.email.StaffEmail,
		Subject: fmt.Sprintf("New wholesale application: %s", companyName),
		HTML:    html,
		Text:    text,
		Tag:     "wholesale-application",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_application", "failed").Inc()
		return fmt.Errorf("send wholesale application notification: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "wholesale_application_worker",
			Action:       audit.AuditEmailWholesaleApplicationReceived,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata:     map[string]any{"company": companyName},
		})
	}); err != nil {
		return fmt.Errorf("audit wholesale application notice: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("wholesale_application", "sent").Inc()
	return nil
}

// SendApprovalEmail mints a password-setup token for the approved customer
// and emails them a setup link. Token creation is in tx 1, email send is
// outside tx, audit is recorded in tx 2.
func (s *WholesaleService) SendApprovalEmail(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID) error {
	var (
		customer *domain.Customer
		rawToken string
	)

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c
		token, err := s.auth.CreateSetupToken(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("create setup token: %w", err)
		}
		rawToken = token
		return nil
	}); err != nil {
		return err
	}

	companyName := "there"
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	setupURL := fmt.Sprintf("%s/wholesale/setup?token=%s", s.email.BaseURL, rawToken)
	html, text, err := s.email.Renderer.Render("wholesale_approved", emailtemplates.WholesaleApprovedData{
		CompanyName: companyName,
		SetupURL:    setupURL,
		StoreName:   s.email.StoreName,
		StoreURL:    s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_approved", "failed").Inc()
		return fmt.Errorf("render wholesale approved template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Your wholesale account has been approved",
		HTML:    html,
		Text:    text,
		Tag:     "wholesale-approved",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_approved", "failed").Inc()
		return fmt.Errorf("send wholesale approved email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "wholesale_approved_worker",
			Action:       audit.AuditEmailWholesaleApproved,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata:     map[string]any{"company": companyName},
		})
	}); err != nil {
		return fmt.Errorf("audit wholesale approved email: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("wholesale_approved", "sent").Inc()
	return nil
}

// SendSuspensionEmail emails a suspended wholesale customer.
func (s *WholesaleService) SendSuspensionEmail(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID) error {
	var customer *domain.Customer
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		c, err := s.customers.GetByID(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("get customer %s: %w", customerID, err)
		}
		customer = c
		return nil
	}); err != nil {
		return err
	}

	companyName := "there"
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	html, text, err := s.email.Renderer.Render("wholesale_suspended", emailtemplates.WholesaleSuspendedData{
		CompanyName: companyName,
		StoreName:   s.email.StoreName,
		StoreURL:    s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_suspended", "failed").Inc()
		return fmt.Errorf("render wholesale suspended template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Your wholesale account has been suspended",
		HTML:    html,
		Text:    text,
		Tag:     "wholesale-suspended",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("wholesale_suspended", "failed").Inc()
		return fmt.Errorf("send wholesale suspended email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "wholesale_suspended_worker",
			Action:       audit.AuditEmailWholesaleSuspended,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata:     map[string]any{"company": companyName},
		})
	}); err != nil {
		return fmt.Errorf("audit wholesale suspended email: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("wholesale_suspended", "sent").Inc()
	return nil
}
