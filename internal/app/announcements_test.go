package app_test

import (
	"context"
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

// announcementEnqueuer records the dispatch jobs a schedule would queue, so
// tests can assert on the send time without a running river client.
type announcementEnqueuer struct {
	*fakeEnqueuer
	dispatched []time.Time
}

func (e *announcementEnqueuer) EnqueueAnnouncementDispatch(_ context.Context, _ pgx.Tx, _ uuid.UUID, sendAt time.Time) error {
	e.dispatched = append(e.dispatched, sendAt)
	return nil
}

func newAnnouncementService() (*app.AnnouncementService, *announcementEnqueuer) {
	enq := &announcementEnqueuer{fakeEnqueuer: &fakeEnqueuer{}}
	svc := app.NewAnnouncementService(
		store.NewAnnouncementStore(),
		store.NewCustomerStore(),
		store.NewCustomerUserStore(),
		audit.NewAuditWriter(),
		metrics.NewRegistry(),
	).WithJobEnqueuer(enq)
	return svc, enq
}

func staffTestActor() app.Actor {
	return app.Actor{Type: domain.AuditActorTypeStaff, Name: "Test Staff"}
}

// retailBuyer creates a retail customer with one completed order.
func retailBuyer(t *testing.T, tx pgx.Tx, opts ...testutil.OrderOption) uuid.UUID {
	t.Helper()
	c := testutil.CreateCustomer(t, tx)
	addr := testutil.CreateAddress(t, tx, c.ID)
	testutil.CreateOrder(t, tx, c.ID, addr.ID, addr.ID, opts...)
	return c.ID
}

func containsRecipient(recipients []domain.AnnouncementRecipient, id uuid.UUID) bool {
	for _, r := range recipients {
		if r.CustomerID == id {
			return true
		}
	}
	return false
}

func TestPreviewRecipients_RetailRequiresAPurchase(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	buyer := retailBuyer(t, tx)
	// Registered but never ordered. An operational notice means nothing to
	// them, and mailing addresses that have never transacted spends the
	// sending domain's reputation for no return.
	browser := testutil.CreateCustomer(t, tx).ID

	recipients, err := svc.PreviewRecipients(ctx, tx, domain.AnnouncementAudienceRetail)
	require.NoError(t, err)
	assert.True(t, containsRecipient(recipients, buyer), "retail customer who ordered should be included")
	assert.False(t, containsRecipient(recipients, browser), "signup who never ordered should be excluded")
}

func TestPreviewRecipients_RetailExcludesCancelledOnlyBuyers(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	// A cancelled order is not a purchase. Counting it would put people who
	// never actually bought anything on the list.
	cancelled := retailBuyer(t, tx, testutil.WithOrderStatus(domain.OrderStatusCancelled))

	recipients, err := svc.PreviewRecipients(ctx, tx, domain.AnnouncementAudienceRetail)
	require.NoError(t, err)
	assert.False(t, containsRecipient(recipients, cancelled))
}

func TestPreviewRecipients_WholesaleRequiresApproval(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	approved := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, approved.ID)

	// A pending applicant is not yet a customer; a suspended one has been
	// deliberately cut off. Neither belongs on an account-wide mailing.
	pending := testutil.CreateCustomer(t, tx)
	_, err := tx.Exec(ctx,
		`UPDATE customers SET account_type = 'wholesale', wholesale_status = 'pending' WHERE id = $1`,
		pending.ID)
	require.NoError(t, err)

	recipients, err := svc.PreviewRecipients(ctx, tx, domain.AnnouncementAudienceWholesale)
	require.NoError(t, err)
	assert.True(t, containsRecipient(recipients, approved.ID))
	assert.False(t, containsRecipient(recipients, pending.ID))
}

func TestPreviewRecipients_WholesaleDoesNotRequireAnOrder(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	// Unlike retail, an approved wholesale account is a customer whether or not
	// it has ordered yet — it was approved by hand, which is the purchase
	// history's job on the retail side.
	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)

	recipients, err := svc.PreviewRecipients(ctx, tx, domain.AnnouncementAudienceWholesale)
	require.NoError(t, err)
	assert.True(t, containsRecipient(recipients, c.ID))
}

