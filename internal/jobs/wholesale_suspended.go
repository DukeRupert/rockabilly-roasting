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

// WholesaleSuspendedWorker sends a suspension notification email to a wholesale customer.
type WholesaleSuspendedWorker struct {
	river.WorkerDefaults[WholesaleSuspendedArgs]
	customers *store.CustomerStore
	pool      *pgxpool.Pool
	mailer    email.Sender
	renderer  *emailtemplates.Renderer
	fromAddr  string
	baseURL   string
	storeName string
}

// NewWholesaleSuspendedWorker creates a new WholesaleSuspendedWorker.
func NewWholesaleSuspendedWorker(customers *store.CustomerStore, pool *pgxpool.Pool, mailer email.Sender, renderer *emailtemplates.Renderer, fromAddr, baseURL, storeName string) *WholesaleSuspendedWorker {
	return &WholesaleSuspendedWorker{
		customers: customers,
		pool:      pool,
		mailer:    mailer,
		renderer:  renderer,
		fromAddr:  fromAddr,
		baseURL:   baseURL,
		storeName: storeName,
	}
}

// Work processes a wholesale suspension notification job.
func (w *WholesaleSuspendedWorker) Work(ctx context.Context, job *river.Job[WholesaleSuspendedArgs]) error {
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

	html, text, err := w.renderer.Render("wholesale_suspended", emailtemplates.WholesaleSuspendedData{
		CompanyName: companyName,
		StoreName:   w.storeName,
		StoreURL:    w.baseURL,
	})
	if err != nil {
		return fmt.Errorf("render wholesale suspended template: %w", err)
	}

	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customer.Email,
		Subject: "Your wholesale account has been suspended",
		HTML:    html,
		Text:    text,
		Tag:     "wholesale-suspended",
	})
	if err != nil {
		return fmt.Errorf("send wholesale suspended email: %w", err)
	}

	slog.Info("wholesale suspended email sent",
		"customer_id", customer.ID,
		"email", customer.Email,
		"company", companyName,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
