// Command os-welcome sends the wholesale migration welcome email — with a
// password-setup link — to a batch of migrated wholesale customers.
//
// SAFE BY DEFAULT: it dry-runs unless --send is passed. The dry run mints no
// tokens and sends nothing; it lists who would be emailed and renders one
// sample. With --send it mints a 72h setup token per customer and emails them.
//
// Run os-migrate first (which creates the accounts on the Wholesale 2026 price
// list + NET 7), verify the import, then run this to invite the batch.
//
// Usage:
//
//	go run ./cmd/os-welcome --emails a@x.com,b@y.com            # dry run
//	go run ./cmd/os-welcome --emails a@x.com,b@y.com --send     # actually send
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"
)

const (
	defaultFrom      = "Rockabilly Roasting Co. <support@rockabillyroasting.com>"
	defaultStoreName = "Rockabilly Roasting Co."
	defaultStoreURL  = "https://rockabillyroasting.com"
	migratedSubject  = "Your Rockabilly wholesale account has moved"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		emailsArg = flag.String("emails", "", "comma-separated customer emails to welcome (required)")
		send      = flag.Bool("send", false, "actually send; without this the command dry-runs (no tokens, no email)")
		from      = flag.String("from", "", "From address (default: $EMAIL_FROM or built-in)")
		bcc       = flag.String("bcc", "", "BCC recipients (comma-separated; optional)")
	)
	flag.Parse()
	_ = godotenv.Load()

	emails := splitCSVLower(*emailsArg)
	if len(emails) == 0 {
		return fmt.Errorf("--emails is required (comma-separated)")
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	fromAddr := firstNonEmpty(*from, os.Getenv("EMAIL_FROM"), defaultFrom)
	storeName := firstNonEmpty(os.Getenv("STORE_NAME"), defaultStoreName)
	baseURL := firstNonEmpty(os.Getenv("BASE_URL"), defaultStoreURL)

	renderer, err := emailtemplates.New()
	if err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect db: %w", err)
	}
	defer pool.Close()

	customerStore := store.NewCustomerStore()
	magicLinks := store.NewMagicLinkStore()
	auditWriter := audit.NewAuditWriter()

	// Resolve each email to an approved wholesale customer. Anything not found or
	// not approved-wholesale is reported and skipped — never emailed.
	type target struct {
		customer *domain.Customer
		company  string
	}
	var targets []target
	for _, em := range emails {
		var c *domain.Customer
		err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
			cc, err := customerStore.GetByEmail(ctx, tx, em)
			if err != nil {
				return err
			}
			c = cc
			return nil
		})
		if err != nil {
			fmt.Printf("SKIP  %-40s not found (%v)\n", em, err)
			continue
		}
		if !c.IsApprovedWholesale() {
			status := ""
			if c.WholesaleStatus != nil {
				status = string(*c.WholesaleStatus)
			}
			fmt.Printf("SKIP  %-40s not an approved wholesale account (type=%s status=%q)\n",
				em, c.AccountType, status)
			continue
		}
		company := "there"
		if c.CompanyName != nil && *c.CompanyName != "" {
			company = *c.CompanyName
		}
		targets = append(targets, target{customer: c, company: company})
	}

	fmt.Printf("\n%d of %d emails resolved to approved wholesale accounts.\n", len(targets), len(emails))
	if len(targets) == 0 {
		return nil
	}

	if !*send {
		fmt.Println("\n=== DRY RUN (no tokens minted, nothing sent) — pass --send to send ===")
		for _, t := range targets {
			fmt.Printf("  WOULD EMAIL  %-40s %s\n", t.customer.Email, t.company)
		}
		_, text, err := renderer.Render("wholesale_migrated", emailtemplates.WholesaleMigratedData{
			CompanyName: targets[0].company,
			SetupURL:    baseURL + "/wholesale/setup?token=SAMPLE_TOKEN",
			StoreName:   storeName,
			StoreURL:    baseURL,
		})
		if err != nil {
			return fmt.Errorf("render sample: %w", err)
		}
		fmt.Printf("\nFrom:    %s\nSubject: %s\n\n--- TEXT (sample for %s) ---\n%s\n",
			fromAddr, migratedSubject, targets[0].company, text)
		return nil
	}

	token := os.Getenv("POSTMARK_SERVER_TOKEN")
	if token == "" {
		return fmt.Errorf("POSTMARK_SERVER_TOKEN is required to --send")
	}
	sender := email.NewPostmarkSender(token)

	var sent, failed int
	for _, t := range targets {
		if err := sendWelcome(ctx, pool, sender, renderer, magicLinks, auditWriter,
			t.customer, t.company, fromAddr, *bcc, baseURL, storeName); err != nil {
			fmt.Printf("FAIL  %-40s %v\n", t.customer.Email, err)
			failed++
			continue
		}
		fmt.Printf("SENT  %-40s %s\n", t.customer.Email, t.company)
		sent++
	}
	fmt.Printf("\nDone: %d sent, %d failed, %d skipped.\n", sent, failed, len(emails)-len(targets))
	if failed > 0 {
		return fmt.Errorf("%d sends failed", failed)
	}
	return nil
}

// sendWelcome mints a 72h password-setup token (tx 1), sends the migration
// welcome email (outside any tx), then records an audit entry (tx 2) — mirroring
// WholesaleService.SendApprovalEmail's two-phase pattern.
func sendWelcome(
	ctx context.Context, pool *pgxpool.Pool, sender email.Sender, renderer *emailtemplates.Renderer,
	magicLinks *store.MagicLinkStore, auditWriter *audit.AuditWriter,
	c *domain.Customer, company, fromAddr, bcc, baseURL, storeName string,
) error {
	var rawToken string
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		t, err := mintSetupToken(ctx, tx, magicLinks, c.ID)
		if err != nil {
			return err
		}
		rawToken = t
		return nil
	}); err != nil {
		return fmt.Errorf("mint token: %w", err)
	}

	html, text, err := renderer.Render("wholesale_migrated", emailtemplates.WholesaleMigratedData{
		CompanyName: company,
		SetupURL:    fmt.Sprintf("%s/wholesale/setup?token=%s", baseURL, rawToken),
		StoreName:   storeName,
		StoreURL:    baseURL,
	})
	if err != nil {
		return fmt.Errorf("render: %w", err)
	}

	if _, err := sender.Send(ctx, email.Message{
		From:    fromAddr,
		To:      c.Email,
		Bcc:     bcc,
		Subject: migratedSubject,
		HTML:    html,
		Text:    text,
		Tag:     "wholesale-migrated",
	}); err != nil {
		return fmt.Errorf("send: %w", err)
	}

	return store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return auditWriter.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "os_welcome",
			Action:       audit.AuditEmailWholesaleMigrated,
			ResourceType: "customer",
			ResourceID:   c.ID,
			Metadata:     map[string]any{"company": company},
		})
	})
}

// mintSetupToken creates a single-use 72h password-setup magic link and returns
// the raw token. Mirrors AuthService.CreateSetupToken.
func mintSetupToken(ctx context.Context, tx pgx.Tx, magicLinks *store.MagicLinkStore, customerID uuid.UUID) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	raw := hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(raw))
	if _, err := magicLinks.Create(ctx, tx, customerID, hex.EncodeToString(sum[:]),
		store.MagicLinkPurposeDefault, time.Now().Add(app.SetupTokenDuration)); err != nil {
		return "", err
	}
	return raw, nil
}

func splitCSVLower(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