func TestPreviewRecipients_HonoursOptOut(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	optedOut := retailBuyer(t, tx)
	require.NoError(t, svc.SetAnnouncementsEnabled(ctx, tx, staffTestActor(), optedOut, false))

	recipients, err := svc.PreviewRecipients(ctx, tx, domain.AnnouncementAudienceAll)
	require.NoError(t, err)
	assert.False(t, containsRecipient(recipients, optedOut))
}

func TestPreviewRecipients_OptOutIsSeparateFromOrderReminders(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	wholesale := newWholesaleService()
	ctx := context.Background()

	c := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, c.ID)

	// Muting the weekly nudge must not silence operational notices — a buyer
	// who does not want reminders still needs to hear the holiday moved their
	// delivery. This is the whole reason the two flags are separate.
	require.NoError(t, wholesale.SetOrderRemindersEnabled(ctx, tx, staffTestActor(), c.ID, false))

	recipients, err := svc.PreviewRecipients(ctx, tx, domain.AnnouncementAudienceWholesale)
	require.NoError(t, err)
	assert.True(t, containsRecipient(recipients, c.ID))
}

func TestPreviewRecipients_AllIsTheUnion(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	retail := retailBuyer(t, tx)
	ws := testutil.CreateCustomer(t, tx)
	approveWholesaleCustomer(t, tx, ws.ID)

	all, err := svc.PreviewRecipients(ctx, tx, domain.AnnouncementAudienceAll)
	require.NoError(t, err)
	assert.True(t, containsRecipient(all, retail))
	assert.True(t, containsRecipient(all, ws.ID))
}

func TestPreviewRecipients_RejectsUnknownAudience(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()

	_, err := svc.PreviewRecipients(context.Background(), tx, domain.AnnouncementAudience("everyone"))
	// Failing loudly rather than defaulting to "everyone" is the point: a typo
	// in an audience must never widen a mailing.
	require.ErrorIs(t, err, app.ErrInvalidAudience)
}

func TestScheduleAnnouncement_RejectsEmptyAndBadAudience(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	_, err := svc.ScheduleAnnouncement(ctx, tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "  ",
		Body:     "something",
		Audience: domain.AnnouncementAudienceAll,
	})
	require.ErrorIs(t, err, app.ErrEmptyAnnouncement)

	_, err = svc.ScheduleAnnouncement(ctx, tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Closed Monday",
		Body:     "  \n ",
		Audience: domain.AnnouncementAudienceAll,
	})
	require.ErrorIs(t, err, app.ErrEmptyAnnouncement)

	_, err = svc.ScheduleAnnouncement(ctx, tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Closed Monday",
		Body:     "We're shut.",
		Audience: domain.AnnouncementAudience(""),
	})
	require.ErrorIs(t, err, app.ErrInvalidAudience)
}

func TestScheduleAnnouncement_RejectsPastSendTime(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()

	_, err := svc.ScheduleAnnouncement(context.Background(), tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Closed Monday",
		Body:     "We're shut.",
		Audience: domain.AnnouncementAudienceAll,
		// River would fire this instantly, which is a surprising way to learn
		// you typed the wrong date into a mailing to every customer.
		SendAt: time.Now().Add(-2 * time.Hour),
	})
	require.ErrorIs(t, err, app.ErrScheduleInPast)
}

func TestScheduleAnnouncement_QueuesDispatchAtTheChosenTime(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, enq := newAnnouncementService()

	sendAt := time.Now().Add(48 * time.Hour)
	a, err := svc.ScheduleAnnouncement(context.Background(), tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Labor Day",
		Body:     "Monday's run moves to Tuesday.",
		Audience: domain.AnnouncementAudienceAll,
		SendAt:   sendAt,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.AnnouncementStatusScheduled, a.Status)
	require.Len(t, enq.dispatched, 1)
	assert.WithinDuration(t, sendAt, enq.dispatched[0], time.Second)
}

