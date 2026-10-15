# Subscriptions — Admin Guide

This guide covers managing subscription plans and customer subscriptions from the admin panel.

## Subscription Plans

Subscription plans define the recurring delivery cadences available to customers. Plans are global — once active, they appear as options on all subscribable products.

### Creating a Plan

Navigate to **Subscription Plans** in the admin sidebar. The "New Plan" form is at the top of the page.

Fill in the following fields:

| Field | Description |
|-------|-------------|
| **Name** | A human-readable label (e.g. "Every 30 Days"). Shown to customers and staff. |
| **Interval** | The delivery cadence. Options: Every 7 Days, Every 14 Days, Every 21 Days, Every 30 Days, Every 60 Days, Every 90 Days. |
| **Discount %** | A percentage discount applied to the product price for subscribers. Enter 0 for no discount, up to 100. |

Click **Create Plan** to save. New plans are created in an active state by default.

### Plan Table Columns

The plan list table shows:

- **Name** — the plan's display name.
- **Interval** — the delivery frequency.
- **Discount** — an inline-editable field. Change the percentage and click **Save** to update it immediately.
- **Status** — Active or Inactive badge.
- **Actions** — Activate or Deactivate toggle.

### Activating and Deactivating Plans

- Click **Deactivate** on an active plan to hide it from customers. Existing subscriptions on that plan continue to renew normally.
- Click **Activate** on an inactive plan to make it available for new subscriptions again.

Deactivating a plan does not cancel or pause any subscriptions already using it. It only prevents new customers from selecting it.

## Several Coffees on One Subscription

Customers can now put more than one coffee on a subscription in a single checkout. The **Subscribe** page lets them add items, and each item can have its own plan.

**Each coffee is its own subscription.** A signup with three coffees creates three subscriptions, one per coffee, each with its own row in the Subscriptions list and its own detail page. Pause, skip, cancel, plan changes and size/grind changes all work on one coffee at a time from the admin, exactly as before.

**Coffees that renew on the same day ship together.** When a customer has several subscriptions going to the same address on the same renewal run, they renew as **one order, one charge and one shipping fee**. Customers see these grouped as a "shipment" on their account page. Nothing groups them except the shared date and address: if one coffee is skipped or paused, it moves out of the shipment and the others stay where they were.

- **Same plan:** coffees signed up together on one plan renew together every time.
- **Different plans:** a weekly and a monthly ship together on the first order only. After that each plan renews on its own day, with its own shipping charge. The Subscribe page tells the customer this before they pay.

### What the customer can do themselves

On their account page, a customer can:

- **Skip this shipment** -- move every coffee in a shipment to a new date together, so they keep shipping as one.
- **Pause all** -- pause every coffee in a shipment.
- Skip, pause, resume or cancel a single coffee.
- Update the card on file.

Changing size, grind or schedule still goes through you: the page tells customers to get in touch for those.

If one coffee in a shipment is past due, the customer can't skip or pause the whole shipment until the payment is sorted.

### Finding the subscriptions behind an order

On an order's detail page, the side panel links to its subscriptions. An order for one subscription shows **View plan**. An order that covers several shows one link per coffee: **Plan 1 of 3**, **Plan 2 of 3**, and so on.

## Subscription List

Navigate to **Subscriptions** in the admin sidebar to see all customer subscriptions.

### Table Columns

| Column | Description |
|--------|-------------|
| **ID** | The subscription ID (truncated). Click to open the detail view. |
| **Status** | Color-coded status badge (Active, Paused, Past Due, Cancelled, Expired). |
| **Next Order** | The date the next renewal order will be placed. |
| **Created** | When the subscription was created. |

### Filtering

Use the **status dropdown** at the top to filter by:

- All statuses (default)
- Active
- Paused
- Past Due
- Cancelled
- Expired

The filter applies immediately when you change the selection. Pagination controls appear at the bottom when there are more than 25 results.

## Subscription Detail

Click any subscription ID to open its detail page. The header shows the plan name, customer name, and current status badge.

### Information Cards

The detail view displays these summary cards:

