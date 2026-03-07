package jobs

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// WholesaleApprovedWorker sends a welcome email to an approved wholesale customer.
type WholesaleApprovedWorker struct {
	river.WorkerDefaults[WholesaleApprovedArgs]
	customers *store.CustomerStore
	pool      *pgxpool.Pool
	mailer    email.Sender
	renderer  *emailtemplates.Renderer
	fromAddr  string
	baseURL   string
	storeName string
}

// NewWholesaleApprovedWorker creates a new WholesaleApprovedWorker.
func NewWholesaleApprovedWorker(customers *store.CustomerStore, pool *pgxpool.Pool, mailer email.Sender, renderer *emailtemplates.Renderer, fromAddr, baseURL, storeName string) *WholesaleApprovedWorker {
	return &WholesaleApprovedWorker{
		customers: customers,
		pool:      pool,
		mailer:    mailer,
		renderer:  renderer,
		fromAddr:  fromAddr,
		baseURL:   baseURL,
		storeName: storeName,
	}
}

// Work processes a wholesale approval notification job.
func (w *WholesaleApprovedWorker) Work(ctx context.Context, job *river.Job[WholesaleApprovedArgs]) error {
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

	companyName := "there"
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	portalURL := fmt.Sprintf("%s/wholesale/login", w.baseURL)
	html, text, err := w.renderer.Render("wholesale_approved", emailtemplates.WholesaleApprovedData{
		CompanyName: companyName,
		PortalURL:   portalURL,
		StoreName:   w.storeName,
		StoreURL:    w.baseURL,
	})
	if err != nil {
		return fmt.Errorf("render wholesale approved template: %w", err)
	}

	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customer.Email,
		Subject: "Your wholesale account has been approved",
		HTML:    html,
		Text:    text,
		Tag:     "wholesale-approved",
	})
	if err != nil {
		return fmt.Errorf("send wholesale approved email: %w", err)
	}

	slog.Info("wholesale approved email sent",
		"customer_id", customer.ID,
		"email", customer.Email,
		"company", companyName,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
