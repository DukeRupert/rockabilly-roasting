package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/jackc/pgx/v5"

	"github.com/dukerupert/hiri/internal/app"
	"github.com/dukerupert/hiri/internal/domain"
	"github.com/dukerupert/hiri/internal/jobs"
	"github.com/dukerupert/hiri/internal/platform/logging"
	"github.com/dukerupert/hiri/internal/platform/payments"
	"github.com/dukerupert/hiri/internal/store"
)

const maxWebhookBodyBytes = 65536

// handleStripeWebhook receives Stripe webhook events, verifies the signature,
// deduplicates, and processes them synchronously.
func (d *Deps) handleStripeWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.FromContext(ctx)

	payload, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBodyBytes))
	if err != nil {
		logger.Error("webhook: read body", "error", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	signature := r.Header.Get("Stripe-Signature")
	if signature == "" {
		http.Error(w, "missing signature", http.StatusBadRequest)
		return
	}

	event, err := d.PaymentProvider.ConstructWebhookEvent(payload, signature)
	if err != nil {
		logger.Warn("webhook: invalid signature", "error", err)
		http.Error(w, "invalid signature", http.StatusBadRequest)
		return
	}

	logger.Info("webhook received", "event_id", event.ID, "type", event.Type)
	d.Metrics.StripeWebhooksReceived.WithLabelValues(event.Type).Inc()

	// Dedup + persist event in a transaction
	var webhookEvent *domain.WebhookEvent
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		webhookEvent, txErr = d.WebhookService.PersistEvent(ctx, tx, "stripe", event.ID, event.Type, json.RawMessage(event.Data))
		return txErr
	})
	if err != nil {
		logger.Error("webhook: persist event", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	// nil means duplicate — already processed
	if webhookEvent == nil {
		logger.Info("webhook: duplicate event, skipping", "event_id", event.ID)
		d.Metrics.StripeWebhooksProcessed.WithLabelValues(event.Type, "duplicate").Inc()
		w.WriteHeader(http.StatusOK)
		return
	}

	// Process the event
	if err := d.processWebhookEvent(r, webhookEvent, event); err != nil {
		logger.Error("webhook: processing failed", "event_id", event.ID, "type", event.Type, "error", err)
		d.Metrics.StripeWebhooksProcessed.WithLabelValues(event.Type, "failed").Inc()
		// Mark as failed but still return 200 to Stripe (prevent retries for known failures)
		reason := err.Error()
		_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			return d.WebhookService.MarkFailed(ctx, tx, webhookEvent.ID, reason)
		})
		w.WriteHeader(http.StatusOK)
		return
	}

	d.Metrics.StripeWebhooksProcessed.WithLabelValues(event.Type, "success").Inc()

	// Mark as processed
	_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.WebhookService.MarkProcessed(ctx, tx, webhookEvent.ID)
	})

	w.WriteHeader(http.StatusOK)
}

// processWebhookEvent routes a verified event to the appropriate handler.
func (d *Deps) processWebhookEvent(r *http.Request, _ *domain.WebhookEvent, event *payments.WebhookEvent) error {
	ctx := r.Context()

	switch event.Type {
	case payments.EventPaymentIntentSucceeded:
		return d.handlePaymentIntentSucceeded(ctx, event)

	case payments.EventPaymentIntentPaymentFailed:
		return d.handlePaymentIntentFailed(ctx, event)

	case payments.EventPaymentIntentCanceled:
		return d.handlePaymentIntentCanceled(ctx, event)

	case payments.EventChargeRefunded:
		return d.handleChargeRefunded(ctx, event)

	default:
		// Unhandled event type — not an error, just skip
		return nil
	}
}

