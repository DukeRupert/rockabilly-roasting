package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/emailtemplates"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/email"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
)

// The sweep opens its own transactions from the pool, so tx-scoped fixtures are
// invisible to it. These helpers commit and register their own teardown.

func commitCustomer(t *testing.T, ctx context.Context) uuid.UUID {
	t.Helper()
	id := uuid.New()
	addr := "sweep-" + id.String()[:8] + "@example.com"
	_, err := testPool.Exec(ctx,
		`INSERT INTO customers (id, email, first_name, last_name, company_name)
		 VALUES ($1, $2, 'Ada', 'Byron', 'Blue Heron Cafe')`, id, addr)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM customers WHERE id = $1`, id)
	})
	return id
}

// commitTicket inserts an open ticket whose last customer contact was quietFor
// ago. last_contact_at is set directly because that is the column the sweep
// reads, and going through the service would stamp it as now.
func commitTicket(t *testing.T, ctx context.Context, customerID uuid.UUID, number string, severity domain.ServiceSeverity, quietFor time.Duration) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool.Exec(ctx,
		`INSERT INTO service_tickets (id, number, customer_id, title, severity, status, last_contact_at)
		 VALUES ($1, $2, $3, 'Group head leaking', $4, 'new', $5)`,
		id, number, customerID, string(severity), time.Now().Add(-quietFor))
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM service_tickets WHERE id = $1`, id)
	})
	return id
}

// sweepService builds a ServiceTicketService wired the way main wires it, with
// the equipment module forced to the requested state.
func sweepService(t *testing.T, ctx context.Context, moduleOn bool) (*app.ServiceTicketService, *email.TestSender) {
	t.Helper()

	renderer, err := emailtemplates.New()
	require.NoError(t, err)
	sender := email.NewTestSender()

	moduleStore := store.NewModuleStore()
	moduleSvc := app.NewModuleService(moduleStore, audit.NewAuditWriter())

	// The registry row is global, so put it back the way it was found.
	var previous bool
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT enabled FROM modules WHERE key = $1`, string(domain.ModuleEquipmentService)).Scan(&previous))
	_, err = testPool.Exec(ctx,
		`UPDATE modules SET enabled = $2 WHERE key = $1`, string(domain.ModuleEquipmentService), moduleOn)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(),
			`UPDATE modules SET enabled = $2 WHERE key = $1`, string(domain.ModuleEquipmentService), previous)
	})

	tx, err := testPool.Begin(ctx)
	require.NoError(t, err)
	require.NoError(t, moduleSvc.Refresh(ctx, tx))
	require.NoError(t, tx.Commit(ctx))

	svc := app.NewServiceTicketService(store.NewServiceTicketStore(), store.NewEquipmentStore(), audit.NewAuditWriter()).
		WithSweep(app.EmailEnv{
			Mailer:     sender,
			Renderer:   renderer,
			FromAddr:   "shop@example.test",
			BaseURL:    "https://example.test",
			StoreName:  "Rockabilly Roasting",
			StaffEmail: "crew@example.test",
		}, store.NewCustomerStore(), moduleSvc, metrics.NewRegistry())

	return svc, sender
}

// The whole point of the step: a ticket nobody has touched in weeks reaches
// staff without anyone thinking to go looking for it.
func TestSweepStaleTickets_MailsTheQuietOnes(t *testing.T) {
	ctx := t.Context()
	svc, sender := sweepService(t, ctx, true)
	customer := commitCustomer(t, ctx)

	number := "SVC-SWEEP" + uuid.New().String()[:6]
	commitTicket(t, ctx, customer, number, domain.ServiceSeverityDown, 21*24*time.Hour)

	require.NoError(t, svc.SweepStaleTickets(ctx, testPool, time.Now()))

	require.Len(t, sender.Sent, 1)
	msg := sender.Sent[0]
	assert.Equal(t, "crew@example.test", msg.To)
	assert.Equal(t, "service-stale-digest", msg.Tag)
	assert.Contains(t, msg.Subject, "going quiet")
	assert.Contains(t, msg.HTML, number)
	assert.Contains(t, msg.HTML, "Blue Heron Cafe")
	assert.Contains(t, msg.Text, number)
	// Severity earns the flag, and the digest never speaks to the customer.
	assert.Contains(t, msg.HTML, "DOWN")
}

// A ticket contacted yesterday is not stale, and a digest that fires on a quiet
// day is a digest people learn to filter.
func TestSweepStaleTickets_SilentWhenNothingIsQuiet(t *testing.T) {
	ctx := t.Context()
	svc, sender := sweepService(t, ctx, true)
	customer := commitCustomer(t, ctx)

	commitTicket(t, ctx, customer, "SVC-FRESH"+uuid.New().String()[:6],
		domain.ServiceSeverityRoutine, 1*time.Hour)

	require.NoError(t, svc.SweepStaleTickets(ctx, testPool, time.Now()))

	assert.Empty(t, sender.Sent, "a recently-contacted ticket must not trigger a digest")
}

// The worker is registered on every instance, so the module gate is the only
// thing stopping a shop that does not service machines from getting this mail.
func TestSweepStaleTickets_ModuleOffSendsNothing(t *testing.T) {
	ctx := t.Context()
	svc, sender := sweepService(t, ctx, false)
	customer := commitCustomer(t, ctx)

	commitTicket(t, ctx, customer, "SVC-OFF"+uuid.New().String()[:6],
		domain.ServiceSeverityDown, 30*24*time.Hour)

	require.NoError(t, svc.SweepStaleTickets(ctx, testPool, time.Now()))

	assert.Empty(t, sender.Sent, "module is off; nothing should be mailed")
}

// Sending the digest records that it happened, so an ignored ticket can later
// be shown to have been reported rather than missed.
func TestSweepStaleTickets_AuditsTheSend(t *testing.T) {
	ctx := t.Context()
	svc, _ := sweepService(t, ctx, true)
	customer := commitCustomer(t, ctx)

	number := "SVC-AUD" + uuid.New().String()[:6]
	commitTicket(t, ctx, customer, number, domain.ServiceSeverityRoutine, 14*24*time.Hour)

	before := time.Now()
	require.NoError(t, svc.SweepStaleTickets(ctx, testPool, time.Now()))

	var n int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = $1 AND created_at >= $2`,
		audit.AuditServiceStaleSwept, before).Scan(&n))
	assert.Equal(t, 1, n)

	//nolint:errcheck // teardown
	t.Cleanup(func() {
		testPool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action = $1 AND created_at >= $2`,
			audit.AuditServiceStaleSwept, before)
	})
}
