package jobs

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/riverqueue/river"
)

// Enqueuer is the concrete app.JobEnqueuer used to enqueue jobs from inside
// service transactions. Wraps a river client so the enqueue rides on the
// caller's tx.
type Enqueuer struct {
	client       *river.Client[pgx.Tx]
	quietHoursTZ *time.Location // populated via WithQuietHours; nil sends notifications immediately
	sendHour     int            // hour-of-day (merchant-local) before which renewal notices are held
}

// NewEnqueuer returns an Enqueuer that uses client for transactional inserts.
func NewEnqueuer(client *river.Client[pgx.Tx]) *Enqueuer {
	return &Enqueuer{client: client}
}

// RetryJob implements app.JobRetrier: it hands a job River discarded back to
// the queue, inside the caller's transaction so the re-queue commits with the
// audit record that explains it. River bumps max_attempts itself when the job
// has already burned through its allowance, so a retried job gets a real
// attempt rather than being discarded again on sight.
func (e *Enqueuer) RetryJob(ctx context.Context, tx pgx.Tx, jobID int64) error {
	if _, err := e.client.JobRetryTx(ctx, tx, jobID); err != nil {
		return fmt.Errorf("river retry job %d: %w", jobID, err)
	}
	return nil
}

// WithQuietHours holds subscription notification emails (renewal receipt,
// past-due, subscription-ended) that would otherwise fire from the pre-dawn
// renewal batch until sendHour:00 merchant-local, so customers aren't pinged at
// 2am. Renewals processed at or after sendHour (e.g. manual admin renewals
// during the day) still send immediately. Wiring-time only; nil tz disables it.
func (e *Enqueuer) WithQuietHours(tz *time.Location, sendHour int) *Enqueuer {
	e.quietHoursTZ = tz
	e.sendHour = sendHour
	return e
}

// notifyOpts returns InsertOpts that defer a customer notification past quiet
// hours: when the current merchant-local time is before sendHour, the job is
// scheduled for sendHour:00 today; otherwise it returns nil (send immediately).
// Only the pre-dawn renewal batch hits the deferral path — daytime renewals
// send right away.
func (e *Enqueuer) notifyOpts() *river.InsertOpts {
	if e.quietHoursTZ == nil {
		return nil
	}
	if send, deferred := deferToSendHour(time.Now(), e.quietHoursTZ, e.sendHour); deferred {
		return &river.InsertOpts{ScheduledAt: send}
	}
	return nil
}

// deferToSendHour reports whether a notification enqueued at now should be held
// until the morning send window. When now (in loc) is before sendHour it
// returns that day's sendHour:00 and true; otherwise the zero time and false
// (send immediately). Pure, so the policy is unit-tested without a clock.
func deferToSendHour(now time.Time, loc *time.Location, sendHour int) (time.Time, bool) {
	lt := now.In(loc)
	if lt.Hour() >= sendHour {
		return time.Time{}, false
	}
	return time.Date(lt.Year(), lt.Month(), lt.Day(), sendHour, 0, 0, 0, loc), true
}

// EnqueueRenewalReceipt enqueues a renewal-receipt email job in tx.
func (e *Enqueuer) EnqueueRenewalReceipt(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, SubscriptionRenewalReceiptArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, e.notifyOpts())
	return err
}

// EnqueuePastDueNotice enqueues one rung of the past-due email ladder in tx.
// stage selects the notice — see SubscriptionPastDueArgs.
func (e *Enqueuer) EnqueuePastDueNotice(ctx context.Context, tx pgx.Tx, subscriptionID, customerID uuid.UUID, stage int) error {
	_, err := e.client.InsertTx(ctx, tx, SubscriptionPastDueArgs{
		SubscriptionID: subscriptionID,
		CustomerID:     customerID,
		Stage:          stage,
	}, e.notifyOpts())
	return err
}

// EnqueueSubscriptionEnded enqueues the "subscription ended" notice sent when
// dunning retries are exhausted, in tx so it rides on the expiry's commit.
func (e *Enqueuer) EnqueueSubscriptionEnded(ctx context.Context, tx pgx.Tx, subscriptionID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, SubscriptionDunningEndedArgs{
		SubscriptionID: subscriptionID,
		CustomerID:     customerID,
	}, e.notifyOpts())
	return err
}

// EnqueueOrderConfirm enqueues an order-confirmation email job in tx.
func (e *Enqueuer) EnqueueOrderConfirm(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderConfirmEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}

// EnqueueOrderShipped enqueues an "order shipped" notification job in tx.
// Callers schedule it alongside the shipment write so the email rides on
// the same commit — if the tx rolls back, no email goes.
func (e *Enqueuer) EnqueueOrderShipped(ctx context.Context, tx pgx.Tx, orderID, customerID, shipmentID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderShippedEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
		ShipmentID: shipmentID,
	}, nil)
	return err
}

// EnqueueOrderReadyForPickup enqueues a "your order is ready" notification
// for a pickup order, in tx so it rides on the staff transition's commit.
func (e *Enqueuer) EnqueueOrderReadyForPickup(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderReadyForPickupEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}

// EnqueueOrderOutForDelivery enqueues an "out for local delivery today"
// notification for a local-delivery order, in tx so it rides on the staff
// transition's commit.
func (e *Enqueuer) EnqueueOrderOutForDelivery(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, OrderOutForDeliveryEmailArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}

// EnqueueInvoicePaid enqueues a payment-confirmation email for a paid QB/ACH
// wholesale invoice, in tx so it rides on the reconcile's commit.
func (e *Enqueuer) EnqueueInvoicePaid(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, EmailInvoicePaidArgs{
		OrderID:    orderID,
		CustomerID: customerID,
	}, nil)
	return err
}

// EnqueueInvoicePastDue enqueues a past-due reminder for an overdue wholesale
// invoice at the given reminder stage, carrying QB's authoritative due date
// for display. UniqueOpts keys on the full args so the same (order, stage)
// reminder can't double-send even if a reconcile is retried.
func (e *Enqueuer) EnqueueInvoicePastDue(ctx context.Context, tx pgx.Tx, orderID, customerID uuid.UUID, stage int, dueDate time.Time) error {
	_, err := e.client.InsertTx(ctx, tx, EmailInvoicePastDueArgs{
		OrderID:    orderID,
		CustomerID: customerID,
		Stage:      stage,
		DueDate:    dueDate,
	}, &river.InsertOpts{
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	})
	return err
}

// EnqueueAnnouncementDispatch schedules an announcement's fan-out for sendAt.
//
// Quiet hours deliberately do not apply: staff picked this time on purpose, and
// silently shifting a "we open at 8am tomorrow" notice would defeat the point
// of letting them schedule it at all.
func (e *Enqueuer) EnqueueAnnouncementDispatch(ctx context.Context, tx pgx.Tx, announcementID uuid.UUID, sendAt time.Time) error {
	var opts *river.InsertOpts
	if !sendAt.IsZero() {
		opts = &river.InsertOpts{ScheduledAt: sendAt}
	}
	_, err := e.client.InsertTx(ctx, tx, AnnouncementDispatchArgs{
		AnnouncementID: announcementID,
	}, opts)
	return err
}

// EnqueueAnnouncementSend enqueues one announcement email to one account.
func (e *Enqueuer) EnqueueAnnouncementSend(ctx context.Context, tx pgx.Tx, announcementID, customerID uuid.UUID) error {
	_, err := e.client.InsertTx(ctx, tx, AnnouncementSendArgs{
		AnnouncementID: announcementID,
		CustomerID:     customerID,
	}, nil)
	return err
}
