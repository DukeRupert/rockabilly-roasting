# Invoices

This guide covers the invoice system for B2B (wholesale) orders in the admin panel.

## What Are Invoices?

When a wholesale customer places an order, the order is created with a payment status of `pending_invoice` instead of being charged through Stripe. Staff then create an invoice from the order, send it to the customer, and record payments as they arrive.

Invoices are always linked to an order. You access them through the order detail page.

---

## Invoice Detail Page

The invoice detail page shows the invoice number, status badge, creation date, and due date (if set). A link in the top right returns you to the associated order.

### Summary Cards

Four cards summarize the financial state:

| Card | Description |
|------|-------------|
| Subtotal | Total before shipping and tax |
| Total | Full invoice amount including shipping and tax |
| Paid | Amount received so far (shown in teal) |
| Amount Due | Remaining balance |

### Notes

If notes were added when the invoice was created, they appear in a highlighted box below the summary cards.

### Line Items

A table lists each item on the invoice:

| Column | Description |
|--------|-------------|
| Description | Product/variant description |
| Qty | Quantity ordered |
| Unit Price | Price per unit |
| Total | Line total (qty x unit price) |

### Payments

A table lists all recorded payments:

| Column | Description |
|--------|-------------|
| Date | When the payment was recorded |
| Method | Payment method (check, ACH, Stripe, cash, other) |
| Reference | Check number, transaction ID, or other reference |
| Amount | Payment amount |

---

## Invoice Lifecycle

Invoices move through these statuses:

```
draft --> sent --> partially_paid --> paid
  |        |
  v        v
 void     void
```

### Draft

The initial status when an invoice is created from an order. At this stage you can review it before sending.

Available actions: **Send Invoice**, **Void Invoice**

### Sent

The invoice has been emailed to the customer. The associated order's payment status changes to `invoiced`.

Available actions: **Record Payment**, **Void Invoice**

### Partially Paid

At least one payment has been recorded, but the full amount has not been received. The order's payment status changes to `partially_paid`.

Available actions: **Record Payment**

### Paid

The full invoice amount has been received. The order's payment status changes to `captured`. No further actions are available.

### Void

The invoice has been cancelled. The order's payment status reverts to `pending_invoice`, allowing a new invoice to be created if needed. Voiding is irreversible.

Only draft and sent invoices can be voided. Invoices that have received payments cannot be voided.

---

## Creating an Invoice

Invoices are created from the order detail page (not the invoice page itself). On a wholesale order with `pending_invoice` payment status, use the invoice creation form. You can optionally set:

- **Due date** -- when payment is expected
- **Notes** -- any additional information to include on the invoice

The system automatically copies all line items from the order to the invoice and calculates subtotal, shipping, tax, and total.

---

## Sending an Invoice

On a draft invoice, click **Send Invoice**. This:

1. Changes the invoice status from `draft` to `sent`
2. Updates the order's payment status to `invoiced`
3. Queues an email to the customer with the invoice details

The Send Invoice button only appears on draft invoices.

---

## Recording a Payment

On a sent or partially paid invoice, a "Record Payment" form appears below the payments table.

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| Amount (cents) | Yes | Payment amount in cents (e.g., enter `1499` for $14.99). The placeholder shows the remaining balance. |
| Method | Yes | Select from: Check, ACH, Stripe, Cash, Other |
| Reference | No | Free-text field for a check number, transaction ID, or other reference |

Click **Record** to save the payment.

The system automatically:

1. Records the payment with the current date
2. Recalculates the invoice status (partially paid or fully paid)
3. Updates the order's payment status accordingly
4. If QuickBooks is connected, queues a background job to sync the payment to QB

You can record multiple partial payments. Each one appears in the payments table.

---

## Voiding an Invoice

On a draft or sent invoice, click **Void Invoice**. A confirmation dialog appears warning that this cannot be undone.

When confirmed:

1. The invoice status changes to `void`
2. The order's payment status reverts to `pending_invoice`
3. You can create a new invoice from the order if needed

Invoices with recorded payments cannot be voided. If you need to cancel an invoice that has received partial payment, contact your system administrator.

---

## QuickBooks Sync

If QuickBooks Online is connected, the system automatically syncs wholesale invoicing data:

- **Customer sync** -- when a wholesale application is approved, a corresponding QB customer record is created
- **Invoice sync** -- when a wholesale order is placed, a background job creates a matching invoice in QuickBooks with the order's line items, shipping, and due date
- **Payment sync** -- when a payment is recorded against an invoice, a background job syncs the payment to QuickBooks with the amount, method, and reference

All QB sync operations run as background jobs. If a sync fails due to a temporary issue (network error, rate limit), the job retries automatically. Permanent failures (invalid data, missing QB customer) are logged and the job is cancelled.

The order record stores the QB invoice ID and document number for cross-referencing.

---

## Related Pages

- [Customers](customers.md) -- customer detail, billing settings, payment terms
- [Wholesale Management](wholesale.md) -- application review, groups, and pricing
