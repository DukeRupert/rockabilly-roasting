# Fulfillment -- Admin Guide

This guide covers the fulfillment workflow: packing orders, creating shipping labels, and marking orders as shipped.

## Fulfillment Queue

Navigate to **Fulfillment** in the admin sidebar (`/admin/fulfillment`).

The fulfillment page is a focused view of orders filtered by their fulfillment state. It is separate from the main Orders page and designed for the daily pack-and-ship workflow.

### Tab Filters

| Tab | What it shows |
|-----|---------------|
| **Needs Action** | Orders that are unfulfilled, partially fulfilled, or fulfilled but not yet shipped. This is the default view and represents your working queue. |
| **Unfulfilled** | Orders with fulfillment status "unfulfilled" only. |
| **Ready to Ship** | Orders that have been fulfilled (packed) but not yet shipped. |
| **Shipped** | Orders that have been handed to a carrier. |
| **Delivered** | Orders confirmed as delivered. |
| **All** | Every order regardless of fulfillment status. |

### Table Columns

The fulfillment table is streamlined compared to the order list:

| Column | Description |
|--------|-------------|
| **Order** | Order number. Click to open the order detail page. |
| **Fulfillment** | Current fulfillment status badge. |
| **Placed** | Date the order was placed. Hidden on small screens. |

### Pagination

Results display 25 per page with Previous/Next navigation.

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

### 5. Create a Shipping Label

For orders with shipping method "Shipped", you can create a shipping label directly from the admin panel.

**To create a label:**

The label creation form (`POST /admin/orders/{id}/label`) requires:

- **Package dimensions:** weight (oz), length, width, height (inches)
- **Service code:** the carrier service level (e.g., USPS Priority Mail)
- **From address:** your warehouse/shop address
- **To address:** the customer's shipping address (pre-filled from the order)

After submission:
- The shipping label is created through the external shipping provider
- A background job stores the label PDF in cloud storage (R2)
- You are redirected back to the order detail page

### 6. Download a Shipping Label

Once a label has been created and stored, you can download it.

**To download:**

Visit `/admin/shipments/{shipment_id}/label`. This generates a temporary signed URL and redirects your browser to download the label PDF. The signed URL expires after 5 minutes.

### 7. Mark as Shipped

After attaching the shipping label and handing the package to the carrier, click **Mark Shipped** on the order detail page.

**Requirements for marking shipped:**
- Fulfillment status must be **fulfilled**
- Order must not be cancelled or refunded

**What happens:**
- Fulfillment status changes to `shipped`
- Order status changes to `complete`
- An audit record is created
- The progress bar advances to step 3 (Shipped)
- The order moves from the "Needs Action" tab to the "Shipped" tab in the fulfillment queue

---

## Fulfillment for Non-Shipped Orders

### Pickup Orders

For orders with shipping method "Pickup":
- Follow the same Fulfill -> Ship workflow
- "Mark Shipped" in this context means the customer has picked up their order
- No shipping label is needed

### Local Delivery Orders

For orders with shipping method "Local Delivery":
- Follow the same Fulfill -> Ship workflow
- "Mark Shipped" means the delivery has been completed
- No shipping label is needed unless you use a delivery service

---

## Quick Reference

| Action | Button | Result |
|--------|--------|--------|
| Pack an order | Fulfill Order | Status: processing, Fulfillment: fulfilled |
| Ship an order | Mark Shipped | Status: complete, Fulfillment: shipped |
| Create label | POST form on order page | Label created via shipping provider, stored in R2 |
| Download label | Link on shipment | Temporary signed URL, auto-downloads PDF |
| Print packing slip | Packing Slip button | Opens printable page in new tab |
