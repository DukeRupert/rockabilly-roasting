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

// SubscriptionConfirmEmailWorker sends a subscription confirmation email.
type SubscriptionConfirmEmailWorker struct {
	river.WorkerDefaults[SubscriptionConfirmEmailArgs]
	subscriptions *store.SubscriptionStore
	customers     *store.CustomerStore
	catalog       *store.CatalogStore
	pool          *pgxpool.Pool
	mailer        email.Sender
	renderer      *emailtemplates.Renderer
	fromAddr      string
	baseURL       string
	storeName     string
}

// NewSubscriptionConfirmEmailWorker creates a new SubscriptionConfirmEmailWorker.
func NewSubscriptionConfirmEmailWorker(
	subscriptions *store.SubscriptionStore,
	customers *store.CustomerStore,
	catalog *store.CatalogStore,
	pool *pgxpool.Pool,
	mailer email.Sender,
	renderer *emailtemplates.Renderer,
	fromAddr, baseURL, storeName string,
) *SubscriptionConfirmEmailWorker {
	return &SubscriptionConfirmEmailWorker{
		subscriptions: subscriptions,
		customers:     customers,
		catalog:       catalog,
		pool:          pool,
		mailer:        mailer,
		renderer:      renderer,
		fromAddr:      fromAddr,
		baseURL:       baseURL,
		storeName:     storeName,
	}
}

// Work processes a subscription confirmation email job.
func (w *SubscriptionConfirmEmailWorker) Work(ctx context.Context, job *river.Job[SubscriptionConfirmEmailArgs]) error {
	tx, err := w.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	sub, err := w.subscriptions.GetByID(ctx, tx, job.Args.SubscriptionID)
	if err != nil {
		return fmt.Errorf("get subscription %s: %w", job.Args.SubscriptionID, err)
	}

	customer, err := w.customers.GetByID(ctx, tx, job.Args.CustomerID)
	if err != nil {
		return fmt.Errorf("get customer %s: %w", job.Args.CustomerID, err)
	}

	plan, err := w.subscriptions.GetPlanByID(ctx, tx, sub.PlanID)
	if err != nil {
		return fmt.Errorf("get plan %s: %w", sub.PlanID, err)
	}

	productName := "Product"
	variant, err := w.catalog.GetVariantByID(ctx, tx, sub.VariantID)
	if err == nil {
		product, err := w.catalog.GetProductByID(ctx, tx, variant.ProductID)
		if err == nil {
			productName = product.Title
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	html, text, err := w.renderer.Render("subscription_confirm", emailtemplates.SubscriptionConfirmData{
		CustomerName: customer.FirstName,
		PlanName:     plan.Name,
		ProductName:  productName,
		Quantity:     sub.Quantity,
		Interval:     string(plan.Interval),
		UnitPrice:    0, // price is on the order, not the subscription
		StoreName:    w.storeName,
		StoreURL:     w.baseURL,
	})
	if err != nil {
		return fmt.Errorf("render subscription confirm template: %w", err)
	}

	result, err := w.mailer.Send(ctx, email.Message{
		From:    w.fromAddr,
		To:      customer.Email,
		Subject: "Your subscription is active",
		HTML:    html,
		Text:    text,
		Tag:     "subscription-confirm",
	})
	if err != nil {
		return fmt.Errorf("send subscription confirm email: %w", err)
	}

	slog.Info("subscription confirmation email sent",
		"subscription_id", sub.ID,
		"customer_email", customer.Email,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