// handlePaymentIntentSucceeded drives the pre-created order to confirmed when
// Stripe reports the PI as succeeded. Idempotent — if the order has already
// been transitioned (e.g. by the redirect-back path) this no-ops cleanly via
// ConfirmCheckoutPayment's row-level lock.
//
// For subscription-signup orders (pre-created by the subscribe flow), payment
// success is also what brings the subscription itself into existence — this
// webhook is the safety net that activates it even when the customer's
// browser never came back to call /api/subscribe/confirm.
func (d *Deps) handlePaymentIntentSucceeded(ctx context.Context, event *payments.WebhookEvent) error {
	piID, err := extractPaymentIntentID(event.Data)
	if err != nil {
		return fmt.Errorf("extract PI ID: %w", err)
	}

	logger := logging.FromContext(ctx)

	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		order, transitioned, err := d.CheckoutService.ConfirmCheckoutPayment(ctx, tx, piID, systemActor())
		if err != nil {
			if errors.Is(err, app.ErrOrderNotFound) {
				logger.Warn("webhook: no order for PI", "payment_intent_id", piID)
				return nil // PI might not be associated with an order yet
			}
			return fmt.Errorf("confirm checkout payment: %w", err)
		}

		if !transitioned {
			// Money was captured but the order can't move forward — almost
			// always a benign idempotent re-entry, but a cancelled order
			// here means a real capture with nothing to fulfil. Shout.
			if order.Status == domain.OrderStatusCancelled {
				logger.Error("webhook: payment succeeded for cancelled order — needs manual refund or restore",
					"order_id", order.ID, "order_number", order.Number, "payment_intent_id", piID)
			}
			return nil
		}

		if _, ok := app.SubscriptionSignupPlanID(order.Metadata); ok {
			sub, aErr := d.SubscriptionService.ActivateFromSignupOrder(ctx, tx, order, systemActor())
			if aErr != nil {
				return fmt.Errorf("activate signup subscription: %w", aErr)
			}
			if _, jErr := d.RiverClient.InsertTx(ctx, tx, jobs.SubscriptionConfirmEmailArgs{
				SubscriptionID: sub.ID,
				CustomerID:     sub.CustomerID,
			}, nil); jErr != nil {
				return fmt.Errorf("enqueue subscription confirm email: %w", jErr)
			}
		}
		return nil
	})
}

// handlePaymentIntentFailed marks the order's payment as failed.
func (d *Deps) handlePaymentIntentFailed(ctx context.Context, event *payments.WebhookEvent) error {
	piID, err := extractPaymentIntentID(event.Data)
	if err != nil {
		return fmt.Errorf("extract PI ID: %w", err)
	}

	logger := logging.FromContext(ctx)

	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		order, err := d.OrderService.GetOrderByStripePaymentIntentIDForUpdate(ctx, tx, piID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("webhook: no order for failed PI", "payment_intent_id", piID)
				return nil
			}
			return fmt.Errorf("get order by PI: %w", err)
		}

		// Idempotent: if payment is already failed (or already past awaiting
		// for some other reason), skip the update + side effects.
		if order.PaymentStatus == domain.PaymentStatusFailed {
			return nil
		}

		_, err = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusFailed, systemActor())
		if err != nil {
			return fmt.Errorf("update payment status failed: %w", err)
		}

		// Pre-created orders (retail checkout and subscribe signups) stay in
		// pending+failed rather than being cancelled here: the PaymentIntent
		// is still live and the customer may fix their card and retry it —
		// ConfirmCheckoutPayment accepts the failed → captured transition.
		// If they never do, the abandoned-order sweep cancels the order (and
		// releases any coupon) after its grace window.

		// If this order belongs to a subscription, mark it past_due and notify
		// the customer — but only on the active → past_due transition.
		// MarkPastDue returns ErrSubscriptionNotActive if the subscription is
		// already past_due (or in another non-active state), in which case the
		// customer was already notified by the original transition.
		if order.SubscriptionID != nil && order.CustomerID != nil {
			_, err = d.SubscriptionService.MarkPastDue(ctx, tx, *order.SubscriptionID)
			if err != nil {
				if errors.Is(err, app.ErrSubscriptionNotActive) {
					return nil
				}
				return fmt.Errorf("mark subscription past_due: %w", err)
			}
			// Route through the enqueuer (not a raw InsertTx) so this past-due
			// notice respects notification quiet hours like the renewal-batch
			// path does — a 2am card-decline webhook shouldn't ping the customer.
			// This webhook fires on the active → past_due transition, so it is
			// always the customer's first word that a charge failed.
			if err := d.Enqueuer.EnqueuePastDueNotice(ctx, tx, *order.SubscriptionID, *order.CustomerID,
				jobs.SubscriptionPastDueStageFirst); err != nil {
				return fmt.Errorf("enqueue past-due email: %w", err)
			}
		}

		return nil
	})
}

