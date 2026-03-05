# Subscription Validation Roadmap

Goal: end-to-end subscription flow — create a product, check out with Stripe Payment Intents, process webhooks, create orders, and validate subscription lifecycle.

## Current State (as of March 2025)

**Already built:**
- Product CRUD (store, service, admin handlers, admin UI)
- Order domain types, store, service (status state machines, refund guards)
- Subscription domain types, store, partial service (read-only — no mutations)
- Webhook event deduplication (store + migration)
- Checkout service (`PlaceOrder` — discount calc, line items, but no Stripe calls)
- Audit infrastructure, metrics counters, sentinel errors
- All database migrations (14 total)

**Not built:**
- Stripe SDK integration (not even in go.mod)
- Payment provider interface (`platform/payments/`)
- PaymentIntent creation flow
- Webhook HTTP handler + signature verification
- River job worker implementations
- Storefront handlers + templates
- Subscription mutations (create, pause, cancel, renew)
- Session/auth wiring (handlers use `devActor()` placeholder)

## Implementation Phases

### Phase 1: Stripe Integration Layer
Create `platform/payments/` with provider interface and Stripe implementation.

**Scope:**
- Add `stripe-go` SDK dependency
- Define `Provider` interface (create payment intent, confirm, refund, retrieve)
- Implement `StripeProvider` with real Stripe API calls
- Webhook signature verification helper
- Config struct for API keys + webhook secret

**Files:**
- `platform/payments/provider.go` — interface
- `platform/payments/stripe.go` — Stripe implementation
- `platform/payments/webhook.go` — signature verification

### Phase 2: Checkout Flow (Payment Intents)
Wire Stripe into checkout to create and manage Payment Intents.

**Scope:**
- `CheckoutService.CreatePaymentIntent` — build PI from cart + customer, handle tax exempt vs automatic tax
- Storefront cart/checkout HTTP handlers
- Svelte checkout component integration (client-side Stripe Elements)
- Store cart → PI association (stripe_payment_intent_id on orders table)

**Depends on:** Phase 1

### Phase 3: Webhook Processing
Receive and process Stripe events to drive order/payment state.

**Scope:**
- `POST /webhooks/stripe` handler — verify signature, dedup, enqueue River job
- `ProcessWebhookEvent` River worker — route by event type
- Handle: `payment_intent.succeeded`, `payment_intent.payment_failed`, `charge.refunded`
- Wire `OrderService.UpdatePaymentStatus` from webhook events
- `FinalizeFromPayment` — confirm order when payment succeeds

**Depends on:** Phase 1, Phase 2

### Phase 4: Order Creation from Checkout
Complete the checkout → order → confirmation pipeline.

**Scope:**
- Order confirmation flow (payment succeeded → order confirmed)
- `OrderConfirmation` River job (send confirmation email)
- Admin order detail shows Stripe payment info
- Refund flow: call Stripe API, then update order state

**Depends on:** Phase 3

### Phase 5: Subscription Lifecycle
Implement subscription creation, renewal, and management.

**Scope:**
- `SubscriptionService` mutations: `Create`, `Pause`, `Resume`, `Cancel`
- `SubscriptionRenewal` River worker — generate renewal order + PI
- `PaymentRetry` River worker — retry failed subscription payments
- Subscription status state machine enforcement
- Admin subscription management UI
- Storefront subscription signup flow

**Depends on:** Phase 4

## Migration Note: WooCommerce Subscriptions

This project will migrate existing WooCommerce subscriptions that use Payment Intents. Key considerations:
- Subscriptions are a scheduling mechanism — they generate standard Orders, not Stripe Subscriptions
- Each renewal creates a new PaymentIntent (matching WooCommerce's model)
- Saved payment methods (Stripe Customer + PaymentMethod) enable off-session renewals
- Migration tooling (import existing customers, subscriptions, payment methods) is out of scope for this roadmap but should follow Phase 5

## Testing Strategy

- Unit tests with mock payment provider for all service methods
- Integration tests against Stripe test mode for `StripeProvider`
- Stripe CLI (`stripe listen --forward-to`) for local webhook testing
- End-to-end: create product → checkout → webhook → order confirmed → subscription renewal
