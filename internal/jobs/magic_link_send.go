package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// MagicLinkSendWorker sends a magic link email to a customer.
type MagicLinkSendWorker struct {
	river.WorkerDefaults[MagicLinkSendArgs]
	customers *store.CustomerStore
	pool      *pgxpool.Pool
	mailer    email.Sender
	fromAddr  string
	baseURL   string
}

// NewMagicLinkSendWorker creates a new MagicLinkSendWorker.
func NewMagicLinkSendWorker(customers *store.CustomerStore, pool *pgxpool.Pool, mailer email.Sender, fromAddr, baseURL string) *MagicLinkSendWorker {
	return &MagicLinkSendWorker{
		customers: customers,
		pool:      pool,
		mailer:    mailer,
		fromAddr:  fromAddr,
		baseURL:   baseURL,
	}
}

// Work processes a magic link send job.
func (w *MagicLinkSendWorker) Work(ctx context.Context, job *river.Job[MagicLinkSendArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	customer, err := w.customers.GetByID(ctx, tx, job.Args.CustomerID)
	if err != nil {
		return fmt.Errorf("get customer %s: %w", job.Args.CustomerID, err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	magicURL := fmt.Sprintf("%s/account/magic?token=%s", w.baseURL, job.Args.RawToken)
	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customer.Email,
		Subject: "Your login link",
		HTML:    fmt.Sprintf(`<p>Click <a href="%s">here</a> to log in. This link expires in 15 minutes.</p>`, magicURL),
		Text:    fmt.Sprintf("Log in here: %s\n\nThis link expires in 15 minutes.", magicURL),
		Tag:     "magic-link",
	})
	if err != nil {
		return fmt.Errorf("send magic link email: %w", err)
	}

	slog.Info("magic link email sent",
		"customer_id", customer.ID,
		"email", customer.Email,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
