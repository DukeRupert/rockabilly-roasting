# QuickBooks wholesale invoice reconciliation

How a wholesale order's payment status stays in sync with its QuickBooks
invoice, and why the manual invoice path is fenced off.

## Source of truth

For a wholesale order, **QuickBooks owns the payment status**. The discriminator
is a single column: an order is *QB-managed* iff `orders.qb_invoice_id IS NOT
NULL`. The internal `InvoiceService` / `invoices` table is the **manual
fallback**, used only when QuickBooks is not connected.

To keep the two from fighting over `orders.payment_status`, the manual path is
fenced: `InvoiceService.CreateFromOrder` / `MarkSent` / `RecordPayment` /
`VoidInvoice` reject any QB-managed order with `app.ErrOrderQBManaged`. A
QB-managed order therefore can never acquire a manual invoice, and the
reconciliation poll (which filters on `qb_invoice_id IS NOT NULL`) never touches
a manually-invoiced order. The two regimes are disjoint by construction.

## The single writer

`OrderService.ReconcileWholesalePayment` is the **only** code that writes
QB-driven payment status. Both triggers funnel through it:

- **Webhook (fast path):** `POST /webhooks/quickbooks` → `qb_process_invoice_update`
  job → reconcile. Fires within seconds of a QB change. Handles Invoice
  Update/Void/Delete operations. Create is deliberately skipped (it races the
  transaction persisting `qb_invoice_id` for invoices we cut ourselves);
  Emailed and Merge are ignored — neither changes payment state.
- **Poll (safety net):** the `qb_reconcile_invoices` periodic job runs **daily**,
  sweeps every open QB-managed order, and reconciles each. This covers missed
  Intuit webhooks (which are not perfectly reliable) and is the detector that
  flips unpaid invoices to `overdue`.

Each worker fetches the invoice from QuickBooks *outside* any transaction, then
calls `ReconcileQBInvoiceByID`, which re-reads the order `FOR UPDATE` and runs
the decision inside one transaction. The `FOR UPDATE` lock serializes a webhook
and a poll that race the same invoice, so status changes and emails fire once.

## Decision precedence

Given QB's `balance`, `total`, and `due date` (all read back from QB — the due
date is authoritative there, not recomputed):

| # | Condition | Result |
|---|-----------|--------|
| 1 | invoice deleted in QB (404) **or** total ≤ 0 (voided) | revert to `pending_invoice` (audit `qb.invoice_voided`) |
| 2 | balance ≤ 0 (incl. overpayment/credit) | `captured` + `email:invoice_paid` |
| 3 | balance owed **and** now > due date | `overdue` + milestone past-due reminder |
| 4 | 0 < balance < total | `partially_paid` |
| 5 | fully unpaid, still `pending_invoice` | `invoiced` |
| 6 | otherwise | no change |

The method is idempotent and a no-op for terminal order states (cancelled /
refunded) and already-settled payment statuses.

## Past-due reminders

While an invoice is overdue, up to four reminders are sent, keyed to **QB's
authoritative due date** so the cadence holds for any NET terms: stage 1 fires
the first time the invoice is seen overdue, stages 2–4 weekly after that, then
silence. `orders.overdue_reminder_stage` records how many have been sent (1–4),
so each fires exactly once even though the poll re-checks daily; the
`email:invoice_past_due` job is additionally `UniqueOpts{ByArgs}` as a second
guard. The reminder email displays the due date the invoice was actually issued
under — QB's, carried through the job args — never one recomputed from the
customer's current terms (which may have changed since the invoice was cut).

> Per-stage **copy** is still placeholder — see
> [qb-overdue-reminders-TODO.md](qb-overdue-reminders-TODO.md).

## Money representation

Orders store money as integer cents; QuickBooks reports float dollars. The
quickbooks package converts with `math.Round(dollars * 100)` (round, not
truncate) so 0.33 → 33¢, not 32¢. A voided QB invoice zeroes its amounts, which
is how `total ≤ 0` distinguishes a void from a genuinely paid (`balance 0,
total > 0`) invoice.
