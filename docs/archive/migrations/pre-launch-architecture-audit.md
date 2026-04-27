# Pre-launch architecture audit

Record of the `/architect` audit pass run the week before launch and the work that shipped from it. Five findings against the e-commerce core (catalog, pricing, orders, fulfillment, subscriptions, customers, discounts, wholesale). Admin UI was deliberately out of scope for this pass.

All five findings are resolved as of commit `f135b15` (`Tighten backend boundaries: pull store out of web/jobs, route email through app`). `mage check` (lint + scoping + full test suite) green.

---

## Status

| # | Finding | Status |
|---|---|---|
| 1 | Remove store from `web.Deps` | ✅ done |
| 2 | Route email workers through app services | ✅ done |
| 3 | Document `provider.go` convention in CLAUDE.md | ✅ done (Option B — pragmatic path) |
| 4 | `AsStaff` suffix + CI scoping lint | ✅ done (rename-and-lint; type-split scheduled post-launch) |
| 5 | Remove raw SQL from `WholesaleService` | ✅ done |

---

## What shipped

### Finding #1 — `web.Deps` no longer imports `store`

`Deps` used to hold 7 `*store.X` references (`CustomerStore`, `AuditStore`, `WebhookStore`, `MagicLinkStore`, `CustomerGroupStore`, `SettingsStore`, `QBCredentialStore`). Every one of those was either moved behind a new app service or dropped:

- New: `app.AuditQueryService`, `app.WebhookService`, `app.CustomerGroupService`.
- New: `platform/quickbooks.OAuthManager` (absorbs CSRF cookie + code exchange + token encryption + credential persistence).
- Extended `CustomerService` with `CountAddresses`, `LinkStripeCustomerID`, `UpdatePaymentTerms`, `UpdateBillingMethod` — all audited writes (the last two previously wrote without audit).
- Dead `MagicLinkStore` and `SettingsStore` (declared in `Deps` but never read) deleted.
- Bonus fix: `domain.QBCredentials` moved from `platform/quickbooks/provider.go` to `internal/domain/quickbooks.go`, reversing an accidental `store → platform` import.
- 9 new audit action constants (customer group CRUD, billing method/terms, Stripe ID link, QB connect/disconnect).

### Finding #2 — email workers go through app services

Seven River workers (`magic_link_send`, `email_order_confirm`, `email_subscription_confirm`, `invoice_send`, `wholesale_application`, `wholesale_approved`, `wholesale_suspended`) used to hold raw stores and compose/send email in-line with no audit and no metrics. Each is now a ~20-line delegator that calls a service method:

- `AuthService.SendMagicLink`, `OrderService.SendConfirmationEmail`, `SubscriptionService.SendConfirmationEmail`, `InvoiceService.SendInvoice`, `WholesaleService.SendApplicationNotice`, `WholesaleService.SendApprovalEmail`, `WholesaleService.SendSuspensionEmail`.
- Common deps (mailer, renderer, fromAddr, baseURL, storeName, staffEmail) are bundled in a new `app.EmailEnv` injected via a `WithEmail(...)` builder on each service.
- Services follow the three-phase pattern from `RenewalService`: read tx → external send (no tx) → audit tx.
- New Prometheus counter `emails_sent_total{kind,status}` and 7 new audit action constants (one per flow).
- Renaming: old `InvoiceService.SendInvoice` (state transition, draft → sent) is now `MarkSent` so the new method owns the actual email delivery.

### Finding #3 — `provider.go` convention documented

CLAUDE.md's "External Service Calls" section now says:

- `provider.go` is for external services with pluggable implementations (email, payments, shipping, QB OAuth). Concrete impls live alongside.
- Internal platform concerns (audit, metrics, sessions, media, tax, logging, help, ratelimit, auth) expose concrete types directly. No interface until a second implementation is needed.
- External calls still go outside transactions; the `RenewalService` two-phase pattern is the template.

This is the pragmatic ("Option B") resolution. If testing friction grows post-launch, we can extract interfaces for `audit.Recorder`, `media.ObjectStore`, etc.

