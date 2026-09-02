// Command support-reply renders an email template and sends it via Postmark
// as a one-off support response. Intended for cases where staff need to reply
// from a saved template without going through the admin UI.
//
// Usage:
//
//	go run ./cmd/support-reply --to person@example.com --name Ivan
//	go run ./cmd/support-reply --to person@example.com --name Ivan --dry-run
//	go run ./cmd/support-reply --to person@example.com --from "RRC Support <support@rockabillyroasting.com>"
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/joho/godotenv"
)

const (
	defaultFrom      = "Rockabilly Roasting Co. <support@rockabillyroasting.com>"
	defaultBcc       = "info@rockabillyroasting.com"
	defaultStoreName = "Rockabilly Roasting Co."
	defaultStoreURL  = "https://rockabillyroasting.com"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		to       = flag.String("to", "", "recipient email (required)")
		name     = flag.String("name", "", "recipient first name (optional)")
		template = flag.String("template", "account_not_migrated", "template name (matches files in internal/emailtemplates)")
		subject  = flag.String("subject", "New website, same shop", "email subject line")
		from     = flag.String("from", defaultFrom, "From address (e.g. \"Name <addr@example.com>\")")
		bcc      = flag.String("bcc", defaultBcc, "BCC recipients (comma-separated; pass empty string to disable)")
		dryRun   = flag.Bool("dry-run", false, "render and print the email but do not send")
	)
	flag.Parse()

	_ = godotenv.Load()

	if *to == "" {
		return fmt.Errorf("--to is required")
	}

	r, err := emailtemplates.New(time.UTC) // no dated template; see New's doc
	if err != nil {
		return fmt.Errorf("load templates: %w", err)
	}

	storeName := getenv("STORE_NAME", defaultStoreName)
	storeURL := getenv("STORE_URL", defaultStoreURL)

	data, err := buildTemplateData(*template, *name, storeName, storeURL)
	if err != nil {
		return err
	}

	html, text, err := r.Render(*template, data)
	if err != nil {
		return fmt.Errorf("render %s: %w", *template, err)
	}

	if *dryRun {
		fmt.Println("=== DRY RUN ===")
		fmt.Printf("From:    %s\n", *from)
		fmt.Printf("To:      %s\n", *to)
		if *bcc != "" {
			fmt.Printf("Bcc:     %s\n", *bcc)
		}
		fmt.Printf("Subject: %s\n", *subject)
		fmt.Printf("Tag:     %s\n", tagFor(*template))
		fmt.Println()
		fmt.Println("--- TEXT BODY ---")
		fmt.Println(text)
		fmt.Println("--- HTML BODY ---")
		fmt.Println(html)
		return nil
	}

	token := os.Getenv("POSTMARK_SERVER_TOKEN")
	if token == "" {
		return fmt.Errorf("POSTMARK_SERVER_TOKEN is required (set in .env or environment)")
	}

	sender := email.NewPostmarkSender(token)
	res, err := sender.Send(context.Background(), email.Message{
		From:    *from,
		To:      *to,
		Bcc:     *bcc,
		Subject: *subject,
		HTML:    html,
		Text:    text,
		Tag:     tagFor(*template),
	})
	if err != nil {
		return fmt.Errorf("send: %w", err)
	}

	fmt.Printf("sent to %s (postmark id: %s)\n", *to, res.MessageID)
	return nil
}

func buildTemplateData(template, name, storeName, storeURL string) (any, error) {
	switch template {
	case "account_not_migrated":
		return emailtemplates.AccountNotMigratedData{
			CustomerName: name,
			StoreName:    storeName,
			StoreURL:     storeURL,
		}, nil
	default:
		return nil, fmt.Errorf("unknown template %q (only --template=account_not_migrated is wired up so far)", template)
	}
}

func tagFor(template string) string {
	return "support-" + strings.ReplaceAll(template, "_", "-")
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
