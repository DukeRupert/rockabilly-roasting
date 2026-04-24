// Sentry smoke test — sends one message to the configured Sentry project
// so you can confirm events are arriving. Reads SENTRY_DSN (and optionally
// SENTRY_ENVIRONMENT / SENTRY_RELEASE) from .env, matching the server.
//
// Usage: go run ./cmd/sentrycheck
package main

import (
	"log"
	"os"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
)

func main() {
	_ = godotenv.Load()

	dsn := os.Getenv("SENTRY_DSN")
	if dsn == "" {
		log.Fatal("SENTRY_DSN is empty — set it in .env before running the smoke test")
	}

	err := sentry.Init(sentry.ClientOptions{
		Dsn:         dsn,
		Environment: os.Getenv("SENTRY_ENVIRONMENT"),
		Release:     os.Getenv("SENTRY_RELEASE"),
	})
	if err != nil {
		log.Fatalf("sentry.Init: %s", err)
	}
	defer sentry.Flush(2 * time.Second)

	sentry.CaptureMessage("sentrycheck: it works")
	log.Println("sentry: message captured, flushing...")
}
