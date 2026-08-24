package app_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/platform/audit"
	"github.com/dukerupert/hiri/internal/platform/metrics"
	"github.com/dukerupert/hiri/internal/store"
	"github.com/dukerupert/hiri/internal/testutil"
)

// fakeEnqueuer records the invoice emails reconcile fans out; the other
// JobEnqueuer methods are no-ops.
type fakeEnqueuer struct {
	paid    []uuid.UUID
	pastDue []pastDueCall
}

type pastDueCall struct {
	orderID uuid.UUID
	stage   int
	dueDate time.Time
}

func (f *fakeEnqueuer) EnqueueRenewalReceipt(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueuePastDueNotice(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, int) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueSubscriptionEnded(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderConfirm(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderShipped(context.Context, pgx.Tx, uuid.UUID, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderReadyForPickup(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueOrderOutForDelivery(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueInvoicePaid(_ context.Context, _ pgx.Tx, orderID, _ uuid.UUID) error {
	f.paid = append(f.paid, orderID)
	return nil
}
func (f *fakeEnqueuer) EnqueueInvoicePastDue(_ context.Context, _ pgx.Tx, orderID, _ uuid.UUID, stage int, dueDate time.Time) error {
	f.pastDue = append(f.pastDue, pastDueCall{orderID: orderID, stage: stage, dueDate: dueDate})
	return nil
}


// Announcements are not part of the QB reconcile path; these satisfy the
// interface so the fake keeps compiling as JobEnqueuer grows.
func (f *fakeEnqueuer) EnqueueAnnouncementDispatch(context.Context, pgx.Tx, uuid.UUID, time.Time) error {
	return nil
}
func (f *fakeEnqueuer) EnqueueAnnouncementSend(context.Context, pgx.Tx, uuid.UUID, uuid.UUID) error {
	return nil
}

func newReconcileService(enq app.JobEnqueuer) (*app.OrderService, *store.OrderStore) {
	orderStore := store.NewOrderStore(nil)
	svc := app.NewOrderService(orderStore, audit.NewAuditWriter(), metrics.NewRegistry()).WithEnqueuer(enq)
	return svc, orderStore
}

// makeQBOrder creates a wholesale order, stamps it with a QB invoice, a
// placed-at, and a reminder stage, then re-reads it through the QB path so the
// returned order carries OverdueReminderStage exactly as reconcile will see it.
func makeQBOrder(t *testing.T, tx pgx.Tx, st *store.OrderStore, paymentStatus domain.PaymentStatus, placedAt time.Time, stage int) (*domain.Order, string) {
	t.Helper()
	ctx := context.Background()
	cust := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, cust.ID)
	o := testutil.CreateOrder(t, tx, cust.ID, addr.ID, addr.ID,
		testutil.WithOrderStatus(domain.OrderStatusConfirmed),
		testutil.WithPaymentStatus(paymentStatus),
		testutil.WithOrderTotals(10000, 0, 0, 0, 10000),
	)
	qbInvoiceID := "QBINV-" + uuid.New().String()[:8]
	_, err := tx.Exec(ctx,
		`UPDATE orders SET qb_invoice_id=$2, qb_invoice_no=$3, placed_at=$4, overdue_reminder_stage=$5 WHERE id=$1`,
		o.ID, qbInvoiceID, "1001", placedAt, int16(stage))
	require.NoError(t, err)
	fresh, err := st.GetOrderByQBInvoiceIDForUpdate(ctx, tx, qbInvoiceID)
	require.NoError(t, err)
	return fresh, qbInvoiceID
}

func TestReconcileWholesalePayment_DecisionTable(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	past := now.Add(-24 * time.Hour)
	future := now.Add(24 * time.Hour)
	placed8dAgo := now.Add(-8 * 24 * time.Hour)
	recentlyPlaced := now.Add(-1 * 24 * time.Hour)

	tests := []struct {
		name        string
		status      domain.PaymentStatus
		placedAt    time.Time
		stage       int
		facts       app.QBInvoiceFacts
		want        app.ReconcileTransition
		wantPayment domain.PaymentStatus
		wantPaid    bool
		wantPastDue int // reminder stage expected, 0 = none
	}{
		{
			name:        "paid in full",
			status:      domain.PaymentStatusInvoiced,
			placedAt:    recentlyPlaced,
			facts:       app.QBInvoiceFacts{BalanceCents: 0, TotalCents: 10000, DueDate: future},
			want:        app.ReconcileCaptured,
			wantPayment: domain.PaymentStatusCaptured,
			wantPaid:    true,
		},
		{
			name:        "overpayment / credit balance",
			status:      domain.PaymentStatusPartiallyPaid,
			placedAt:    recentlyPlaced,
			facts:       app.QBInvoiceFacts{BalanceCents: -500, TotalCents: 10000, DueDate: future},
			want:        app.ReconcileCaptured,
			wantPayment: domain.PaymentStatusCaptured,
			wantPaid:    true,
		},
		{
			name:        "partial within terms",
			status:      domain.PaymentStatusInvoiced,
			placedAt:    recentlyPlaced,
			facts:       app.QBInvoiceFacts{BalanceCents: 4000, TotalCents: 10000, DueDate: future},
			want:        app.ReconcilePartiallyPaid,
			wantPayment: domain.PaymentStatusPartiallyPaid,
		},
		{
			name:        "overdue full balance sends first reminder",
			status:      domain.PaymentStatusInvoiced,
			placedAt:    placed8dAgo,
			facts:       app.QBInvoiceFacts{BalanceCents: 10000, TotalCents: 10000, DueDate: past},
			want:        app.ReconcileOverdue,
			wantPayment: domain.PaymentStatusOverdue,
			wantPastDue: 1,
		},
		{
			name:        "late partial flags overdue (precedence over partial)",
			status:      domain.PaymentStatusPartiallyPaid,
			placedAt:    placed8dAgo,
			facts:       app.QBInvoiceFacts{BalanceCents: 4000, TotalCents: 10000, DueDate: past},
			want:        app.ReconcileOverdue,
			wantPayment: domain.PaymentStatusOverdue,
			wantPastDue: 1,
		},
		{
			name:        "voided in QB (total zeroed) reverts",
			status:      domain.PaymentStatusInvoiced,
			placedAt:    recentlyPlaced,
			facts:       app.QBInvoiceFacts{BalanceCents: 0, TotalCents: 0, DueDate: future},
			want:        app.ReconcileReverted,
			wantPayment: domain.PaymentStatusPendingInvoice,
		},
		{
			name:        "deleted in QB (not found) reverts",
			status:      domain.PaymentStatusOverdue,
			placedAt:    placed8dAgo,
			facts:       app.QBInvoiceFacts{NotFound: true},
			want:        app.ReconcileReverted,
			wantPayment: domain.PaymentStatusPendingInvoice,
		},
		{
			name:        "fully unpaid within terms advances to invoiced",
			status:      domain.PaymentStatusPendingInvoice,
			placedAt:    recentlyPlaced,
			facts:       app.QBInvoiceFacts{BalanceCents: 10000, TotalCents: 10000, DueDate: future},
			want:        app.ReconcileInvoiced,
			wantPayment: domain.PaymentStatusInvoiced,
		},
		{
			name:        "already captured is a no-op",
			status:      domain.PaymentStatusCaptured,
			placedAt:    recentlyPlaced,
			facts:       app.QBInvoiceFacts{BalanceCents: 0, TotalCents: 10000, DueDate: future},
			want:        app.ReconcileNone,
			wantPayment: domain.PaymentStatusCaptured,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tx := testutil.NewTestTx(t, testPool)
			enq := &fakeEnqueuer{}
			svc, st := newReconcileService(enq)

			order, qbInvoiceID := makeQBOrder(t, tx, st, tc.status, tc.placedAt, tc.stage)

			got, err := svc.ReconcileWholesalePayment(ctx, tx, order, tc.facts, now)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got, "transition")

			reread, err := st.GetOrderByQBInvoiceIDForUpdate(ctx, tx, qbInvoiceID)
			require.NoError(t, err)
			assert.Equal(t, tc.wantPayment, reread.PaymentStatus, "persisted payment status")

			assert.Equal(t, tc.wantPaid, len(enq.paid) == 1, "paid email enqueued")
			if tc.wantPastDue > 0 {
				require.Len(t, enq.pastDue, 1)
				assert.Equal(t, tc.wantPastDue, enq.pastDue[0].stage)
			} else {
				assert.Empty(t, enq.pastDue, "no past-due email")
			}
		})
	}
}

func TestReconcileWholesalePayment_Idempotent(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	tx := testutil.NewTestTx(t, testPool)
	enq := &fakeEnqueuer{}
	svc, st := newReconcileService(enq)

	// Already overdue, first reminder (stage 1) already sent.
	order, qbInvoiceID := makeQBOrder(t, tx, st, domain.PaymentStatusOverdue, now.Add(-8*24*time.Hour), 1)
	facts := app.QBInvoiceFacts{BalanceCents: 10000, TotalCents: 10000, DueDate: now.Add(-24 * time.Hour)}

	got, err := svc.ReconcileWholesalePayment(ctx, tx, order, facts, now)
	require.NoError(t, err)
	assert.Equal(t, app.ReconcileNone, got)
	assert.Empty(t, enq.pastDue, "stage 1 already sent, no resend")

	reread, err := st.GetOrderByQBInvoiceIDForUpdate(ctx, tx, qbInvoiceID)
	require.NoError(t, err)
	assert.Equal(t, domain.PaymentStatusOverdue, reread.PaymentStatus)
}

func TestMarkWholesaleOrderPaid(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	actor := app.Actor{Type: domain.AuditActorTypeStaff, Name: "tester"}

	t.Run("invoiced order is captured and emails customer when opted in", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &fakeEnqueuer{}
		svc, st := newReconcileService(enq)
		order, _ := makeQBOrder(t, tx, st, domain.PaymentStatusInvoiced, now, 0)

		got, err := svc.MarkWholesaleOrderPaid(ctx, tx, order.ID, true, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentStatusCaptured, got.PaymentStatus)

		reread, err := st.GetOrderByIDForUpdate(ctx, tx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentStatusCaptured, reread.PaymentStatus, "persisted")
		assert.Equal(t, []uuid.UUID{order.ID}, enq.paid, "confirmation email enqueued")
	})

	t.Run("overdue order is captured silently when not opted in", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &fakeEnqueuer{}
		svc, st := newReconcileService(enq)
		order, _ := makeQBOrder(t, tx, st, domain.PaymentStatusOverdue, now, 0)

		_, err := svc.MarkWholesaleOrderPaid(ctx, tx, order.ID, false, actor)
		require.NoError(t, err)

		reread, err := st.GetOrderByIDForUpdate(ctx, tx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentStatusCaptured, reread.PaymentStatus, "persisted")
		assert.Empty(t, enq.paid, "no email when staff did not opt in")
	})

	t.Run("already-captured order is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &fakeEnqueuer{}
		svc, st := newReconcileService(enq)
		order, _ := makeQBOrder(t, tx, st, domain.PaymentStatusCaptured, now, 0)

		_, err := svc.MarkWholesaleOrderPaid(ctx, tx, order.ID, false, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotPayable)
		assert.Empty(t, enq.paid)
	})

	t.Run("retail order without a QB invoice is rejected", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &fakeEnqueuer{}
		svc, _ := newReconcileService(enq)
		cust := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, cust.ID)
		order := testutil.CreateOrder(t, tx, cust.ID, addr.ID, addr.ID,
			testutil.WithOrderStatus(domain.OrderStatusConfirmed),
			testutil.WithPaymentStatus(domain.PaymentStatusInvoiced),
		)

		_, err := svc.MarkWholesaleOrderPaid(ctx, tx, order.ID, false, actor)
		assert.ErrorIs(t, err, app.ErrOrderNotPayable)
	})

	t.Run("non-QB wholesale order in pending_invoice is captured and emails when opted in", func(t *testing.T) {
		tx := testutil.NewTestTx(t, testPool)
		enq := &fakeEnqueuer{}
		svc, st := newReconcileService(enq)
		cust := testutil.CreateCustomer(t, tx)
		addr := testutil.CreateAddress(t, tx, cust.ID)
		order := testutil.CreateOrder(t, tx, cust.ID, addr.ID, addr.ID,
			testutil.WithOrderChannel(domain.OrderChannelWholesale),
			testutil.WithOrderStatus(domain.OrderStatusConfirmed),
			testutil.WithPaymentStatus(domain.PaymentStatusPendingInvoice),
		)

		got, err := svc.MarkWholesaleOrderPaid(ctx, tx, order.ID, true, actor)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentStatusCaptured, got.PaymentStatus)

		reread, err := st.GetOrderByIDForUpdate(ctx, tx, order.ID)
		require.NoError(t, err)
		assert.Equal(t, domain.PaymentStatusCaptured, reread.PaymentStatus, "persisted")
		assert.Equal(t, []uuid.UUID{order.ID}, enq.paid, "confirmation email enqueued")
	})
}

