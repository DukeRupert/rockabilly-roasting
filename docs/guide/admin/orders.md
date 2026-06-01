# Orders -- Admin Guide

This guide covers how to view, manage, and act on customer orders in the admin panel.

## Order List Page

Navigate to **Orders** in the admin sidebar (`/admin/orders`).

The Orders page is the record-and-lookup surface for **every** order: search it, scan totals and payment state, and manage the order lifecycle. The physical pack-and-ship work lives on the separate [Fulfillment](fulfillment.md) page — including buying shipping labels in bulk. An order can legitimately appear on both pages at once; that's by design (one tracks billing/lifecycle, the other tracks the warehouse).

### Columns

The order table displays:

| Column | Description |
|--------|-------------|
| **Order** | Order number (e.g., `RR-10042`), a **Wholesale** tag for B2B accounts, and the customer's name and email. Click anywhere in the row to open the order detail page. |
| **State** | A single status badge. For cancelled, refunded, on-hold, and pending orders it shows the **order status**; otherwise it shows the **fulfillment status** (the actionable signal). A red **Label failed** badge appears if the most recent label purchase failed. |
| **Shipping** | Shipping method: Pickup, Local Delivery, or Shipped. Hidden below large screens. |
| **Total** | Order total plus a payment-status chip (paid, awaiting, refunded, etc.). |
| **Placed** | Relative date the order was placed ("Today 3:04 PM", "Yesterday", "3d ago"). Non-terminal orders sitting 48h+ turn rust with a flag on the row's leading edge. Hidden on small screens. |

### Filtering

Use the tab bar above the table to filter orders into workflow buckets (these group several raw statuses, they are not one-tab-per-status). Each tab shows a live count, and the active tab is "stamped" (ink border + offset shadow) rather than underlined.

- **Open** (default) -- orders that need attention: confirmed or processing, excluding unconfirmed intents
- **On hold** -- orders manually paused (stock issue, customer request)
- **Shipped** -- completed orders
- **Archive** -- cancelled or refunded orders
- **All** -- every order regardless of status

Filters and search are preserved across page navigation.

### Search

The search box (to the right of the tabs on desktop, above them on mobile) accepts order numbers, customer names, or email addresses. Results appear as you type (debounced 300ms). The search query is preserved when switching tabs.

### Bulk Actions

Tick the checkboxes on one or more rows (or the header checkbox to select the whole page) and an action bar slides in:

- **Print invoices** -- opens a print-ready invoice for each selected order in a new tab.

Buying shipping labels in bulk is **not** here — it lives on the [Fulfillment](fulfillment.md) page, grouped by shipping method. The Orders bulk bar is for documents and lookup only.

### Pagination

Orders display 25 per page. Use the Previous/Next links at the bottom to navigate. The current range (e.g., "1--25 of 142") is shown between the pagination buttons.

---

## Order Detail Page

Click any order number to open its detail view (`/admin/orders/{id}`).

### Header

Shows the order number, date placed, and a status badge if the order is cancelled or refunded. Action links appear in the top-right:

- **Print Invoice** -- opens a printable invoice in a new tab
- **Packing Slip** -- opens a printable packing slip in a new tab
- **Back to orders** -- returns to the order list

### Progress Bar

A three-step progress indicator tracks the order lifecycle:

1. **Paid** -- payment has been captured
2. **Fulfilled** -- items have been packed
3. **Shipped** -- order has been handed to the carrier

Completed steps show a red checkmark. The progress bar is hidden for cancelled and refunded orders.

### Action Buttons

The detail page has a "Guided actions" block; which buttons appear depends on the order's state and shipping method. The shipping/fulfillment verbs are documented in full in the [Fulfillment guide](fulfillment.md) — this is the summary.

| Button | When it appears | What it does |
|--------|----------------|--------------|
| **Fulfill Order** | Payment captured, fulfillment is "unfulfilled", order not cancelled/refunded | Marks the order fulfilled and changes order status to "processing" |
| **Buy Label** / **Retry Buy Label** | Carrier ("Shipped") order, payment captured, no existing shipment/in-flight attempt | Pick a USPS service and enqueue a label-purchase job (no dimensions form; see Fulfillment guide). "Retry" shows after a failed attempt |
| **Mark Shipped** | Carrier order, fulfillment is "fulfilled", not cancelled/refunded | Marks the order shipped and changes order status to "complete" |
| **Mark Ready for Pickup** | Pickup order, unfulfilled or fulfilled | Sets fulfillment to "ready for pickup" and emails the customer |
| **Mark Picked Up** | Pickup order already marked ready for pickup | Records the customer collected the order |
| **Out for Local Delivery** | Local-delivery order, unfulfilled or fulfilled | Marks out for delivery and emails the customer |
| **Revert Fulfillment** / **Revert Shipment** | After the corresponding transition | Steps the order back one stage when a transition was applied in error |
| **Cancel Order** | Order status is "pending" or "confirmed" | Cancels the order (confirmation dialog appears first) |
| **Refund Order** | Order status is "confirmed" or "complete" AND payment is "captured" | Refunds the order (confirmation dialog appears first) |

### Info Cards

Three summary cards appear below the actions:

- **Total** -- the order total
- **Customer** -- customer name (links to their admin profile) and email
- **Ship to** -- the shipping address including company name and second line if present

### Shipping and Delivery Details

If applicable, a card shows:

- **Shipping method** (Pickup, Local Delivery, or Shipped)
- **Requested delivery date** (for wholesale/B2B orders)
- **PO number** (customer purchase order reference)

### Stripe Payment Intent

If the order was paid through Stripe, the Payment Intent ID is displayed in a muted info bar. Use this ID to look up the payment in the Stripe Dashboard.

### Line Items Table

Lists every item in the order:

| Column | Description |
|--------|-------------|
| **Product** | Product title |
| **SKU** | Variant SKU |
| **Qty** | Quantity ordered |
| **Unit Price** | Price per unit |
| **Total** | Line item total |

### Order Totals

A summary section breaks down:

- Subtotal
- Discounts (if any, shown as a negative amount)
- Shipping
- Tax
- **Total** (bold, separated by a divider)

### Adjustments

If the order has adjustments (e.g., manual price changes, coupon credits), they appear in a separate table showing the label, type, and amount.

### Notes

Customer-provided order notes are displayed at the bottom of the page if present.

---

## Order Statuses

Orders progress through a defined lifecycle. Not all transitions are available from every status.

| Status | Meaning |
|--------|---------|
| **pending** | Order placed, awaiting payment confirmation |
| **confirmed** | Payment confirmed, ready for fulfillment |
| **processing** | Order is being packed/fulfilled |
| **on_hold** | Order paused -- requires staff attention. This is a manually-set status for exceptional situations (e.g., stock issues, customer requests). There are no automated transitions into or out of this status. |
| **complete** | Order shipped and delivered |
| **cancelled** | Order cancelled before completion |
| **refunded** | Payment returned to the customer |

### Valid Transitions

- `pending` -> `confirmed` (automatic on payment capture)
- `pending` -> `cancelled` (manual)
- `confirmed` -> `processing` (triggered by "Fulfill Order")
- `confirmed` -> `cancelled` (manual)
- `confirmed` -> `refunded` (manual, requires payment captured)
- `processing` -> `complete` (triggered by "Mark Shipped")
- `complete` -> `refunded` (manual, requires payment captured)

Cancelled and refunded are terminal states. Once an order reaches either, no further status changes are possible through the admin UI.

---

## Payment Statuses

| Status | Meaning |
|--------|---------|
| **awaiting** | No payment activity yet |
| **authorized** | Payment authorized but not yet captured |
| **captured** | Payment successfully collected |
| **partial** | Partial payment received |
| **refunded** | Full refund issued |
| **failed** | Payment attempt failed |
| **voided** | Authorization voided before capture |
| **pending_invoice** | Invoice created but not yet sent (B2B) |
| **invoiced** | Invoice sent to customer (B2B) |
| **partially_paid** | Partial invoice payment received (B2B) |
| **overdue** | Invoice past due (B2B) |

---

## Cancelling an Order

### When cancellation is allowed

An order can only be cancelled when its status is **pending** or **confirmed**. Once an order has moved to processing, complete, or any terminal state, cancellation is no longer available.

### How to cancel

1. Open the order detail page.
2. Click **Cancel Order** (appears only when cancellation is allowed).
3. Confirm in the dialog that appears.
4. The order status changes to "cancelled" and a success toast appears.

### What happens

- The order status is set to `cancelled`.
- An audit record is created with the staff member's identity.
- The progress bar is hidden on the detail page.
- No further fulfillment or shipping actions are available.

Cancellation does **not** automatically issue a refund. If payment was captured, you must separately refund the order.

---

## Refunding an Order

### When refund is allowed

An order can be refunded when:

- The order status is **confirmed** or **complete**, AND
- The payment status is **captured**

Orders that are pending, cancelled, already refunded, or not yet paid cannot be refunded through this action.

### How to refund

1. Open the order detail page.
2. Click **Refund Order** (appears only when refund is allowed).
3. Confirm in the dialog that appears.
4. The order status changes to "refunded" and the payment status changes to "refunded".

### What happens

- The order status is set to `refunded`.
- The payment status is set to `refunded`.
- An audit record is created.
- The progress bar is hidden on the detail page.
- No further actions are available on the order.

**Important:** You must first process the refund in the **Stripe Dashboard** before clicking "Refund Order" in the admin panel. The "Refund Order" action records the refund in the system but does not initiate the Stripe refund itself. Process the refund in Stripe first, then click "Refund Order" here to update the order and payment status.

---

## Packing Slips

Packing slips are printable documents included with shipped orders.

### How to print a packing slip

1. Open the order detail page.
2. Click the **Packing Slip** button in the top-right header area.
3. A new browser tab opens with the packing slip, and the print dialog appears automatically.

### What the packing slip contains

- Company name and order number/date in the header
- **Ship To** address
- **Customer** name and email
- Line items table with product name, SKU, quantity, unit price, and total
- Order totals breakdown (subtotal, discounts, shipping, tax, total)
- Order notes (if any)

The packing slip uses a clean, print-optimized layout with no color or branding elements beyond the company name.