// handlePaymentIntentCanceled cancels the linked order. Stripe auto-cancels
// PaymentIntents that the customer never finished authorizing (Klarna 48h
// timeout, abandoned card flows that the merchant manually cancels, etc.).
// CancelOrder releases any coupon redemption. Idempotent.
func (d *Deps) handlePaymentIntentCanceled(ctx context.Context, event *payments.WebhookEvent) error {
	piID, err := extractPaymentIntentID(event.Data)
	if err != nil {
		return fmt.Errorf("extract PI ID: %w", err)
	}

	logger := logging.FromContext(ctx)

	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		order, err := d.OrderService.GetOrderByStripePaymentIntentIDForUpdate(ctx, tx, piID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("webhook: no order for canceled PI", "payment_intent_id", piID)
				return nil
			}
			return fmt.Errorf("get order by PI: %w", err)
		}

		// Only cancel pre-payment intent orders. If the order has already
		// been confirmed (e.g. payment was eventually captured before the
		// PI was canceled — unusual but possible), leave it alone.
		if order.Status != domain.OrderStatusPending {
			return nil
		}

		if _, err := d.OrderService.CancelOrder(ctx, tx, order.ID, systemActor()); err != nil {
			if errors.Is(err, app.ErrOrderNotCancellable) {
				return nil
			}
			return fmt.Errorf("cancel order on PI canceled: %w", err)
		}
		return nil
	})
}

// handleChargeRefunded processes a refund event from Stripe. Idempotent on
// the payment-status side (UpdatePaymentStatus tolerates re-runs), but the
// notification email is suppressed if the order is already in a refunded
// state to avoid double-sending when the admin has already marked it.
func (d *Deps) handleChargeRefunded(ctx context.Context, event *payments.WebhookEvent) error {
	piID, refundAmount, err := extractRefundDetails(event.Data)
	if err != nil {
		return fmt.Errorf("extract refund details: %w", err)
	}

	logger := logging.FromContext(ctx)

	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		order, err := d.OrderService.GetOrderByStripePaymentIntentIDAsStaff(ctx, tx, piID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("webhook: no order for refunded charge", "payment_intent_id", piID)
				return nil
			}
			return fmt.Errorf("get order by PI: %w", err)
		}

		alreadyRefunded := order.PaymentStatus == domain.PaymentStatusRefunded

		_, err = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusRefunded, systemActor())
		if err != nil {
			return fmt.Errorf("update payment status refunded: %w", err)
		}

		// Mirror the admin refund flow: the order itself transitions to
		// refunded so the admin UI reflects it. Skip if already there to keep
		// the webhook idempotent.
		if order.Status != domain.OrderStatusRefunded {
			if _, err := d.OrderService.UpdateOrderStatus(ctx, tx, order.ID, domain.OrderStatusRefunded, systemActor()); err != nil {
				return fmt.Errorf("update order status refunded: %w", err)
			}
		}

		if alreadyRefunded || order.CustomerID == nil {
			return nil
		}

		if _, err := d.RiverClient.InsertTx(ctx, tx, jobs.RefundConfirmationArgs{
			OrderID:      order.ID,
			CustomerID:   *order.CustomerID,
			RefundAmount: refundAmount,
		}, nil); err != nil {
			return fmt.Errorf("enqueue refund confirmation email: %w", err)
		}

		return nil
	})
}

// --- Helpers ---

func systemActor() app.Actor {
	return app.Actor{
		Type: domain.AuditActorTypeSystem,
		Name: "stripe_webhook",
	}
}

// extractPaymentIntentID pulls the PaymentIntent ID from a payment_intent event's data.
func extractPaymentIntentID(data []byte) (string, error) {
	var obj struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf("unmarshal PI event: %w", err)
	}
	if obj.ID == "" {
		return "", fmt.Errorf("missing payment_intent ID in event data")
	}
	return obj.ID, nil
}

// extractRefundDetails pulls the PaymentIntent ID and the most recent refund
// amount (in cents) from a charge.refunded event's data. Stripe sends the
// charge object with a list of refunds; the latest one is the refund this
// event is firing for.
func extractRefundDetails(data []byte) (string, int, error) {
	var obj struct {
		PaymentIntent  string `json:"payment_intent"`
		AmountRefunded int    `json:"amount_refunded"`
		Refunds        struct {
			Data []struct {
				Amount int `json:"amount"`
			} `json:"data"`
		} `json:"refunds"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", 0, fmt.Errorf("unmarshal charge event: %w", err)
	}
	if obj.PaymentIntent == "" {
		return "", 0, fmt.Errorf("missing payment_intent in charge event data")
	}
	// Prefer the most recent individual refund amount (works for partials);
	// fall back to amount_refunded (cumulative) if the refunds list is empty.
	amount := obj.AmountRefunded
	if n := len(obj.Refunds.Data); n > 0 {
		amount = obj.Refunds.Data[n-1].Amount
	}
	return obj.PaymentIntent, amount, nil
}
