package app

import (
	"context"
	"fmt"
	"net/url"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// SendMagicLink renders and sends a retail magic-link sign-in email. The token
// must already have been created via CreateMagicLinkToken; this method only
// composes + sends the notification. Orchestration (fetch → external send →
// audit) happens in three phases per the pattern in RenewalService:
//
//  1. Load customer inside a read tx.
//  2. Render + send email outside any tx.
//  3. Record audit + bump metric in a second tx.
func (s *AuthService) SendMagicLink(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID, rawToken, next string) error {
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

	magicURL := s.email.BaseURL + "/account/magic?token=" + url.QueryEscape(rawToken)
	if next != "" {
		magicURL += "&next=" + url.QueryEscape(next)
	}

	html, text, err := s.email.Renderer.Render("magic_link", emailtemplates.MagicLinkData{
		CustomerName: customer.FirstName,
		MagicLinkURL: magicURL,
		ExpiresIn:    "15 minutes",
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("magic_link", "failed").Inc()
		return fmt.Errorf("render magic link template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Your sign-in link",
		HTML:    html,
		Text:    text,
		Tag:     "magic-link",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("magic_link", "failed").Inc()
		return fmt.Errorf("send magic link email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "magic_link_worker",
			Action:       audit.AuditEmailMagicLinkSent,
			ResourceType: "customer",
			ResourceID:   customer.ID,
		})
	}); err != nil {
		return fmt.Errorf("audit magic link sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("magic_link", "sent").Inc()
	return nil
}

// SendVerificationEmail renders and sends an email-verification message. The
// underlying token is a magic-link token (created via CreateMagicLinkToken),
// so redeeming the link both verifies the email and signs the customer in —
// but the email itself is framed as a verification, not a sign-in. Same
// three-phase pattern as SendMagicLink.
func (s *AuthService) SendVerificationEmail(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID, rawToken, next string) error {
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

	verifyURL := s.email.BaseURL + "/account/magic?token=" + url.QueryEscape(rawToken)
	if next != "" {
		verifyURL += "&next=" + url.QueryEscape(next)
	}

	html, text, err := s.email.Renderer.Render("verify_email", emailtemplates.VerifyEmailData{
		CustomerName: customer.FirstName,
		VerifyURL:    verifyURL,
		ExpiresIn:    "15 minutes",
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("email_verification", "failed").Inc()
		return fmt.Errorf("render verify email template: %w", err)
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: "Verify your email",
		HTML:    html,
		Text:    text,
		Tag:     "email-verification",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("email_verification", "failed").Inc()
		return fmt.Errorf("send verify email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "email_verify_worker",
			Action:       audit.AuditEmailVerificationSent,
			ResourceType: "customer",
			ResourceID:   customer.ID,
		})
	}); err != nil {
		return fmt.Errorf("audit verify email sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("email_verification", "sent").Inc()
	return nil
}

// SendPasswordResetEmail renders and sends a self-service password reset email.
// The token must already have been created via CreateSetupToken; this method
// only composes + sends the notification (same three-phase pattern as
// SendMagicLink). It reuses the password_setup template and the
// /account/password-setup consumption page, so a reset lands on the same page as
// an admin-triggered setup. The wording adapts based on whether the customer
// already has a password: "Reset" for existing passwords, "Set" for a first-time
// password (e.g. a guest-checkout customer recovering access).
//
// Unlike SendPasswordSetupEmail (staff-triggered), this records a system actor —
// the customer initiated it anonymously from the public forgot-password page.
func (s *AuthService) SendPasswordResetEmail(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID, rawToken string) error {
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

	isReset := customer.PasswordHash != nil
	setupURL := s.email.BaseURL + "/account/password-setup?token=" + url.QueryEscape(rawToken)

	html, text, err := s.email.Renderer.Render("password_setup", emailtemplates.PasswordSetupData{
		CustomerName: customer.FirstName,
		SetupURL:     setupURL,
		IsReset:      isReset,
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("password_reset", "failed").Inc()
		return fmt.Errorf("render password reset template: %w", err)
	}

	subject := "Set your password"
	if isReset {
		subject = "Reset your password"
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: subject,
		HTML:    html,
		Text:    text,
		Tag:     "password-reset",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("password_reset", "failed").Inc()
		return fmt.Errorf("send password reset email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "password_reset_request",
			Action:       audit.AuditEmailPasswordSetupSent,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata:     map[string]any{"reset": isReset, "self_service": true},
		})
	}); err != nil {
		return fmt.Errorf("audit password reset email sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("password_reset", "sent").Inc()
	return nil
}

// SendPasswordSetupEmail mints a setup token for the customer and emails them a
// link to set (or reset) their password. Triggered by staff from the admin
// customer page when a customer cannot sign in. The email wording adapts based
// on whether the customer already has a password.
//
// Three-phase pattern: token creation in tx 1, email send outside tx, audit in
// tx 2. actor identifies the staff member who triggered the send.
func (s *AuthService) SendPasswordSetupEmail(ctx context.Context, pool *pgxpool.Pool, customerID uuid.UUID, actor Actor) error {
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
		token, err := s.CreateSetupToken(ctx, tx, customerID)
		if err != nil {
			return fmt.Errorf("create setup token: %w", err)
		}
		rawToken = token
		return nil
	}); err != nil {
		return err
	}

	isReset := customer.PasswordHash != nil
	setupURL := s.email.BaseURL + "/account/password-setup?token=" + url.QueryEscape(rawToken)

	html, text, err := s.email.Renderer.Render("password_setup", emailtemplates.PasswordSetupData{
		CustomerName: customer.FirstName,
		SetupURL:     setupURL,
		IsReset:      isReset,
		StoreName:    s.email.StoreName,
		StoreURL:     s.email.BaseURL,
	})
	if err != nil {
		s.metrics.EmailsSent.WithLabelValues("password_setup", "failed").Inc()
		return fmt.Errorf("render password setup template: %w", err)
	}

	subject := "Set your password"
	if isReset {
		subject = "Reset your password"
	}

	if _, err := s.email.Mailer.Send(ctx, email.Message{
		From:    s.email.FromAddr,
		To:      customer.Email,
		Subject: subject,
		HTML:    html,
		Text:    text,
		Tag:     "password-setup",
	}); err != nil {
		s.metrics.EmailsSent.WithLabelValues("password_setup", "failed").Inc()
		return fmt.Errorf("send password setup email: %w", err)
	}

	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    actor.Type,
			ActorID:      actor.ID,
			ActorName:    actor.Name,
			Action:       audit.AuditEmailPasswordSetupSent,
			ResourceType: "customer",
			ResourceID:   customer.ID,
			Metadata:     map[string]any{"reset": isReset},
		})
	}); err != nil {
		return fmt.Errorf("audit password setup email sent: %w", err)
	}

	s.metrics.EmailsSent.WithLabelValues("password_setup", "sent").Inc()
	return nil
}
