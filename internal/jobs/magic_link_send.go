package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// MagicLinkSendWorker sends a magic link email to a customer.
type MagicLinkSendWorker struct {
	river.WorkerDefaults[MagicLinkSendArgs]
	customers *store.CustomerStore
	pool      *pgxpool.Pool
	mailer    email.Sender
	renderer  *emailtemplates.Renderer
	fromAddr  string
	baseURL   string
	storeName string
}

// NewMagicLinkSendWorker creates a new MagicLinkSendWorker.
func NewMagicLinkSendWorker(customers *store.CustomerStore, pool *pgxpool.Pool, mailer email.Sender, renderer *emailtemplates.Renderer, fromAddr, baseURL, storeName string) *MagicLinkSendWorker {
	return &MagicLinkSendWorker{
		customers: customers,
		pool:      pool,
		mailer:    mailer,
		renderer:  renderer,
		fromAddr:  fromAddr,
		baseURL:   baseURL,
		storeName: storeName,
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
	if job.Args.Next != "" {
		magicURL += "&next=" + url.QueryEscape(job.Args.Next)
	}
	html, text, err := w.renderer.Render("magic_link", emailtemplates.MagicLinkData{
		CustomerName: customer.FirstName,
		MagicLinkURL: magicURL,
		ExpiresIn:    "15 minutes",
		StoreName:    w.storeName,
		StoreURL:     w.baseURL,
	})
	if err != nil {
		return fmt.Errorf("render magic link template: %w", err)
	}

	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customer.Email,
		Subject: "Your sign-in link",
		HTML:    html,
		Text:    text,
		Tag:     "magic-link",
	})
	if err != nil {
		return fmt.Errorf("send magic link email: %w", err)
	}

	slog.Info("magic link email sent",
		"customer_id", customer.ID,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