### Finding #4 — staff-only lookups suffixed `AsStaff`

10 store methods and 6 service wrappers renamed so any unscoped customer-owned-resource lookup is visible at the call site:

- Store: `OrderStore.GetOrderByIDAsStaff`, `GetOrderByNumberAsStaff`, `GetOrderByStripePaymentIntentIDAsStaff`, `GetOrderByQBInvoiceIDAsStaff`, `GetCartByIDAsStaff`; `CartStore.GetCartByIDAsStaff`; `SubscriptionStore.GetByIDAsStaff`; `InvoiceStore.GetByIDAsStaff`; `FulfillmentStore.GetFulfillmentByIDAsStaff`; `CustomerStore.GetAddressByIDAsStaff`; `ShippingStore.GetShipmentByIDAsStaff`.
- Service: `OrderService.GetOrderAsStaff`, `GetOrderByNumberAsStaff`, `GetOrderByStripePaymentIntentIDAsStaff`; `SubscriptionService.GetSubscriptionAsStaff`; `InvoiceService.GetInvoiceAsStaff`; `CustomerService.GetAddressByIDAsStaff`.
- Three customer-facing call sites carry `// scoping:` waiver comments explaining why `AsStaff` is safe there (two: ownership enforced immediately; one: checkout — see follow-up below).
- New `mage checkScoping` target scans 7 customer-facing handler files (`account.go`, `cart.go`, `checkout.go`, `customer_auth.go`, `storefront.go`, `subscribe.go`, `wholesale.go`) for `AsStaff(` calls lacking a `// scoping:` waiver on the preceding 3 lines. Wired into `mage check` so drift fails CI.

### Finding #5 — `WholesaleService` no longer writes raw SQL

Six `tx.Exec` sites in `app/wholesale.go` replaced with named store methods:

- `CustomerStore.SetWholesaleApplicationFields`, `SetWholesaleApproved`, `SetWholesaleNotes`, `SetWholesaleSuspended`, `SetWholesaleReactivated`.
- `OrderStore.SetCustomerPONumber`.

The store owns all SQL again; services compose store methods.

---

## Open follow-ups

### Known issues surfaced during the audit (not fixed yet)

**1. Checkout accepts a client-submitted `addressID` without customer scoping.** `internal/web/checkout.go` (search for `// scoping: addressID comes from client-submitted JSON`) looks up shipping addresses for tax calculation and order creation using an address ID from the POST body, without verifying the address belongs to the submitted `customerID`. Impact is bounded (the address content is not echoed back to the client; only tax + order destination are affected), but an attacker could pair their own `customerID` with a victim's `addressID` and cause misdirected shipping. Fix: add `CheckoutService.ResolveCustomerAddress(customerID, addressID)` that verifies ownership, or tighten the existing flow so the address is always created fresh per checkout. Post-launch priority.

### Scheduled post-launch work

**2. Finding #4 — type-split for staff vs customer-facing store methods.** The pre-launch rename-and-lint is a cheap defense; the stronger fix is to split stores into `store.CustomerFacingSubscriptions` / `store.StaffSubscriptions` etc. and wire them into different services, so the type system (not a grep) enforces that customer-facing handlers cannot accidentally load unscoped data. Defer to post-launch since it touches most store callers.

**3. Finding #3 — interface extraction if testing pain grows.** We landed on the pragmatic Option B. If writing unit tests for services becomes painful because the concrete `audit.AuditWriter` / `*media.R2Client` / `quickbooks.Client` are awkward to mock, revisit and extract interfaces into `provider.go` per sub-package.

### Out-of-scope for this pass — tracked separately

**4. Admin panel review.** Explicitly excluded from this audit per the original scope. Needs its own architecture + UX review pass before launch or shortly after.

---

## Reference

- Commit: `f135b15 Tighten backend boundaries: pull store out of web/jobs, route email through app` — 60 files, +1749/-1059.
- Previous architecture spec: `docs/CLAUDE-backend.md`, `docs/lean-commerce-package-structure.md`.
- CLAUDE.md was updated as part of this pass (`provider.go` section).