- **Plan** — the plan name and interval.
- **Quantity** — number of units per renewal.
- **Customer** — name and email, linked to the customer detail page.
- **Current Period** — the start and end dates of the current billing cycle.
- **Next Order** — the date the next renewal will be placed.

If the subscription has been cancelled, the cancellation timestamp is shown. If it is paused with a resume date, the "paused until" date is displayed.

### Skipping Shipments

Active subscriptions get a **Skip shipments** row in the Actions panel, for when a customer calls in rather than doing it themselves.

- **Skip the next N shipments** -- pick a count (up to 6) and click **Skip**. The schedule advances by that many whole billing cycles.
- **Or restart on a date** -- pick a day and click **Set date**. The next order lands on that day and the normal cadence resumes from there. The picker runs from the day after the order already scheduled through the 60-day ceiling; if the next order is already past that ceiling the date option is hidden and only the shipment count applies.

Skipping never charges the customer and never creates an order for the skipped window; it only moves the next order date. The subscription stays active, keeps its plan, variant and quantity, and every skip is recorded in the audit log. For an open-ended break, pause instead.

**The customer is emailed either way.** Every skip -- yours or theirs -- sends a notice confirming what was skipped, when the next shipment bills, and a one-click link to undo it. The link is signed with `ORDER_ACTION_SECRET`, falling back to `UNSUBSCRIBE_SECRET`, and then to a key derived from `APP_SECRET`; with none of the three the email still sends and asks the customer to reply instead.

**Undoing notifies the customer.** An undo moves the charge date *earlier* than the last message told them, so a staff-initiated undo emails them the old and new dates. A customer who undoes it themselves gets nothing extra -- they saw it confirmed on screen.

**Undoing.** While a skip can still be cleanly reversed, an **Undo skip** bar appears in that row showing the date it would restore. Undo puts back the exact schedule the skip replaced. It disappears once that original date has passed or the schedule moves for another reason (renewal, resume, plan change, or a second skip) -- at that point there is no single skip left to reverse, and the dates have to be set by hand.

### Renewal Orders

A table at the bottom lists all orders generated by this subscription. Each row shows:

- **Order** — the order number, linked to the order detail page.
- **Period Start** — the beginning of the billing period that order covers.
- **Period End** — the end of that billing period.

## Subscription Status Lifecycle

A subscription moves through these statuses:

```
active --> paused --> active (via resume)
active --> cancelled
active --> past_due --> active (on successful retry)
active --> past_due --> expired (payment never recovered)
paused --> cancelled
past_due --> cancelled
```

### Status Definitions

| Status | Meaning |
|--------|---------|
| **Active** | The subscription is running normally. Renewal orders are placed automatically on schedule. |
| **Paused** | The subscription is temporarily suspended. No renewal orders are created. The customer is not charged. |
| **Past Due** | A renewal payment failed (e.g. expired card, insufficient funds). The system retries and emails the customer on its own; see "Payment Failures" below. |
| **Cancelled** | The subscription is permanently stopped. No further renewals will occur. This cannot be undone. |
| **Expired** | The payment never recovered, so the subscription ended after its last retry. The customer is emailed when it ends. Like Cancelled, it does not restart; the customer signs up again. |

## Staff Actions

Everything you can *do* to a subscription lives in one **Actions** panel near the top of the detail page. The reversible lifecycle buttons — Pause, Resume, Retry payment, Cancel — sit in its header, with Cancel ruled off to the right because it's the one that can't be taken back. The settings that need a sentence of explanation (**Skip shipments**, **Renewal shipping**) are rows underneath. What's offered depends on the current status; a cancelled subscription shows no panel at all.

Changing the **plan** and changing the **size/grind** are not in this panel — they sit next to the values they change, on the plan card and the product line.

### Pause

Available when status is **Active**. Pausing a subscription:

- Stops all future renewals until it is resumed.
- Does not affect any orders already placed.
- Does not refund any previous charges.

Use this when a customer asks to skip deliveries temporarily.

### Resume

Available when status is **Paused**. Resuming a subscription:

- Sets the status back to **Active**.
- Books the next order at the **next renewal window** — the next pre-dawn run, never a second one — not a full interval out. Resuming means the customer wants coffee, so they get it on the next run rather than waiting a whole cycle. The normal cadence then continues from that order.
- Clears any "pause until" date.

