# Admin Dashboard

The dashboard is the first page you see after logging in at `/admin/`. It gives a real-time snapshot of today's activity, highlights items that need your attention, and lists recent orders.

## KPI Strip

The top of the dashboard shows five metrics, all scoped to **today (UTC)**:

| Metric | What it means |
|---|---|
| **Revenue** | Total dollar amount of orders placed today. |
| **Orders** | Number of orders placed today. |
| **To Pack** | Orders that have been paid but not yet fulfilled (items picked and packed). Turns red when greater than zero. |
| **To Ship** | Orders that have been packed but not yet handed off to a carrier. Turns red when greater than zero. |
| **Active Subs** | Total number of active subscriptions across all customers. Visible on wider screens only. |

"To Pack" and "To Ship" are not limited to today -- they reflect all outstanding orders across any time period. If either number is non-zero, it is displayed in red to draw your attention.

## Needs Attention

Below the KPI strip is the action queue. It lists individual items that require staff action, grouped into four categories. If nothing needs attention, this section shows a teal checkmark and the message "All caught up."

### Orders to Fulfill (red dot, FULFILL label)

Each row is a paid order whose items have not been packed yet. These are orders where payment has been captured, the fulfillment status is "unfulfilled," and the order has not been cancelled or refunded.

**What to do:** Click the order number to open the order detail page. From there, pick and pack the items, then mark the order as fulfilled.

### Orders to Ship (teal dot, SHIP label)

Each row is an order that has been packed (fulfillment complete) but has not been shipped yet. Cancelled and refunded orders are excluded.

**What to do:** Click the order number to open the order detail page. Purchase a shipping label, hand the package to the carrier, and mark the order as shipped.

### Pending Wholesale Applications (amber dot, REVIEW label)

Shows the count of wholesale (B2B) customer applications waiting for approval. This row only appears when there are pending applications.

**What to do:** Click the row to go to the wholesale management page at `/admin/wholesale`, where you can review and approve or reject each application.

### Past-Due Subscriptions (red dot, REVIEW label)

Shows the count of subscriptions in a "past due" state, meaning a renewal payment has failed. This row only appears when past-due subscriptions exist.

**What to do:** Click the row to go to the subscriptions page at `/admin/subscriptions`, where you can investigate failed payments and take action (retry payment, contact the customer, or cancel the subscription).

## Recent Orders

The bottom section shows the **10 most recent orders** regardless of status, displayed in a table with the following columns:

| Column | Description |
|---|---|
| **Order** | The order number (e.g., RR-1042). Click it to open the order detail page. |
| **Status** | The order's lifecycle status (e.g., open, completed, cancelled, refunded). Shown as a colored badge. |
| **Fulfillment** | The fulfillment status (e.g., unfulfilled, fulfilled, shipped). Shown as a colored badge. |
| **Total** | The order total in dollars. |
| **Date** | When the order was placed (e.g., "Mar 16, 2:30 PM"). Visible on wider screens only. |

Click **View all** in the section header to navigate to the full orders list at `/admin/orders`.

If there are no orders yet, this section displays a placeholder message with a link to view the storefront.

## Tips

- **Check the dashboard at the start of each day.** The "Needs Attention" section is designed to be your to-do list. When all items are cleared, you will see the "All caught up" message.
- **Work top to bottom.** Fulfill orders first (pack them), then ship them. This matches the natural order flow.
- **Red means action required.** Any metric or dot shown in red indicates something that should not stay in that state for long.
- **The dashboard auto-refreshes via htmx** when navigated to from the admin sidebar, so you always see current data without a full page reload.