func TestScheduleAnnouncement_ZeroSendTimeMeansNow(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, enq := newAnnouncementService()

	a, err := svc.ScheduleAnnouncement(context.Background(), tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Closed today",
		Body:     "Burst pipe. Back tomorrow.",
		Audience: domain.AnnouncementAudienceRetail,
	})
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now(), a.ScheduledAt, time.Minute)
	require.Len(t, enq.dispatched, 1)
}

func TestCancelAnnouncement_OnlyWhileScheduled(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	ctx := context.Background()

	a, err := svc.ScheduleAnnouncement(ctx, tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Labor Day",
		Body:     "Monday's run moves to Tuesday.",
		Audience: domain.AnnouncementAudienceAll,
		SendAt:   time.Now().Add(24 * time.Hour),
	})
	require.NoError(t, err)

	require.NoError(t, svc.CancelAnnouncement(ctx, tx, staffTestActor(), a.ID))

	cancelled, err := svc.GetAnnouncement(ctx, tx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AnnouncementStatusCancelled, cancelled.Status)

	// Cancelling twice is not a way to un-send anything.
	err = svc.CancelAnnouncement(ctx, tx, staffTestActor(), a.ID)
	require.ErrorIs(t, err, app.ErrAnnouncementNotCancellable)
}

func TestCancelAnnouncement_UnknownIDIsNotFound(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()

	err := svc.CancelAnnouncement(context.Background(), tx, staffTestActor(), uuid.New())
	require.ErrorIs(t, err, app.ErrAnnouncementNotFound)
}

func TestClaimForDispatch_IsIdempotent(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	announcements := store.NewAnnouncementStore()
	ctx := context.Background()

	a, err := svc.ScheduleAnnouncement(ctx, tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Labor Day",
		Body:     "Monday's run moves to Tuesday.",
		Audience: domain.AnnouncementAudienceAll,
	})
	require.NoError(t, err)

	claimed, err := announcements.ClaimForDispatch(ctx, tx, a.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.AnnouncementStatusSending, claimed.Status)

	// River jobs may run more than once. The second claim finding nothing is
	// what stops a retry mailing everybody twice.
	_, err = announcements.ClaimForDispatch(ctx, tx, a.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestClaimForDispatch_SkipsACancelledAnnouncement(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	announcements := store.NewAnnouncementStore()
	ctx := context.Background()

	a, err := svc.ScheduleAnnouncement(ctx, tx, staffTestActor(), app.ScheduleAnnouncementParams{
		Subject:  "Labor Day",
		Body:     "Monday's run moves to Tuesday.",
		Audience: domain.AnnouncementAudienceAll,
		SendAt:   time.Now().Add(24 * time.Hour),
	})
	require.NoError(t, err)
	require.NoError(t, svc.CancelAnnouncement(ctx, tx, staffTestActor(), a.ID))

	// The cancel path leaves the river job alone and relies on this: the whole
	// cancel is one UPDATE, with no window where the queue and the admin
	// disagree about whether mail is going out.
	_, err = announcements.ClaimForDispatch(ctx, tx, a.ID)
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

func TestIsAnnouncementRecipient_ReflectsLateOptOut(t *testing.T) {
	tx := testutil.NewTestTx(t, testPool)
	svc, _ := newAnnouncementService()
	announcements := store.NewAnnouncementStore()
	ctx := context.Background()

	id := retailBuyer(t, tx)

	ok, err := announcements.IsAnnouncementRecipient(ctx, tx, id, domain.AnnouncementAudienceRetail)
	require.NoError(t, err)
	assert.True(t, ok)

	// The send-time re-check is what makes an opt-out between the fan-out and
	// the send actually stick.
	require.NoError(t, svc.SetAnnouncementsEnabled(ctx, tx, staffTestActor(), id, false))
	ok, err = announcements.IsAnnouncementRecipient(ctx, tx, id, domain.AnnouncementAudienceRetail)
	require.NoError(t, err)
	assert.False(t, ok)
}
