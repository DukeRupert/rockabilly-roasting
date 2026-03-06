# Subscription Validation Roadmap

Goal: end-to-end subscription flow — create a product, check out with Stripe Payment Intents, process webhooks, create orders, and validate subscription lifecycle.

> **See also:** `lean-commerce-subscriptions.md` for the full subscription design (data model, lifecycle, architecture decisions).

## Current State (as of March 2026)

**Built:**
- Product CRUD with `subscribable` toggle (store, service, admin UI with htmx switch)
- Subscription plans as decoupled cadence templates (day-based intervals, discount_pct)
- Subscription domain types, store, service (create, pause, resume, cancel, advance period)
- Renewal service (create payment, place order, advance period)
- River job workers for subscription renewal
- Stripe PaymentIntent integration (`platform/payments/`)
- Webhook handler with signature verification and event dedup
- Storefront subscribe page with Svelte checkout component
- Admin plan management, subscription list/detail with lifecycle actions
- Order domain with status state machines, refund guards
- Audit infrastructure, metrics counters, sentinel errors
- 17 database migrations

**Not built:**
- Customer self-service subscription management (change variant, change frequency, skip delivery)
- Payment retry worker for failed renewals
- Session/auth wiring (handlers use `devActor()` placeholder)
- WooCommerce subscription migration tooling

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

### Phase 3: Webhook Processing ✅
Receive and process Stripe events to drive order/payment state.

**Done:**
- `POST /webhooks/stripe` handler — verify signature, dedup, persist event
- Synchronous event processing (route by event type, no separate River job)
- Handle: `payment_intent.succeeded`, `payment_intent.payment_failed`, `charge.refunded`
- Wire `OrderService.UpdatePaymentStatus` from webhook events
- Graceful handling of unmatched payment intents (test events, etc.)
- API version mismatch tolerance via `ConstructEventWithOptions`

**Depends on:** Phase 1, Phase 2

### Phase 4: Order Creation from Checkout
Complete the checkout → order → confirmation pipeline.

**Scope:**
- Order confirmation flow (payment succeeded → order confirmed)
- `OrderConfirmation` River job (send confirmation email)
- Admin order detail shows Stripe payment info
- Refund flow: call Stripe API, then update order state

**Depends on:** Phase 3

### Phase 5: Subscription Lifecycle ✅ (validated end-to-end)
Implement subscription creation, renewal, and management.

**Done:**
- `SubscriptionService` mutations: `Create`, `Pause`, `Resume`, `Cancel`, `AdvancePeriod`
- `SubscriptionRenewal` River worker — generate renewal order + PI
- Subscription status state machine enforcement
- Admin plan management UI (create plans with interval + discount)
- Admin subscription detail with pause/resume/cancel actions
- Product `subscribable` toggle with htmx switch component
- Storefront subscribe page with Svelte checkout + Stripe Elements
- Stripe customer creation + payment method saving during subscribe checkout (`setup_future_usage: off_session`)
- Off-session renewal charges with saved payment methods
- Plan discount applied to renewal charges
- Renewal jobs permanently cancelled for inactive/missing subscriptions (no retry spam)
- Full lifecycle validated with Stripe test mode: subscribe → renew → pause → resume → renew → cancel
- Subscription quantity support (1–10 units per subscription)
- Batched renewals: multiple subscriptions for the same customer + address are consolidated into a single order with one Stripe charge (see `docs/batched-subscription-renewals.md`)
- Storefront `/subscriptions` landing page with subscribable product grid, strikethrough pricing, and `?mode=subscribe` deep-link to product detail

**Remaining:**
- `PaymentRetry` River worker — retry failed subscription payments
- Customer self-service portal (change variant, change frequency, skip)
- Revert scheduler interval from 1 minute back to 1 hour for production
- Remove `every_2_minutes` dev-only interval before production
- Validate batched renewals end-to-end with Stripe test mode

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

### Validated (March 6, 2026)
Using Stripe CLI + local Postgres (docker-compose) + 2-minute test plan:
- Webhook signature verification and event deduplication
- `payment_intent.succeeded`, `payment_intent.payment_failed`, `charge.refunded` handlers
- Unhandled event types silently skipped
- Subscribe checkout → Stripe customer created → payment method saved
- Automatic renewal after interval expires (off-session charge with saved PM)
- Pause stops renewals, resume resets billing period and renewals continue
- Cancel stops renewals, stale renewal jobs permanently cancelled
- Plan discount correctly applied to renewal charges
