# Fulfillment -- Admin Guide

This guide covers the fulfillment workflow: packing orders, creating shipping labels, and marking orders as shipped.

## Fulfillment Queue

Navigate to **Fulfillment** in the admin sidebar (`/admin/fulfillment`).

The Fulfillment page is the **pack-and-ship workspace** — it filters orders by their *fulfillment* state (warehouse dimension) rather than order status (billing/lifecycle dimension), and it carries the state-advancing bulk verbs the [Orders](orders.md) page does not: buy labels, mark ready for pickup, mark picked up, out for delivery. The header shows a one-line queue summary ("N need action · N ready to ship · N in transit") so you see the shape of the work before scanning rows. There is no "new order" button here — the queue itself is the action.

### Tab Filters

Each tab shows a live count; the active tab is "stamped" (ink border + offset shadow).

| Tab | What it shows |
|-----|---------------|
| **Needs action** | Orders that still need packing or shipping: unfulfilled, partially fulfilled, fulfilled, or ready-for-pickup. Default view and primary working queue. |
| **Ready to ship** | Orders that have been packed (fulfilled) but not yet handed off. |
| **Shipped** | Orders handed to a carrier (shipped / partially shipped). |
| **Delivered** | Orders confirmed delivered (delivered / partially delivered). |
| **All** | Every order regardless of fulfillment status (still excludes unconfirmed intents). |
| **Load list** | How many pounds of each coffee the delivery van needs. See [Load List](#load-list-loading-the-delivery-van) below. |

### Two View Modes

**Action views** (Needs action, Ready to ship) render as a **grouped workspace**, not a flat list. Rows are split into sections by shipping method — *Ship via carrier*, *Local delivery*, *Pickup*, *No shipping method* — because each method needs a different verb. Each section has its own checkboxes and its own bulk action bar. Columns per section: checkbox · **Order** (number, Wholesale tag, customer name + email) · **State** (fulfillment badge, plus a red **Label failed** badge if a label purchase failed) · **Placed** (relative date; stale 48h+ rows flag rust). Up to 100 orders per page.

The bulk verbs available depend on the section's shipping method:

| Section | Bulk verbs |
|---------|-----------|
| **Ship via carrier** | Print packing slips · Print invoices · pick a carrier service and **Buy labels** (charges your Shippo account) |
| **Pickup** | Print packing slips · **Mark ready for pickup** (emails the customer) · **Mark picked up** |
| **Local delivery** | Print packing slips · **Out for delivery** (emails the customer) |
| **No shipping method** | Print packing slips · Print invoices |

After a bulk verb runs, a banner at the top of the page summarizes the result — how many succeeded, and a linked per-order list of any that were skipped (with the reason) so you can jump in and fix them.

**Flat views** (Shipped, Delivered, All) are post-handoff lookup only — a single paginated table, no checkboxes or bulk bar, with a **Ship via** column (since the method grouping is gone). 25 per page.

### Pagination

Action views show up to 100 grouped orders per page; flat lookup views show 25 per page. Both use Previous/Next navigation.

---

## Fulfillment Statuses

| Status | Meaning |
|--------|---------|
| **unfulfilled** | No items have been packed yet |
| **partially_fulfilled** | Some items packed, others still pending |
| **fulfilled** | All items packed and ready to ship |
| **partially_shipped** | Some items shipped, others still at the warehouse |
| **shipped** | All items handed to the carrier |
| **partially_delivered** | Some items delivered, others still in transit |
| **delivered** | All items confirmed delivered |
| **returned** | Order returned by the customer |

---

## Daily Fulfillment Workflow

For volume, work straight from the grouped queue: select a batch within a shipping-method section and use that section's bulk verb (Buy labels, Mark ready for pickup, Mark picked up, Out for delivery). The per-order steps below describe the same transitions from an individual order's detail page — useful for one-offs or when you need to review an order before acting.

The typical order fulfillment process follows these steps:

### 1. Review the Queue

Open the Fulfillment page. The **Needs Action** tab shows all orders requiring attention. Orders appear here when payment has been captured and they have not yet been shipped.

### 2. Open an Order

Click an order number to go to its detail page. Review:

- The line items (what to pack)
- The shipping address (where to send it)
- The shipping method (pickup, local delivery, or shipped)
- Any customer notes

### 3. Print a Packing Slip

Click **Packing Slip** in the order header to open a printable packing slip in a new tab. The browser print dialog opens automatically. Include this slip in the package.

### 4. Mark as Fulfilled

Once items are packed, click **Fulfill Order** on the order detail page.

**Requirements for fulfilling:**
- Payment status must be **captured**
- Fulfillment status must be **unfulfilled**
- Order must not be cancelled or refunded

**What happens:**
- Fulfillment status changes to `fulfilled`
- Order status changes to `processing`
- An audit record is created
- The progress bar advances to step 2 (Fulfilled)

### 5. Buy a Shipping Label

For orders with shipping method "Shipped", buy a label directly from the order detail page. There is **no dimensions form** — you only pick a carrier service; weight, box size, and addresses are all derived server-side.

**To buy a label:**

In the "Buy Label" control, choose a service from the dropdown and submit (`POST /admin/orders/{id}/label`):

- **USPS Ground Advantage** (default)
- **USPS Priority**
- **USPS Priority Express**

That single `service_code` is the only label-shaping input you provide. The system derives the rest:

- **Weight** — summed from each physical line item's variant weight plus a configured tare (packaging) weight.
- **Box dimensions** — chosen automatically from your configured box presets based on the computed weight. (If no box preset is configured, the label fails with a non-retryable error — set up presets in shipping settings first.)
- **Ship-from address** — from your shipping configuration, not the form.
- **Ship-to address** — from the order's shipping address.

Buying a label is **asynchronous**. Submitting enqueues a background job and immediately returns with a "Label queued" flash; the job calls the shipping provider, then a second job stores the label PDF in cloud storage (R2). While the worker runs (typically 1–3s) the order page **refreshes itself automatically** — the shipment row, tracking number, and download link appear on their own once the label is bought, no manual reload needed. (The auto-refresh gives up after ~20s; if a label is still queued past that, refresh manually.) If the purchase fails, the button changes to **Retry Buy Label**.

Pickup and Local Delivery orders don't get this control — no label is needed.

### 6. Download a Shipping Label

Once a label has been bought and stored, a **Label ({tracking number})** link appears for that shipment on the order detail page (`GET /admin/shipments/{shipment_id}/label`). Clicking it generates a temporary signed URL and redirects your browser to download the label PDF. The signed URL expires after 5 minutes.

### 7. Mark as Shipped

After attaching the shipping label and handing the package to the carrier, click **Mark Shipped** on the order detail page (`POST /admin/orders/{id}/ship`). This button appears only for carrier ("Shipped") orders — pickup and local-delivery orders use their own verbs instead (see [Non-Shipped Orders](#fulfillment-for-non-shipped-orders) below).

**Requirements for marking shipped:**
- Fulfillment status must be **fulfilled**
- Order must not be cancelled or refunded

**What happens:**
- Fulfillment status changes to `shipped`
- Order status changes to `complete`
- An audit record is created
- The progress bar advances to step 3 (Shipped)
- The order moves from the "Needs action" tab to the "Shipped" tab in the fulfillment queue

> Made a mistake? The detail page also offers **Revert Fulfillment** and **Revert Shipment** to step an order back when a transition was applied in error.

---

## Fulfillment for Non-Shipped Orders

Pickup and local-delivery orders don't use "Mark Shipped" or shipping labels. They have their own verbs, available both as single-order buttons on the detail page (gated by the order's shipping method) and as bulk verbs in the matching section of the fulfillment queue.

### Pickup Orders

For orders with shipping method "Pickup":
- No shipping label is needed.
- **Mark Ready for Pickup** — sets fulfillment to "ready for pickup" and emails the customer that their order is ready.
- **Mark Picked Up** — records that the customer has collected the order (available once it's ready for pickup).

### Local Delivery Orders

For orders with shipping method "Local Delivery":
- No shipping label is needed.
- **Out for Local Delivery** — emails the customer a delivery notification and marks the order out for delivery.

---

## Load List — Loading the Delivery Van

The **Load list** page answers one question before the van pulls out: *do we have enough of each coffee on board to fill every delivery order?*

Without it, that check meant opening each delivery order and adding bags up by hand. The page does the arithmetic for the whole run.

### What it shows

**On board** — one row per coffee, with the bag count and the total pounds, heaviest first, and a grand total at the bottom. That total is the number to check the van against.

**Orders on this run** — the delivery orders those totals cover. Everything waiting in the local-delivery queue starts checked, with a **Retail** or **Wholesale** badge on each stop so you can see the mix you're loading.

**Both channels by default.** The load list lives at `/admin/fulfillment/load-list` and covers retail *and* wholesale deliveries together, because one van makes one run. The **All · Retail · Wholesale** switch at the top narrows it for the days when only one channel is going out — wholesale today, retail tomorrow. Narrowing carries through to the print sheet and to **Plan route**, so the van, the paper, and the driver's stop list can't disagree.

Orders that are cancelled, refunded, or not yet paid never appear.

### Using it

1. Open **Fulfillment → Load list** once the day's delivery orders are packed.
2. Uncheck anything that isn't going out today — an order on hold, a reschedule, a second run. The totals update as you check and uncheck.
3. Read the pound totals and confirm the van matches.
4. Click **Print load sheet** for a paper copy to carry: the same totals with a tick box beside each coffee, plus the list of stops. The print dialog opens on its own.

Unchecking only affects the load list — it doesn't change the order, cancel anything, or notify the customer. It's just a way to say "this one isn't on this run." Reload the page and everything is checked again.

### Pounds are coffee, not freight

The totals come from each variant's weight in the catalog: a 12 oz bag counts as 0.75 lb, a 5 lb bag as 5 lb. That's the weight of the coffee itself, not the packed box — the right number for "did we roast and bag enough," which is what this check is for.

If a coffee shows an amber **"N bag(s) unweighed"** badge, some of its bags have no weight set in the catalog and are **not** included in the pound total — the real load is heavier than shown. Fix the variant's weight in [Catalog](catalog.md) so the total can be trusted.

### This is a check, not a handoff

The load list doesn't change any order's state. Once the van is loaded and rolling, go back to the **Needs action** tab, select the delivery orders, and use **Out for delivery** to notify customers and advance the orders.

---

## Route Planning — What Order to Drive In

The load list says *what goes in the van*. **Plan route**, on the same page, says *what order to drive it in*, and hands the result to the driver's phone.

This replaces exporting addresses into a third-party routing app by hand. The stop order is worked out on our own server; the driver's phone still does turn-by-turn in Google or Apple Maps.

### Planning

1. On the **Load list** page, check the orders going out — the same checkboxes that drive the pound totals.
2. Set **End of run** if the van isn't coming back to the shop (see below). Leave it blank most days.
3. Click **Plan route**. It takes a few seconds: each address is placed on the map, then the driving order is worked out.
4. You land on the route's review page.

The route covers exactly what the load list is showing. Left on **All**, that means **both retail and wholesale** together — one van makes one run, so a cafe and a house on the same street end up next to each other rather than on two routes that cross. Narrow the scope first if only one channel is going out.

If you uncheck every order and click **Plan route**, nothing is planned and you land back on the load list.

### Where the run ends

By default a route is a loop: out from the roastery, round the stops, back to the roastery. That's the Monday shape.

On the runs where the driver takes the van home afterwards, type their address into **End of run** above the totals before planning. The route is then worked out to finish at the stop nearest that address instead of doubling back across town, and the drive time and distance shown are for the one-way run — no return leg nobody drives.

A few things worth knowing:

- **Nothing is delivered there.** The address is a finishing point for the optimizer, not a stop. It never appears on the stop list and the driver is never asked to mark it.
- **The route page says which it is** — "Ends back at the roastery" or "Ends at *address*" — so nobody has to guess why the stop order looks unusual.
- **Remove & re-plan keeps it.** Dropping a stop won't quietly turn a home run back into a loop.
- **A bad address stops the plan** rather than falling back to the roastery, because a route optimized to end on the wrong side of town is worse than no route. Check the spelling, or clear the field.

### Reviewing

The review page shows the stop order, total drive time and distance, and each stop's customer, address, and delivery notes.

Two things to look for:

- **"N delivery order(s) could not be placed on the map."** Those orders are **not** on the route and will not be delivered. Almost always a bad address — fix it on the order, then plan again.
- **Remove & re-plan** on any stop drops it and works out the best order for what's left. The order itself is untouched: it stays in the delivery queue and turns up on the next run.

Re-planning replaces the draft. There's only ever one route per delivery day, so there's no way to hand a driver an out-of-date sheet by accident.

### Handing it to the driver

Click **Activate & get driver link**. That produces a **QR code**.

The driver scans it with their phone camera — no app, no login, no typing. It opens their stop list, which shows:

- The next stop, large, at the top
- **Google Maps** and **Apple Maps** buttons per stop
- **Navigate stops 1–10** links for the whole run (Google Maps holds ten stops per link, so longer runs are split into legs to work through in order)
- **Delivered** and **Skip this stop** buttons

Print the QR if the driver would rather carry paper.

### While the run is going

**Delivered** marks the order delivered and complete in Hiri straight away — you'll see it move in the fulfillment queue while the van is still out.

**Skip this stop** asks for a short reason ("wrong address", "nobody home"). A skip is about *today's run only*: the order isn't touched, stays in the delivery queue, and appears on the next route automatically. The reason shows on the route so you can fix whatever caused it before the next van goes out.

Once every stop is delivered or skipped, the route completes on its own and the driver's link stops working. You can also end it early with **End route**.

### If planning won't run

| Message | What to do |
|---------|-----------|
| "A route for this delivery day is already out with a driver" | The route is active. End it before planning a new one — this stops a driver's stop list being swapped mid-run. |
| "No local delivery orders are waiting to be routed" | Nothing is checked, or the queue is empty. |
| "Set the roastery address in shipping settings" | The route starts from the shop. Fill in the origin address under [Settings](settings.md). |
| "The end-of-run address could not be placed on the map" | The **End of run** address wasn't recognised. Check the spelling, or leave it blank to finish at the roastery. |
| "Address lookup is temporarily unavailable" | The address service is having a moment. Try again shortly; nothing was lost. |

---

## Quick Reference

| Action | Button / control | Result |
|--------|------------------|--------|
| Pack an order | Fulfill Order | Status: processing, Fulfillment: fulfilled |
| Ship a carrier order | Mark Shipped | Status: complete, Fulfillment: shipped |
| Buy a label | Service dropdown + Buy Label | Enqueues a background job; shipment + label appear on next render. No dimensions form — weight/box/addresses are derived |
| Download a label | Label ({tracking}) link on shipment | Temporary signed URL (5 min), auto-downloads PDF |
| Ready a pickup order | Mark Ready for Pickup | Fulfillment: ready for pickup; customer emailed |
| Complete a pickup | Mark Picked Up | Customer has collected the order |
| Hand off a local delivery | Out for Local Delivery | Out for delivery; customer emailed |
| Undo a transition | Revert Fulfillment / Revert Shipment | Steps the order back one stage |
| Print packing slip | Packing Slip button | Opens printable page in new tab |
| Check the delivery load | Fulfillment → **Load list** | Pounds of each coffee needed for the run; uncheck orders that aren't going today |
| Carry the load list | **Print load sheet** | Printable totals + stop list, with tick boxes |
| Finish somewhere else | **End of run** field, then Plan route | Route ends at that address instead of the roastery; no return leg |
