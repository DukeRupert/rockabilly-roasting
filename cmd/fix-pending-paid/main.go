// fix-pending-paid sweeps orders left in the inconsistent state
// status=pending, payment_status=captured. The subscription checkout flow
// historically set payment_status=captured without advancing status from
// pending to confirmed, leaving paid orders stuck. Run once after deploying
// the subscribe.go fix to repair existing orders.
//
// Usage:
//
//	./fix-pending-paid              # apply fix
//	./fix-pending-paid --dry-run    # preview without writing
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/joho/godotenv"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	dryRun := flag.Bool("dry-run", false, "list affected orders without updating")
	flag.Parse()

	_ = godotenv.Load()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return fmt.Errorf("DATABASE_URL is required")
	}

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect to database: %w", err)
	}
	defer pool.Close()

	type stuck struct {
		ID     uuid.UUID
		Number string
	}
	var rows []stuck

	err = store.Tx(ctx, pool, func(tx pgx.Tx) error {
		q := `SELECT id, number FROM orders
		      WHERE status = $1 AND payment_status = $2
		      ORDER BY placed_at ASC`
		r, err := tx.Query(ctx, q, string(domain.OrderStatusPending), string(domain.PaymentStatusCaptured))
		if err != nil {
			return fmt.Errorf("query stuck orders: %w", err)
		}
		defer r.Close()
		for r.Next() {
			var s stuck
			if err := r.Scan(&s.ID, &s.Number); err != nil {
				return fmt.Errorf("scan stuck order: %w", err)
			}
			rows = append(rows, s)
		}
		return r.Err()
	})
	if err != nil {
		return err
	}

	if len(rows) == 0 {
		fmt.Println("No orders found in pending+captured state.")
		return nil
	}

	fmt.Printf("Found %d order(s) in pending+captured state:\n", len(rows))
	for _, s := range rows {
		fmt.Printf("  %s  %s\n", s.ID, s.Number)
	}

	if *dryRun {
		fmt.Println("\n--dry-run set; no changes written.")
		return nil
	}

	orderSvc := app.NewOrderService(store.NewOrderStore(nil), audit.NewAuditWriter(), nil)
	actor := app.Actor{
		Type: domain.AuditActorTypeSystem,
		Name: "fix_pending_paid",
	}

	fixed := 0
	for _, s := range rows {
		err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
			_, err := orderSvc.UpdateOrderStatus(ctx, tx, s.ID, domain.OrderStatusConfirmed, actor)
			return err
		})
		if err != nil {
			slog.Default().Error("fix-pending-paid: update failed", "order_id", s.ID, "number", s.Number, "error", err)
			continue
		}
		fmt.Printf("  - %s confirmed\n", s.Number)
		fixed++
	}

	fmt.Printf("\nFixed %d/%d order(s).\n", fixed, len(rows))
	if fixed < len(rows) {
		return fmt.Errorf("%d order(s) failed; see logs above", len(rows)-fixed)
	}
	return nil
}
