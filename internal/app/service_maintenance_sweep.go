package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// WithScheduling wires the sweep's collaborators: the module toggle it obeys,
// the ticket service it books work through, and the metrics it publishes.
//
// Separate from the constructor for the same reason ServiceTicketService does
// it — the plan service is usable for plain CRUD without any of this, and
// tests that only exercise a schedule should not have to build a mailer.
func (s *ServicePlanService) WithScheduling(tickets *ServiceTicketService, modules *ModuleService, m *metrics.Registry) *ServicePlanService {
	s.tickets = tickets
	s.modules = modules
	s.metrics = m
	return s
}

// bookingLimit caps how many tickets one sweep will open. A shop that has just
// switched the module on and back-dated fifty machines should get a day's work
// booked, not fifty tickets in one silent burst — the rest come tomorrow, and
// the overdue count on the dashboard says what is still waiting.
const bookingLimit = 25

// SweepMaintenance is the daily pass over preventive maintenance. It does three
// things, in this order:
//
//  1. Backfills. Every live (assignment, task) pair that has no pending
//     occurrence gets one. This is the path by which a task added to a plan
//     reaches the forty machines already on that plan — the write path
//     deliberately does not fan out, because one job that finds every gap is
//     easier to trust than several paths that each find some.
//
//  2. Books the covered work. A contract customer's maintenance inside its lead
//     window opens itself a routine ticket, so it lands in the queue somebody
//     is already working. Uncovered work is never booked: opening a ticket
//     commits the shop to a visit nobody agreed to pay for. Those rows go on
//     the call list instead, which is a human's job to work through.
//
//  3. Publishes the gauges, always — a series that goes blank on a quiet day is
//     indistinguishable from a broken exporter.
//
// Returns nil (not an error) when the module is off: that is not a fault, and
// an error would make River retry a job that can never succeed here.
//
// Idempotency: every write it makes is conditional. Backfill inserts through
// the pending unique index with ON CONFLICT DO NOTHING; booking is scoped to
// occurrences with no ticket yet and attaches the ticket in the same
// transaction, so a retry after a partial run neither doubles the schedule nor
// double-books a visit.
func (s *ServicePlanService) SweepMaintenance(ctx context.Context, pool *pgxpool.Pool, now time.Time) error {
	if s.modules == nil || !s.modules.Enabled(domain.ModuleEquipmentService) {
		return nil
	}

	filled, err := s.backfillMissing(ctx, pool)
	if err != nil {
		return err
	}

	booked, err := s.bookCoveredMaintenance(ctx, pool, now)
	if err != nil {
		return err
	}

	counts, err := s.sweepCounts(ctx, pool, now)
	if err != nil {
		return err
	}
	s.publishMaintenanceGauges(counts)

	// One audit row a day, whatever happened. A quiet due list and a job that
	// stopped running look identical from the outside otherwise.
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		return s.audit.Record(ctx, tx, audit.AuditEntry{
			ActorType:    domain.AuditActorTypeSystem,
			ActorName:    "service_maintenance_sweep",
			Action:       audit.AuditMaintenanceSwept,
			ResourceType: "equipment",
			Metadata: map[string]any{
				"backfilled": filled,
				"booked":     booked,
				"overdue":    counts[domain.MaintenanceOverdue],
				"due_soon":   counts[domain.MaintenanceDueSoon],
				"uncovered":  counts[maintenanceUncoveredKey],
				"warranty":   counts[maintenanceWarrantyKey],
			},
		})
	}); err != nil {
		// Best effort: the work is done and committed. Failing here would make
		// River re-run a sweep whose effects have already landed, to write a
		// row nobody reads on a normal day.
		slog.ErrorContext(ctx, "maintenance sweep: audit failed",
			"backfilled", filled, "booked", booked, "error", err.Error())
	}

	return nil
}

// backfillMissing writes a pending occurrence for every live (assignment, task)
// pair that has none, anchored on the last completion of that task or on the
// assignment's start date where there has not been one.
//
// A backfilled date landing in the past is correct and deliberate: a task added
// to a plan whose machines were anchored two years ago genuinely is overdue,
// and quietly moving it forward would hide exactly the finding the plan was
// written to surface.
func (s *ServicePlanService) backfillMissing(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	var filled int
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		missing, err := s.plans.ListMissingDue(ctx, tx)
		if err != nil {
			return err
		}
		for _, m := range missing {
			created, err := s.plans.CreateDue(ctx, tx, store.CreateMaintenanceDueParams{
				AssignmentID: m.AssignmentID,
				TaskID:       m.TaskID,
				EquipmentID:  m.EquipmentID,
				DueOn:        domain.FirstDueOn(m.Anchor, m.IntervalDays),
			})
			if err != nil {
				return err
			}
			if created != nil {
				filled++
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("backfill maintenance: %w", err)
	}
	return filled, nil
}

// bookCoveredMaintenance opens a routine ticket for each piece of covered
// maintenance that is due and not yet booked.
//
// Each booking is its own transaction. One machine whose ticket fails — a
// customer deleted mid-sweep, a constraint nobody predicted — must not roll
// back the twenty that succeeded, and the failure is worth logging rather than
// aborting the run for.
func (s *ServicePlanService) bookCoveredMaintenance(ctx context.Context, pool *pgxpool.Pool, now time.Time) (int, error) {
	if s.tickets == nil {
		return 0, nil
	}

	var due []domain.MaintenanceDueRow
	if err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		var err error
		due, err = s.plans.ListDue(ctx, tx, store.MaintenanceFilter{
			Scope: store.MaintenanceScopeBookable,
			Now:   now,
			Limit: bookingLimit,
		})
		return err
	}); err != nil {
		return 0, fmt.Errorf("list bookable maintenance: %w", err)
	}

	var booked int
	for _, row := range due {
		// The store scope and this check answer the same question from two
		// sides. The predicate is the authority on what is bookable; the query
		// is an index-shaped approximation of it, and keeping both means a
		// divergence shows up as work not done rather than a visit nobody
		// agreed to.
		if !row.BookableOn(now) {
			continue
		}
		if err := s.bookOne(ctx, pool, row); err != nil {
			slog.ErrorContext(ctx, "maintenance sweep: could not book visit",
				"due_id", row.ID.String(), "customer_id", row.CustomerID.String(),
				"task", row.TaskName, "error", err.Error())
			continue
		}
		booked++
		if s.metrics != nil {
			s.metrics.MaintenanceBooked.Inc()
		}
	}
	return booked, nil
}

