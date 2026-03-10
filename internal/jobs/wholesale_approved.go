package jobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
)

// WholesaleApprovedWorker sends a welcome email to an approved wholesale customer.
// setupTokenDuration is how long a wholesale password setup link is valid.
const setupTokenDuration = 72 * time.Hour

type WholesaleApprovedWorker struct {
	river.WorkerDefaults[WholesaleApprovedArgs]
	customers  *store.CustomerStore
	magicLinks *store.MagicLinkStore
	pool       *pgxpool.Pool
	mailer     email.Sender
	renderer   *emailtemplates.Renderer
	fromAddr   string
	baseURL    string
	storeName  string
}

// NewWholesaleApprovedWorker creates a new WholesaleApprovedWorker.
func NewWholesaleApprovedWorker(customers *store.CustomerStore, magicLinks *store.MagicLinkStore, pool *pgxpool.Pool, mailer email.Sender, renderer *emailtemplates.Renderer, fromAddr, baseURL, storeName string) *WholesaleApprovedWorker {
	return &WholesaleApprovedWorker{
		customers:  customers,
		magicLinks: magicLinks,
		pool:       pool,
		mailer:     mailer,
		renderer:   renderer,
		fromAddr:   fromAddr,
		baseURL:    baseURL,
		storeName:  storeName,
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

	// Generate a password-setup token.
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return fmt.Errorf("generate setup token: %w", err)
	}
	rawToken := hex.EncodeToString(tokenBytes)
	tokenHash := sha256.Sum256([]byte(rawToken))
	tokenHashHex := hex.EncodeToString(tokenHash[:])

	expiresAt := time.Now().Add(setupTokenDuration)
	if _, err := w.magicLinks.Create(ctx, tx, customer.ID, tokenHashHex, expiresAt); err != nil {
		return fmt.Errorf("store setup token: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}

	companyName := "there"
	if customer.CompanyName != nil {
		companyName = *customer.CompanyName
	}

	setupURL := fmt.Sprintf("%s/wholesale/setup?token=%s", w.baseURL, rawToken)
	html, text, err := w.renderer.Render("wholesale_approved", emailtemplates.WholesaleApprovedData{
		CompanyName: companyName,
		SetupURL:    setupURL,
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
		"company", companyName,
		"message_id", result.MessageID,
		"river_job_id", job.ID,
	)

	return nil
}
