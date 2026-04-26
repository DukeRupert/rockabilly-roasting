# Stripe Setup

One-time configuration that lives in the Stripe Dashboard, not in code. Run
through this checklist on a fresh Stripe account, or after rotating the API
keys onto a new account.

## API keys

Set these in the deploy environment (`.env` for local dev):

- `STRIPE_SECRET_KEY` — `sk_live_...` (production) or `sk_test_...` (dev)
- `STRIPE_WEBHOOK_SECRET` — `whsec_...` from the webhook endpoint below

## Webhook endpoint

Stripe Dashboard → Developers → Webhooks → Add endpoint.

- URL: `https://rockabillyroasting.com/webhooks/stripe`
- Events: whatever the current `internal/web/stripe_webhook.go` handler
  switches on. At minimum: `payment_intent.succeeded`,
  `payment_intent.payment_failed`, `charge.refunded`. Add events here as the
  handler grows.
- Copy the signing secret into `STRIPE_WEBHOOK_SECRET`.

## Customer Portal (payment-method self-serve)

Customers update their saved payment method via Stripe's hosted Billing
Portal. The "Manage payment method" button on `/account/subscriptions` POSTs
to `/account/billing-portal`, which creates a portal session and 303s the
customer to Stripe. The portal does not work until you enable it once in the
dashboard.

Stripe Dashboard → Settings → Billing → Customer portal:

- **Functionality → Payment methods**: ✅ Allow customers to update their
  payment methods.
- **Functionality → Update payment method**: ✅ Default payment method —
  Allow customers to set a default payment method. (When a customer adds a
  new card, Stripe sets it as the default automatically; renewal charges the
  default.)
- **Business information**: Headline, privacy policy URL, terms URL.
- **Branding**: Stripe pulls from the account's brand settings (logo, accent
  color). Match the Rockabilly palette: rust `#B4351D` for accent.
- Save.

The portal needs to be active in **both** test mode and live mode if you
want to exercise it in dev. Toggle the test/live switch in the dashboard
and repeat the configuration in each.

### Why we don't auto-detach old cards

The portal lets customers add a card without removing the old one. We don't
care: renewal charges `customer.default_payment_method`, which Stripe keeps
pointed at the most recently added card. Old cards sit harmlessly in the
customer's account. If a webhook-driven detach-on-add ever becomes
necessary, listen for `payment_method.attached` and detach all other PMs on
that customer.

## Stripe Tax

If automatic tax calculation is enabled (`SetupFutureUsage` and
shipping-address-driven tax in `internal/platform/payments/stripe.go`), the
Stripe Tax product must be activated in the dashboard:

Stripe Dashboard → Tax → Get started → register the merchant's nexus state
(WA — Kennewick) and any other states with physical or economic nexus.

## Stripe Link

We do not opt out of Link. `ListPaymentMethods` returns Link methods
alongside cards, and the renewal path handles Link by reading
`default_payment_method` (type-agnostic). Don't reintroduce a `type=card`
filter — it broke renewals on 2026-04-25 and is the reason
`internal/app/renewal.go:pickRenewalPaymentMethod` exists.
