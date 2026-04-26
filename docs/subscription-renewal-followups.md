# Subscription Renewal — Follow-ups

Items surfaced by the 2026-04-26 incident where two subscriptions silently flipped to `past_due` without any Stripe activity (root cause: `ListPaymentMethods` filtered to `type=card` and excluded customers whose only saved method was Stripe Link). The filter has been removed; these are the leftover gaps the incident exposed.

## 1. No audit record on past-due transitions

`MarkPastDue` (`internal/app/subscriptions.go`) and the four `UpdateStatus(...PastDue)` calls inside `internal/app/renewal.go` (single + batch paths, both no-PM and PI-failure branches) write the status change directly with no audit row.

When a subscription flips to past_due there is no record of:
- which renewal attempt triggered it (job ID)
- which Stripe error caused it (decline code, message, request log URL)
- whether it came via the renewal job or the webhook handler

Result: when an admin sees `past_due` on the dashboard, the only way to find out why is to cross-reference Stripe by hand — and only if the underlying call actually reached Stripe.

**Suggested fix:** record `audit.AuditSubscriptionPastDue` (new action) inside the same transaction as the status update. Include the Stripe error code/message in `metadata` for the renewal-job paths. For the webhook path (`internal/web/webhook.go`), include the PaymentIntent ID and `last_payment_error`.

## 2. Past-due failures lost on container restart

The renewal job's only durable signal on failure is the returned error. River's failure logs go to stdout; on the production VPS the Docker JSON log driver has no rotation policy and gets wiped on every container replacement.

The 2026-04-26 incident was diagnosable only because Stripe retains every PaymentIntent attempt — for the no-PM branch (which never calls Stripe), there would have been no recoverable trail at all.

**Suggested fix (any one):**
- Persist renewal failures to the audit log per item 1 above (preferred — it solves both problems)
- Configure Docker `log-opts` (`max-size`, `max-file`) on the VPS so logs survive a container replacement
- Ship logs off-host (Loki/Grafana already running for metrics — would be a small add)

## 3. `invoice.payment_failed` webhook is defined but not handled

`internal/platform/payments/webhook.go:15` declares the event type as a constant. The switch in `internal/web/webhook.go:99-112` only branches on `payment_intent.succeeded`, `payment_intent.payment_failed`, and `charge.refunded`. The `invoice.payment_failed` constant is unreferenced.

Likely a no-op today (Hiri does not use Stripe Invoices — it creates PaymentIntents directly), but if Stripe Tax or a future Stripe-managed subscription path is added, this becomes a silent miss.

**Suggested action:** either delete the unused constant, or wire up a handler if/when invoice-based billing is introduced.

## 4. Admin "Reactivate past_due" button — deliberately deferred

The dashboard has no way to flip `past_due → active`. The 2026-04-26 incident required raw SQL on the production DB. A button is tempting, but the right pattern is two distinct actions, not one:

- **Retry payment** — re-enqueue a renewal job for this subscription (operator says "I confirmed with the customer, try the saved PM again").
- **Mark current period paid** — clear `past_due` without a charge (operator handled payment outside the system, e.g. customer paid by check).

Both need a captured reason on the audit record. A single "Reactivate" button without those distinctions is dangerous in the general case: most past-due states are real card failures (declined, expired, insufficient funds), and a second immediate charge attempt is exactly what banks flag.

**Blocked on item 1.** Until past-due transitions carry an audit trail with the underlying Stripe error, the operator has no basis for choosing between the two actions — they'd be guessing at the cause. Build item 1 first; then this becomes safe to add.

## 5. Default payment method is not respected

Both renewal paths pick `methods[0]` from the `ListPaymentMethods` response. With the type filter gone, this becomes "first PM Stripe returns" — usually the most recently attached, but not guaranteed. If a customer adds a backup card after their primary fails, the renewal job may still pick the broken one.

**Suggested fix:** prefer `customer.invoice_settings.default_payment_method` when set; fall back to `methods[0]` only if no default exists. Stripe's idiomatic pattern for off-session charging.
