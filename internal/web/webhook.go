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

	// Dedup + persist event in a transaction
	var webhookEvent *domain.WebhookEvent
	err = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		var txErr error
		webhookEvent, txErr = d.WebhookStore.Create(ctx, tx, "stripe", event.ID, event.Type, json.RawMessage(event.Data))
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
		w.WriteHeader(http.StatusOK)
		return
	}

	// Process the event
	if err := d.processWebhookEvent(r, webhookEvent, event); err != nil {
		logger.Error("webhook: processing failed", "event_id", event.ID, "type", event.Type, "error", err)
		// Mark as failed but still return 200 to Stripe (prevent retries for known failures)
		reason := err.Error()
		_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
			return d.WebhookStore.MarkFailed(ctx, tx, webhookEvent.ID, &reason)
		})
		w.WriteHeader(http.StatusOK)
		return
	}

	// Mark as processed
	_ = store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		return d.WebhookStore.MarkProcessed(ctx, tx, webhookEvent.ID)
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

	case payments.EventChargeRefunded:
		return d.handleChargeRefunded(ctx, event)

	default:
		// Unhandled event type — not an error, just skip
		return nil
	}
}

// handlePaymentIntentSucceeded confirms the order associated with a successful payment.
func (d *Deps) handlePaymentIntentSucceeded(ctx context.Context, event *payments.WebhookEvent) error {
	piID, err := extractPaymentIntentID(event.Data)
	if err != nil {
		return fmt.Errorf("extract PI ID: %w", err)
	}

	logger := logging.FromContext(ctx)

	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		order, err := d.OrderService.GetOrderByStripePaymentIntentID(ctx, tx, piID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("webhook: no order for PI", "payment_intent_id", piID)
				return nil // PI might not be associated with an order yet
			}
			return fmt.Errorf("get order by PI: %w", err)
		}

		// Only update if payment is still awaiting
		if order.PaymentStatus == domain.PaymentStatusAwaiting || order.PaymentStatus == domain.PaymentStatusAuthorized {
			_, err = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusCaptured, systemActor())
			if err != nil {
				return fmt.Errorf("update payment status: %w", err)
			}
		}

		// Confirm order if still pending
		if order.Status == domain.OrderStatusPending {
			_, err = d.OrderService.UpdateOrderStatus(ctx, tx, order.ID, domain.OrderStatusConfirmed)
			if err != nil {
				return fmt.Errorf("confirm order: %w", err)
			}
		}

		d.Metrics.PaymentsCapturedTotal.WithLabelValues("stripe").Inc()
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
		order, err := d.OrderService.GetOrderByStripePaymentIntentID(ctx, tx, piID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("webhook: no order for failed PI", "payment_intent_id", piID)
				return nil
			}
			return fmt.Errorf("get order by PI: %w", err)
		}

		_, err = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusFailed, systemActor())
		if err != nil {
			return fmt.Errorf("update payment status failed: %w", err)
		}

		// If this order belongs to a subscription, mark it past_due
		if order.SubscriptionID != nil {
			_, err = d.SubscriptionService.MarkPastDue(ctx, tx, *order.SubscriptionID)
			if err != nil && !errors.Is(err, app.ErrSubscriptionNotActive) {
				return fmt.Errorf("mark subscription past_due: %w", err)
			}
		}

		d.Metrics.PaymentsFailedTotal.WithLabelValues("stripe", "payment_failed").Inc()
		return nil
	})
}

// handleChargeRefunded processes a refund event from Stripe.
func (d *Deps) handleChargeRefunded(ctx context.Context, event *payments.WebhookEvent) error {
	piID, err := extractChargePaymentIntentID(event.Data)
	if err != nil {
		return fmt.Errorf("extract PI from charge: %w", err)
	}

	logger := logging.FromContext(ctx)

	return store.Tx(ctx, d.Pool, func(tx pgx.Tx) error {
		order, err := d.OrderService.GetOrderByStripePaymentIntentID(ctx, tx, piID)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				logger.Warn("webhook: no order for refunded charge", "payment_intent_id", piID)
				return nil
			}
			return fmt.Errorf("get order by PI: %w", err)
		}

		_, err = d.OrderService.UpdatePaymentStatus(ctx, tx, order.ID, domain.PaymentStatusRefunded, systemActor())
		if err != nil {
			return fmt.Errorf("update payment status refunded: %w", err)
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
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf("unmarshal PI event: %w", err)
	}
	if obj.Object.ID == "" {
		return "", fmt.Errorf("missing payment_intent ID in event data")
	}
	return obj.Object.ID, nil
}

// extractChargePaymentIntentID pulls the PaymentIntent ID from a charge event's data.
func extractChargePaymentIntentID(data []byte) (string, error) {
	var obj struct {
		Object struct {
			PaymentIntent string `json:"payment_intent"`
		} `json:"object"`
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf("unmarshal charge event: %w", err)
	}
	if obj.Object.PaymentIntent == "" {
		return "", fmt.Errorf("missing payment_intent in charge event data")
	}
	return obj.Object.PaymentIntent, nil
}