Nothing is charged at the moment you press Resume; the charge happens when the renewal runs. The customer is emailed either way — your resume as well as theirs — and the mail names the booked date.

**Go by the Next Order field, not the date in the dialog.** The confirmation dialog names the window as of the moment the page was *rendered*. If the page has been open across the renewal hour — an overnight tab is the usual way — the run it named has already gone, and the resume books the next one.

After you press Resume, the green toast reports what was actually booked ("Subscription resumed — next order Friday, September 11"). That toast fades after a few seconds and does not survive a page refresh, so treat it as a confirmation rather than a record: the **Next Order** panel at the top of the page shows the same date, in shop time, for as long as you need it. Reload the page before resuming if you want the dialog and the booking to agree.

### Cancel

Available when status is **Active**, **Paused**, or **Past Due**. Cancelling a subscription:

- Permanently ends the subscription.
- Records a cancellation timestamp.
- A confirmation prompt appears before the action is taken.

Cancellation cannot be reversed. If a customer wants to resubscribe, they must create a new subscription.

## How Renewals Work

Renewals are fully automatic and run in the background.

### The Renewal Process

1. A **renewal scheduler** job runs periodically and scans for active subscriptions whose `next_order_at` date has arrived or passed.
2. The scheduler groups due subscriptions by customer and shipping address. If a customer has multiple subscriptions shipping to the same address, they are batched into a single order.
3. For each group, a **batch renewal job** is enqueued.
4. The renewal job:
   - Loads the subscription, plan, customer, and pricing data.
   - Applies the plan's discount percentage to the product price.
   - Creates an off-session Stripe PaymentIntent using the customer's saved payment method.
   - Creates an order with status "Confirmed" and payment status "Captured".
   - Links the order to the subscription(s).
   - Advances the billing period and sets the next renewal date.

### Payment Failures

If a renewal payment fails (declined card, no saved card, etc.), the subscription moves to **Past Due** and the shop works through a 14-day recovery schedule on its own:

| When | What happens |
|------|--------------|
| Day 0 | The charge fails. The customer is emailed with a one-click link to update their card. |
| Day 3 | Retry. If it fails, no email (this one is deliberately quiet). |
| Day 8 | Retry. If it fails, a reminder email. |
| Day 12 | Retry. If it fails, a final-notice email. |
| Day 14 | Last retry. If it fails, the subscription becomes **Expired** and the customer is emailed that it has ended. |

Any successful retry puts the subscription back to **Active** and ships the order. The subscription's detail page shows a **Payment recovery** panel with where it is on this schedule, and **Retry payment now** queues a charge straight away, useful when a customer calls to say they've added a new card. It can be pressed once a day; a second press the same day says the attempt is already queued. A retry never charges a card the bank has blocked (below).

**One email per failed charge.** When a shipment of several coffees fails, the customer gets one email naming every coffee in it, not one email per coffee. Each coffee still keeps its own place on the schedule above.

**The card link covers the whole account.** The customer's card belongs to their account, not to one coffee, so updating it fixes every subscription they have. The link in the email keeps working as long as any of their subscriptions is past due, even if they have since paused or cancelled the coffee the email was about.

**Cards the bank has blocked for good.** If the bank says a card is lost, stolen or closed, the shop never charges that card again, on any of the customer's subscriptions. The Payment recovery panel says so. If the customer has another card on file, renewals use it; if not, nothing more is charged until they add a new card, and the customer is still emailed so they know to do it.

Jobs for subscriptions that no longer exist or are already cancelled are permanently discarded and will not retry.

### Subscription Orders

Each renewal creates a standard order in the system with an order number prefixed `SUB-`. These orders:

- Appear in the regular Orders list and can be fulfilled like any other order.
- Are linked back to the subscription via the Renewal Orders table on the subscription detail page.
- Track which billing period they cover (period start and end dates).

When multiple subscriptions are batched into one order, each subscription gets its own line item, and all subscriptions are linked to that single order (see "Finding the subscriptions behind an order" above). A customer's first signup works the same way: one order, one shipping charge, however many coffees are on it.