func TestReconcileWholesalePayment_ReminderCadence(t *testing.T) {
	ctx := context.Background()

	// The cadence keys off QB's due date, not days-since-placed, so it holds
	// for any NET terms: the first reminder fires when the invoice is first
	// seen overdue, then weekly, capped at four total.
	for _, termsDays := range []int{7, 30} {
		t.Run(fmt.Sprintf("net-%d", termsDays), func(t *testing.T) {
			placedAt := time.Now().Add(-120 * 24 * time.Hour) // long ago; we drive "now" explicitly
			tx := testutil.NewTestTx(t, testPool)
			enq := &fakeEnqueuer{}
			svc, st := newReconcileService(enq)

			_, qbInvoiceID := makeQBOrder(t, tx, st, domain.PaymentStatusInvoiced, placedAt, 0)
			dueDate := placedAt.Add(time.Duration(termsDays) * 24 * time.Hour)
			facts := app.QBInvoiceFacts{BalanceCents: 10000, TotalCents: 10000, DueDate: dueDate}

			// Walk forward from the due date; each weekly crossing fires
			// exactly one reminder, a check in between fires nothing, and the
			// fifth week onward stays silent (cap).
			steps := []struct {
				daysPastDue int
				wantStage   int // 0 = no new reminder this step
			}{
				{1, 1},
				{3, 0},
				{8, 2},
				{15, 3},
				{24, 4},
				{38, 0},
			}
			wantTotal := 0
			for _, s := range steps {
				now := dueDate.Add(time.Duration(s.daysPastDue) * 24 * time.Hour)
				order, err := st.GetOrderByQBInvoiceIDForUpdate(ctx, tx, qbInvoiceID)
				require.NoError(t, err)
				_, err = svc.ReconcileWholesalePayment(ctx, tx, order, facts, now)
				require.NoError(t, err)
				if s.wantStage > 0 {
					wantTotal++
					require.Len(t, enq.pastDue, wantTotal)
					assert.Equal(t, s.wantStage, enq.pastDue[wantTotal-1].stage, "stage at %d days past due", s.daysPastDue)
					assert.Equal(t, dueDate, enq.pastDue[wantTotal-1].dueDate, "email carries QB's due date")
				} else {
					assert.Len(t, enq.pastDue, wantTotal, "no new reminder at %d days past due", s.daysPastDue)
				}
			}
			assert.Equal(t, 4, wantTotal, "exactly four reminders, then silence")
		})
	}
}

func TestInvoiceService_FenceQBManagedOrder(t *testing.T) {
	ctx := context.Background()
	tx := testutil.NewTestTx(t, testPool)

	invoiceSvc := app.NewInvoiceService(store.NewInvoiceStore(), store.NewOrderStore(nil), audit.NewAuditWriter(), metrics.NewRegistry())
	_, st := newReconcileService(&fakeEnqueuer{})

	order, _ := makeQBOrder(t, tx, st, domain.PaymentStatusPendingInvoice, time.Now(), 0)

	_, err := invoiceSvc.CreateFromOrder(ctx, tx, order.ID, nil, nil, testutil.TestActor())
	assert.ErrorIs(t, err, app.ErrOrderQBManaged, "manual invoice creation must be fenced on QB-owned orders")
}
