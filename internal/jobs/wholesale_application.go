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

// WholesaleApplicationNotifyWorker sends a notification to staff when a new wholesale application is submitted.
type WholesaleApplicationNotifyWorker struct {
	river.WorkerDefaults[WholesaleApplicationNotifyArgs]
	customers  *store.CustomerStore
	pool       *pgxpool.Pool
	mailer     email.Sender
	fromAddr   string
	staffEmail string
	baseURL    string
}

// NewWholesaleApplicationNotifyWorker creates a new WholesaleApplicationNotifyWorker.
func NewWholesaleApplicationNotifyWorker(customers *store.CustomerStore, pool *pgxpool.Pool, mailer email.Sender, fromAddr, staffEmail, baseURL string) *WholesaleApplicationNotifyWorker {
	return &WholesaleApplicationNotifyWorker{
		customers:  customers,
		pool:       pool,
		mailer:     mailer,
		fromAddr:   fromAddr,
		staffEmail: staffEmail,
		baseURL:    baseURL,
	}
}

// Work processes a wholesale application notification job.
func (w *WholesaleApplicationNotifyWorker) Work(ctx context.Context, job *river.Job[WholesaleApplicationNotifyArgs]) error {
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

	companyName := "Unknown"
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	reviewURL := fmt.Sprintf("%s/admin/wholesale", w.baseURL)
	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      w.staffEmail,
		Subject: fmt.Sprintf("New wholesale application: %s", companyName),
		HTML:    fmt.Sprintf(`<p><strong>%s</strong> (%s) has applied for a wholesale account.</p><p><a href="%s">Review Application</a></p>`, companyName, customer.Email, reviewURL),
		Text:    fmt.Sprintf("%s (%s) has applied for a wholesale account.\n\nReview: %s", companyName, customer.Email, reviewURL),
		Tag:     "wholesale-application",
	})
	if err != nil {
		return fmt.Errorf("send wholesale application notification: %w", err)
	}

	slog.Info("wholesale application notification sent",
		"customer_id", customer.ID,
		"email", customer.Email,
		"company", customer.CompanyName,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
