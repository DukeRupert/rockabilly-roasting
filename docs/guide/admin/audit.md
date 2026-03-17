# Audit Log

The audit log records every significant change made to your store. It provides a chronological trail of who did what, when, and to which resource. Navigate to **Admin > Audit Log** in the sidebar.

## What Gets Tracked

Every data-modifying operation in the system writes an audit entry within the same database transaction as the change itself. This guarantees that the audit trail is always consistent with the actual data -- if the change is saved, the audit record is saved too.

The audit log covers actions across all major areas of the platform:

- Orders (created, status changes, refunds, cancellations, fulfillment, shipping)
- Products and variants (created, updated, archived, deleted, price changes, visibility, media)
- Subscriptions and plans (created, paused, cancelled, renewed, resumed, failed renewals)
- Customers (group changes, tax exemptions, address changes, deactivations)
- Staff (created, role changes, deactivations, login/logout)
- Discounts (created, updated, deactivated)
- Shipping (label creation, config updates)
- Wholesale (application approvals/declines, account suspension/reactivation)
- Invoices (created, sent, voided, payments recorded)
- QuickBooks sync (customer/invoice creation, payment sync)
- Product attributes (attribute sets and keys created, updated, deleted)

## What Each Entry Records

Every audit entry contains:

| Field | Description |
|-------|-------------|
| **When** | Timestamp of the action (displayed as `Jan 2 15:04` format) |
| **Actor Type** | Who performed the action: `staff`, `customer`, or `system` |
| **Actor Name** | The name of the person or process that performed the action |
| **Action** | The specific action taken (e.g., `order.refunded`, `product.updated`) |
| **Resource Type** | The kind of resource affected (e.g., `order`, `product`, `subscription`) |
| **Resource ID** | A unique identifier for the specific resource (first 8 characters shown in the table) |

Behind the scenes, each entry also stores:

- **After snapshot** -- A JSON snapshot of the resource state after the change.
- **Request ID** -- The HTTP request ID that triggered the action, for correlating with server logs.
- **IP address** -- The IP address of the request, when available.
- **Reason** -- An optional human-readable reason for the change.
- **Metadata** -- Additional context (e.g., `river_job_id` for background job actions).

## Reading the Audit Log

The audit log page displays entries in reverse chronological order (newest first), 50 per page.

Each row shows:

- The timestamp in the **When** column.
- The actor, displayed as a colored badge indicating the type (slate for staff, green for customer, grey for system) followed by the actor's name.
- The **Action** column shows the action in a human-readable format (e.g., `order.refunded` displays as "Order Refunded").
- The **Resource** column (visible on wider screens) shows the resource type and the first 8 characters of the resource ID.

Use the Previous and Next links at the bottom of the page to navigate through older entries.

## Filtering

Two filter dropdowns appear above the table:

### By Actor Type

Filter entries by who performed the action:

- **All actors** -- No filter applied.
- **Staff** -- Actions taken by admin panel users.
- **Customer** -- Actions taken by customers (login, logout, etc.).
- **System** -- Automated actions from background jobs, scheduled tasks, or integrations.

### By Resource Type

Filter entries by the kind of resource affected. Note: not all resource types are available as filters -- the dropdown covers the most commonly audited resources.

- **Orders**
- **Products**
- **Variants**
- **Subscriptions**
- **Customers**
- **Addresses**
- **Invoices**

When any filter is active, a **Clear filters** link appears to reset all filters at once.

Filters are preserved in the URL query string, so filtered views can be bookmarked or shared.

## Common Audit Actions Reference

### Orders

| Action | Meaning |
|--------|---------|
| `order.created` | A new order was placed |
| `order.status_changed` | The order status was updated |
| `order.refunded` | A refund was issued |
| `order.cancelled` | The order was cancelled |
| `order.fulfilled` | The order was marked as fulfilled |
| `order.shipped` | A shipment was dispatched |
| `order.payment_captured` | Payment was captured for the order |

### Products and Variants

| Action | Meaning |
|--------|---------|
| `product.created` | A new product was added to the catalog |
| `product.updated` | Product details were changed |
| `product.archived` | A product was archived (hidden from storefront) |
| `product.deleted` | A product was permanently removed |
| `product.visibility_updated` | Product visibility settings were changed |
| `variant.created` | A new variant was added to a product |
| `variant.updated` | Variant details were changed |
| `variant.price_updated` | A variant's price was changed |

### Subscriptions

| Action | Meaning |
|--------|---------|
| `subscription.created` | A new subscription was started |
| `subscription.paused` | A subscription was paused |
| `subscription.cancelled` | A subscription was cancelled |
| `subscription.renewed` | A subscription renewal was processed |
| `subscription.resumed` | A paused subscription was resumed |
| `subscription.renewal_failed` | A renewal attempt failed (e.g., payment declined) |

### Staff

| Action | Meaning |
|--------|---------|
| `staff.created` | A new staff account was created |
| `staff.role_changed` | A staff member's role was updated |
| `staff.login` | A staff member logged in |
| `staff.logout` | A staff member logged out |

### Discounts

| Action | Meaning |
|--------|---------|
| `discount.created` | A new discount was created |
| `discount.updated` | A discount's settings were changed |
| `discount.deactivated` | A discount was disabled |
