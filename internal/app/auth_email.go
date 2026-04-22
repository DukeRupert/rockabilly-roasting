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
