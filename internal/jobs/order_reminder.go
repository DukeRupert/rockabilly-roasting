package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// OrderReminderSchedulerArgs triggers the weekly scan for wholesale accounts
// that should be reminded to order before the cutoff.
type OrderReminderSchedulerArgs struct{}

// Kind returns the job kind identifier.
func (OrderReminderSchedulerArgs) Kind() string { return "order_reminder_scheduler" }

// OrderReminderArgs sends the weekly order reminder to one customer.
type OrderReminderArgs struct {
	CustomerID uuid.UUID `json:"customer_id"`
}

// Kind returns the job kind identifier.
func (OrderReminderArgs) Kind() string { return "order_reminder" }

// OrderReminderSchedulerWorker finds this week's reminder audience and enqueues
// one send job per account.
//
// Fanning out rather than sending inline is what the old rr service got wrong:
// it looped over customers in one pass, and a single Postmark failure was
// logged and dropped with no retry. Here each recipient is its own job, so one
// bad address retries (and eventually surfaces) without affecting the rest.
type OrderReminderSchedulerWorker struct {
	river.WorkerDefaults[OrderReminderSchedulerArgs]
	wholesale *app.WholesaleService
	pool      *pgxpool.Pool
	client    *river.Client[pgx.Tx]
	metrics   *metrics.Registry
}

// NewOrderReminderSchedulerWorker creates a new OrderReminderSchedulerWorker.
func NewOrderReminderSchedulerWorker(
	wholesale *app.WholesaleService,
	pool *pgxpool.Pool,
	client *river.Client[pgx.Tx],
	m *metrics.Registry,
) *OrderReminderSchedulerWorker {
	return &OrderReminderSchedulerWorker{wholesale: wholesale, pool: pool, client: client, metrics: m}
}

// Work scans for eligible accounts and enqueues a reminder for each.
func (w *OrderReminderSchedulerWorker) Work(ctx context.Context, job *river.Job[OrderReminderSchedulerArgs]) error {
	start := time.Now()
	logger := slog.Default()

	var count int
	err := store.Tx(ctx, w.pool, func(tx pgx.Tx) error {
		recipients, txErr := w.wholesale.ListOrderReminderRecipients(ctx, tx, start)
		if txErr != nil {
			return fmt.Errorf("list reminder recipients: %w", txErr)
		}

		for _, r := range recipients {
			// Unique by customer for the week: a scheduler retry, or two
			// server instances both firing the periodic job, must not mail
			// the same account twice.
			_, txErr = w.client.InsertTx(ctx, tx, OrderReminderArgs{CustomerID: r.CustomerID}, &river.InsertOpts{
				UniqueOpts: river.UniqueOpts{
					ByArgs:   true,
					ByPeriod: 7 * 24 * time.Hour,
				},
			})
			if txErr != nil {
				return fmt.Errorf("enqueue order reminder: %w", txErr)
			}
			metrics.TrackJobEnqueued(w.metrics, "order_reminder")
			count++
		}
		return nil
	})

	metrics.TrackJob(w.metrics, "order_reminder_scheduler", start, err)
	if err != nil {
		return err
	}

	logger.Info("enqueued wholesale order reminders", "count", count, "river_job_id", job.ID)
	return nil
}

// OrderReminderWorker delegates to WholesaleService.SendOrderReminder.
type OrderReminderWorker struct {
	river.WorkerDefaults[OrderReminderArgs]
	wholesale *app.WholesaleService
	pool      *pgxpool.Pool
}

// NewOrderReminderWorker creates a new OrderReminderWorker.
func NewOrderReminderWorker(wholesale *app.WholesaleService, pool *pgxpool.Pool) *OrderReminderWorker {
	return &OrderReminderWorker{wholesale: wholesale, pool: pool}
}

// Work sends one weekly order reminder.
func (w *OrderReminderWorker) Work(ctx context.Context, job *river.Job[OrderReminderArgs]) error {
	return w.wholesale.SendOrderReminder(ctx, w.pool, job.Args.CustomerID)
}
