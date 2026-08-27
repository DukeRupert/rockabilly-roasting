package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
)

// SendTicketOpenedNotice opens its own transactions from the pool, so it cannot
// see tx-scoped fixtures. These commit and register their own teardown, the same
// arrangement the stale sweep's tests use.

func commitEquipment(t *testing.T, ctx context.Context, customerID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool.Exec(ctx,
		`INSERT INTO equipment (id, customer_id, category, make, model, serial_number)
		 VALUES ($1, $2, 'espresso_machine', 'La Marzocco', 'Linea PB', 'LM-99201')`,
		id, customerID)
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM equipment WHERE id = $1`, id)
	})
	return id
}

// commitReportedTicket inserts the ticket a customer's report would leave
// behind: a machine named, the fault in their own words, and nobody assigned.
func commitReportedTicket(t *testing.T, ctx context.Context, customerID, equipmentID uuid.UUID, number string, severity domain.ServiceSeverity) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := testPool.Exec(ctx,
		`INSERT INTO service_tickets (id, number, customer_id, equipment_id, title, description, severity, status)
		 VALUES ($1, $2, $3, $4, 'La Marzocco Linea PB — machine down',
		         'No pressure at all this morning.', $5, 'new')`,
		id, number, customerID, equipmentID, string(severity))
	require.NoError(t, err)
	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(), `DELETE FROM service_tickets WHERE id = $1`, id)
	})
	return id
}

// The point of the whole step: a cafe reports a dead machine from the portal and
// the crew are told, with enough on the face of the mail to act on it.
func TestSendTicketOpenedNotice_MailsStaff(t *testing.T) {
	ctx := t.Context()
	svc, sender := notifyingService(t, ctx, true)
	customer := commitCustomer(t, ctx)
	machine := commitEquipment(t, ctx, customer)

	number := "SVC-OPEN" + uuid.New().String()[:6]
	ticket := commitReportedTicket(t, ctx, customer, machine, number, domain.ServiceSeverityDown)

	require.NoError(t, svc.SendTicketOpenedNotice(ctx, testPool, ticket))

	require.Len(t, sender.Sent, 1)
	msg := sender.Sent[0]
	assert.Equal(t, "crew@example.test", msg.To)
	assert.Equal(t, "service-ticket-opened", msg.Tag)
	// A crew reading this on a phone must be able to triage from the subject
	// alone — that is the whole reason down bypasses quiet hours.
	assert.Contains(t, msg.Subject, "MACHINE DOWN")
	assert.Contains(t, msg.Subject, "Blue Heron Cafe")
	// The customer's own words, the machine, and who to ring, in both bodies.
	assert.Contains(t, msg.HTML, "No pressure at all this morning.")
	assert.Contains(t, msg.HTML, "La Marzocco Linea PB")
	assert.Contains(t, msg.HTML, number)
	assert.Contains(t, msg.Text, "No pressure at all this morning.")
	assert.Contains(t, msg.Text, number)
}

// A grinder sounding odd is not a machine down, and the subject must not shout
// about it — a staff inbox where everything is urgent has no urgent in it.
func TestSendTicketOpenedNotice_DegradedIsNotShouted(t *testing.T) {
	ctx := t.Context()
	svc, sender := notifyingService(t, ctx, true)
	customer := commitCustomer(t, ctx)
	machine := commitEquipment(t, ctx, customer)

	ticket := commitReportedTicket(t, ctx, customer, machine,
		"SVC-DEG"+uuid.New().String()[:6], domain.ServiceSeverityDegraded)

	require.NoError(t, svc.SendTicketOpenedNotice(ctx, testPool, ticket))

	require.Len(t, sender.Sent, 1)
	assert.NotContains(t, sender.Sent[0].Subject, "MACHINE DOWN")
	assert.Contains(t, sender.Sent[0].Subject, "Service report")
}

// The worker is registered on every instance, so the module gate is the only
// thing stopping a shop that does not service machines from getting this mail —
// including for a job enqueued just before somebody switched the module off.
func TestSendTicketOpenedNotice_ModuleOffSendsNothing(t *testing.T) {
	ctx := t.Context()
	svc, sender := notifyingService(t, ctx, false)
	customer := commitCustomer(t, ctx)
	machine := commitEquipment(t, ctx, customer)

	ticket := commitReportedTicket(t, ctx, customer, machine,
		"SVC-MOFF"+uuid.New().String()[:5], domain.ServiceSeverityDown)

	require.NoError(t, svc.SendTicketOpenedNotice(ctx, testPool, ticket))

	assert.Empty(t, sender.Sent, "module is off; nothing should be mailed")
}

// "We were never told" is the argument a shop loses when a machine stays broken.
// The audit row answers it with a time.
func TestSendTicketOpenedNotice_AuditsTheSend(t *testing.T) {
	ctx := t.Context()
	svc, _ := notifyingService(t, ctx, true)
	customer := commitCustomer(t, ctx)
	machine := commitEquipment(t, ctx, customer)

	ticket := commitReportedTicket(t, ctx, customer, machine,
		"SVC-AUDO"+uuid.New().String()[:5], domain.ServiceSeverityDown)

	before := time.Now()
	require.NoError(t, svc.SendTicketOpenedNotice(ctx, testPool, ticket))

	var n int
	require.NoError(t, testPool.QueryRow(ctx,
		`SELECT COUNT(*) FROM audit_log WHERE action = $1 AND resource_id = $2`,
		audit.AuditServiceTicketNotified, ticket).Scan(&n))
	assert.Equal(t, 1, n)

	t.Cleanup(func() {
		//nolint:errcheck // teardown
		testPool.Exec(context.Background(),
			`DELETE FROM audit_log WHERE action = $1 AND created_at >= $2`,
			audit.AuditServiceTicketNotified, before)
	})
}