// bookOne opens the ticket for one due item and attaches it, in one
// transaction. The pairing matters: a ticket opened without the attachment
// would be booked again tomorrow, and every day after.
func (s *ServicePlanService) bookOne(ctx context.Context, pool *pgxpool.Pool, row domain.MaintenanceDueRow) error {
	return store.Tx(ctx, pool, func(tx pgx.Tx) error {
		equipmentID := row.EquipmentID
		dueOn := row.DueOn

		ticket, err := s.tickets.Open(ctx, tx, OpenTicketParams{
			CustomerID:  row.CustomerID,
			EquipmentID: &equipmentID,
			AddressID:   row.AddressID,
			Title:       row.TaskName + " — " + row.MachineDescription(),
			Description: maintenanceTicketBody(row),
			// Routine by definition: this is planned work on a machine that is
			// still running, not a call-out. Severity is what the queue sorts
			// on, and scheduled maintenance must never outrank a cafe that is
			// down.
			Severity: domain.ServiceSeverityRoutine,
			// The due date is the target, not a booking. Somebody still has to
			// agree a time with the cafe; putting it here means the ticket
			// shows up in the right week rather than at the bottom of the list.
			ScheduledFor: &dueOn,
			// Covered work: the contract has been paid for, so the visit is not
			// billed again on top.
			Billable: false,
		}, systemActor())
		if err != nil {
			return err
		}

		return s.plans.AttachTicket(ctx, tx, row.ID, ticket.ID)
	})
}

// maintenanceTicketBody is what the tech reads on arrival: which machine, what
// the plan says to do, and why it matters. The task's instructions are copied
// rather than linked because the person holding the ticket is standing in a
// cafe, not browsing the admin.
func maintenanceTicketBody(row domain.MaintenanceDueRow) string {
	body := "Scheduled maintenance from the " + row.PlanName + " plan.\n\n" +
		"Task: " + row.TaskName + " (" + row.IntervalLabel() + ")\n" +
		"Due: " + row.DueOn.Format("Monday, 2 January 2006") + "\n" +
		"Machine: " + row.MachineDescription()
	if row.EquipmentSerial != "" {
		body += " · serial " + row.EquipmentSerial
	}
	if row.WarrantyRequired {
		body += "\n\nThis one is required to keep the manufacturer's warranty."
	}
	if row.TaskInstructions != "" {
		body += "\n\n" + row.TaskInstructions
	}
	return body
}

// systemActor is the actor a job acts as.
func systemActor() Actor {
	return Actor{
		Type: domain.AuditActorTypeSystem,
		Name: domain.SystemActor.Name,
	}
}

// The two count keys that are not urgencies. Both are slices of the pending set
// the dashboard names in its own words, so they ride in the same map rather
// than in a struct that would exist only to hold four ints.
const (
	maintenanceUncoveredKey domain.MaintenanceUrgency = "uncovered"
	maintenanceWarrantyKey  domain.MaintenanceUrgency = "warranty"
)

// sweepCounts is the state of the schedule on a given day, in one read.
func (s *ServicePlanService) sweepCounts(ctx context.Context, pool *pgxpool.Pool, now time.Time) (map[domain.MaintenanceUrgency]int, error) {
	counts := make(map[domain.MaintenanceUrgency]int, 4)
	err := store.Tx(ctx, pool, func(tx pgx.Tx) error {
		for key, scope := range map[domain.MaintenanceUrgency]store.MaintenanceScope{
			domain.MaintenanceOverdue: store.MaintenanceScopeOverdue,
			domain.MaintenanceDueSoon: store.MaintenanceScopeDueSoon,
			maintenanceUncoveredKey:   store.MaintenanceScopeUncovered,
			maintenanceWarrantyKey:    store.MaintenanceScopeWarranty,
		} {
			n, err := s.plans.CountDue(ctx, tx, store.MaintenanceFilter{Scope: scope, Now: now})
			if err != nil {
				return err
			}
			counts[key] = n
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("count maintenance: %w", err)
	}
	return counts, nil
}

// publishMaintenanceGauges writes every series every run, including the zeros,
// so a scope that empties out drops to zero instead of holding its last value.
func (s *ServicePlanService) publishMaintenanceGauges(counts map[domain.MaintenanceUrgency]int) {
	if s.metrics == nil {
		return
	}
	for _, key := range []domain.MaintenanceUrgency{
		domain.MaintenanceOverdue, domain.MaintenanceDueSoon,
		maintenanceUncoveredKey, maintenanceWarrantyKey,
	} {
		s.metrics.MaintenanceDue.WithLabelValues(string(key)).Set(float64(counts[key]))
	}
}
