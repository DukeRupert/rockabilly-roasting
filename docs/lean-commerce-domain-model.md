# Lean Commerce — Core Domain Model

> **Green-Field Design Reference**
> Scope: B2C Retail · B2B Wholesale · Recurring Subscriptions

**Legend:**
- `core` — Required by every deployment
- `ext` — Supports a specific vertical or optional feature

---

## Design Philosophy

This document describes a lean, green-field ecommerce domain model designed for extensibility. The goal is a small, stable core that can be vertically adapted — for a coffee roastery, a construction supplier, a wholesaler — without touching the foundation.

### Four Principles

1. **Separate what changes from what doesn't.** The pricing model is volatile; the order structure is not. These live in different tables.
2. **State machines are explicit.** Every entity that moves through lifecycle stages (orders, fulfillments, subscriptions) has a named status column with a constrained enum — not a boolean or a free-text field.
3. **Extension over modification.** Vertical-specific concepts (roast profiles, inspection types, quote line items) attach to the core via foreign keys or a JSON metadata column — they do not alter core tables.
4. **Pricing is a first-class domain, not a column on Product.** Prices belong to PriceSets which link to variants. This cleanly separates catalog from commerce.

### Domain Boundaries

The model is organized into six domains. Each domain owns its tables and exposes relationships to other domains — it never reaches across to modify another domain's data directly.

| Domain | Responsibility |
|---|---|
| Catalog | Products, options, variants, media, taxonomy |
| Pricing | Price sets, price lists, tiers, customer-group rules |
| Customer | Accounts, groups, addresses, authentication |
| Order | Carts, orders, line items, adjustments, payment & fulfillment state |
| Fulfillment | Shipments, locations, inventory, tracking |
| Subscription | Plans, schedules, renewal orders |

---

## Domain 1 — Catalog

The catalog domain answers the question: *what do we sell and how is it organized?* It is deliberately free of pricing and inventory — those belong to other domains.

### 1.1 Product

A Product is the human-facing concept of a sellable thing. It carries descriptive information, categorization, and media. It is never sold directly — only its Variants are sold.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| slug | text | URL-safe unique identifier | `core` |
| title | text | Display name | `core` |
| description | text | Long-form description, HTML or Markdown | `core` |
| status | enum | `draft` \| `active` \| `archived` | `core` |
| product_type_id | uuid FK | Optional grouping by type (e.g. "Green Bean", "Roasted") | `core` |
| taxon_id | uuid FK | Primary taxonomy node | `core` |
| metadata | jsonb | Vertical-specific attributes (e.g. origin, process) | `ext` |
| available_on | timestamptz | Date product becomes visible in storefront | `ext` |
| discontinue_on | timestamptz | Date product is removed from storefront | `ext` |
| created_at | timestamptz | | `core` |
| updated_at | timestamptz | | `core` |

### 1.2 ProductOption

Options define the dimensions along which a product varies. Examples: "Roast Level", "Grind", "Size", "Color". Each option has a set of allowed values.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| product_id | uuid FK | Parent product | `core` |
| name | text | e.g. "Roast Level", "Grind" | `core` |
| position | int | Display sort order | `core` |

### 1.3 ProductOptionValue

The specific values an option can take. e.g. "Light", "Medium", "Dark" for a "Roast Level" option.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| product_option_id | uuid FK | Parent option | `core` |
| value | text | e.g. "Light", "Medium" | `core` |
| position | int | Display sort order | `core` |

### 1.4 Variant

A Variant is the actual sellable unit — one specific combination of option values for a product. Every product must have at least one variant. A product with no meaningful options still has a single "default" variant.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| product_id | uuid FK | Parent product | `core` |
| sku | text | Stock-keeping unit; unique across the store | `core` |
| barcode | text | UPC, EAN, or internal barcode | `ext` |
| position | int | Display sort order within the product | `core` |
| is_default | bool | True for the variant shown by default in storefront | `core` |
| weight_grams | int | Used for shipping rate calculation | `ext` |
| metadata | jsonb | Vertical-specific variant attributes | `ext` |
| created_at | timestamptz | | `core` |
| updated_at | timestamptz | | `core` |

### 1.5 VariantOptionValue

Join table connecting each variant to its selected option values.

| Field | Type | Description | Layer |
|---|---|---|---|
| variant_id | uuid FK | References Variant | `core` |
| product_option_value_id | uuid FK | References ProductOptionValue | `core` |

### 1.6 ProductMedia

Images and other media assets attached to a product or specific variant. Position controls display order. The primary image has `position = 0`.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| product_id | uuid FK | Parent product | `core` |
| variant_id | uuid FK | Null = applies to all variants | `core` |
| url | text | Storage URL (S3, CDN, etc.) | `core` |
| alt_text | text | Accessibility description | `core` |
| position | int | 0 = primary image | `core` |
| media_type | enum | `image` \| `video` | `ext` |

### 1.7 Taxon

Taxons form a tree for organizing products in navigation (e.g. Categories > Coffee > Single Origin). A product belongs to one primary taxon and can be tagged to additional taxons.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| parent_id | uuid FK | Null = root node | `core` |
| name | text | Display name | `core` |
| slug | text | URL segment | `core` |
| position | int | Sort order among siblings | `core` |
| depth | int | Cached depth for efficient queries | `core` |

---

## Domain 2 — Pricing

Pricing is intentionally separated from the catalog. A variant has no price column — instead it links to a PriceSet, which holds one or more prices. This design makes it possible to express currency variants, quantity tiers, customer-group rates, and promotional overrides without touching the product or variant tables.

### 2.1 PriceSet

A PriceSet is a container for prices related to one resource (typically a variant). The separation allows pricing logic to evolve independently of the catalog.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| variant_id | uuid FK | The variant this price set belongs to | `core` |

### 2.2 Price

A Price represents one specific money amount within a PriceSet. Multiple Prices in the same PriceSet allow for currency variants, quantity tiers, and customer-group-specific rates.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| price_set_id | uuid FK | Parent PriceSet | `core` |
| amount | int | Amount in smallest currency unit (e.g. cents) | `core` |
| currency_code | text | ISO 4217 code: `usd`, `eur`, `cad` | `core` |
| min_quantity | int | Null = no minimum; enables tiered pricing | `ext` |
| max_quantity | int | Null = no maximum; enables tiered pricing | `ext` |
| customer_group_id | uuid FK | Null = all customers; enables group pricing | `ext` |
| price_list_id | uuid FK | Null = always active; groups promotional prices | `ext` |
| starts_at | timestamptz | Null = always active | `ext` |
| ends_at | timestamptz | Null = no expiry | `ext` |

**Example** — a "Colombia Washed" variant with retail, wholesale, and tiered pricing:

| amount | currency | min_qty | max_qty | customer_group |
|---|---|---|---|---|
| 1800 (= $18.00) | usd | — | — | — (retail default) |
| 1400 (= $14.00) | usd | — | — | wholesale |
| 1600 (= $16.00) | usd | 5 | 19 | — (qty tier) |
| 1400 (= $14.00) | usd | 20 | — | — (qty tier) |

### 2.3 PriceList

A PriceList groups promotional or scheduled prices — for example a "Holiday Sale" or "Early Bird" pricing window. Prices belonging to a PriceList are only applied when the list is active and its rules are satisfied.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| name | text | Internal label | `core` |
| type | enum | `sale` \| `override` | `ext` |
| status | enum | `draft` \| `active` \| `expired` | `ext` |
| starts_at | timestamptz | Null = immediately active | `ext` |
| ends_at | timestamptz | Null = no expiry | `ext` |

### 2.4 CustomerGroup

CustomerGroups gate prices and promotions to specific customers. Examples: "Retail", "Wholesale", "VIP", "Staff". A customer can belong to multiple groups; the most favorable price wins.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| name | text | Display name | `core` |
| metadata | jsonb | Vertical-specific group attributes (e.g. credit terms) | `ext` |

---

## Domain 3 — Customer

The customer domain manages accounts, identity, and addresses. It is deliberately thin — business logic (pricing eligibility, order history) is handled by other domains that reference Customer.

### 3.1 Customer

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| email | text | Unique; used for login and notifications | `core` |
| first_name | text | | `core` |
| last_name | text | | `core` |
| phone | text | | `ext` |
| is_guest | bool | True for checkout-only accounts | `core` |
| metadata | jsonb | Vertical-specific customer attributes | `ext` |
| created_at | timestamptz | | `core` |

### 3.2 CustomerGroupMembership

Join table — a customer can belong to zero or more CustomerGroups.

| Field | Type | Description | Layer |
|---|---|---|---|
| customer_id | uuid FK | References Customer | `core` |
| customer_group_id | uuid FK | References CustomerGroup | `core` |
| assigned_at | timestamptz | | `core` |

### 3.3 Address

Addresses are stored independently and referenced by orders and customers. This allows address history to persist even if an order's shipping address is later updated.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| customer_id | uuid FK | Null for guest checkout addresses | `core` |
| first_name | text | | `core` |
| last_name | text | | `core` |
| company | text | Useful for B2B / wholesale billing | `ext` |
| line1 | text | | `core` |
| line2 | text | | `core` |
| city | text | | `core` |
| state | text | | `core` |
| postal_code | text | | `core` |
| country_code | text | ISO 3166-1 alpha-2 | `core` |
| is_default | bool | Customer's default shipping address | `core` |

---

## Domain 4 — Order

The order domain is the transactional heart of the platform. Its most important design decision is that **order state, payment state, and fulfillment state are three separate status fields** — not one. A "complete" order may still have an outstanding payment, and a paid order may be only partially fulfilled.

### 4.1 Cart

A Cart is a pre-order container. It becomes an Order on checkout completion. Carts can exist for guests (no customer) and are converted to customer-owned carts on login.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| customer_id | uuid FK | Null for guest carts | `core` |
| currency_code | text | Set at cart creation | `core` |
| shipping_address_id | uuid FK | | `core` |
| billing_address_id | uuid FK | | `core` |
| applied_discount_id | uuid FK | FK to discounts; null if no discount applied | `ext` |
| applied_coupon_code_id | uuid FK | FK to coupon_codes; null for automatic discounts or no discount | `ext` |
| metadata | jsonb | | `ext` |
| expires_at | timestamptz | Carts expire if abandoned | `ext` |
| created_at | timestamptz | | `core` |

### 4.2 Order

An Order is an immutable snapshot of a completed cart. Core financial totals are denormalized and stored directly on the order so historical records remain accurate even if pricing changes.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| number | text | Human-readable reference e.g. `ORD-10042` | `core` |
| customer_id | uuid FK | Null for guest orders | `core` |
| status | enum | See Order Status States below | `core` |
| payment_status | enum | See Payment States below | `core` |
| fulfillment_status | enum | See Fulfillment States below | `core` |
| currency_code | text | Currency at time of order | `core` |
| subtotal | int | Sum of line item totals before adjustments | `core` |
| discount_total | int | Total value of all discounts applied | `core` |
| shipping_total | int | Total shipping cost | `core` |
| tax_total | int | Total tax collected | `core` |
| total | int | Final amount charged | `core` |
| shipping_address_id | uuid FK | Snapshot address at time of order | `core` |
| billing_address_id | uuid FK | | `core` |
| subscription_id | uuid FK | Null for one-time orders | `ext` |
| draft_by_user_id | uuid FK | Staff-created draft orders | `ext` |
| tax_exempt | bool | True if customer was tax exempt at order time | `ext` |
| tax_exempt_reason | text | Copied from customer at order time; null if not exempt | `ext` |
| stripe_tax_id | text | Stripe tax calculation reference; null for exempt orders | `ext` |
| notes | text | Internal staff notes | `ext` |
| metadata | jsonb | Vertical-specific order attributes | `ext` |
| placed_at | timestamptz | | `core` |
| created_at | timestamptz | | `core` |

Discount, tax, and shipping details beyond the totals stored on the order are recorded in related tables: `order_discounts` (discount type, value, and amount applied), and `shipments` (label URL, tracking number, carrier). See `lean-commerce-tax.md`, `lean-commerce-shipping.md`, and `lean-commerce-discounts.md` for full schema and design details.

### 4.3 Order Status States

| State | Meaning | Terminal? |
|---|---|---|
| `pending` | Order placed, awaiting payment confirmation | No |
| `confirmed` | Payment confirmed; ready for processing | No |
| `processing` | Actively being prepared or assembled | No |
| `on_hold` | Paused pending customer action or staff review | No |
| `complete` | All items fulfilled and delivered | **Yes** |
| `cancelled` | Cancelled before fulfillment; refund may be issued | **Yes** |
| `refunded` | Fully refunded after fulfillment | **Yes** |

### 4.4 Payment States

| State | Meaning | Terminal? |
|---|---|---|
| `awaiting` | Payment has not been attempted | No |
| `authorized` | Payment authorized but not captured | No |
| `captured` | Payment successfully captured | **Yes** |
| `partial` | Partial payment captured (e.g. deposit) | No |
| `refunded` | Full or partial refund issued | **Yes** |
| `failed` | Payment attempt failed | No |
| `voided` | Authorization voided without capture | **Yes** |

### 4.5 Fulfillment States

| State | Meaning | Terminal? |
|---|---|---|
| `unfulfilled` | No items have been fulfilled yet | No |
| `partially_fulfilled` | Some line items fulfilled, others pending | No |
| `fulfilled` | All items picked and packed; shipment pending | No |
| `partially_shipped` | Some shipments sent; others pending | No |
| `shipped` | All shipments dispatched | No |
| `partially_delivered` | Some shipments confirmed delivered | No |
| `delivered` | All items confirmed delivered | **Yes** |
| `returned` | Items returned by customer | **Yes** |

### 4.6 Admin Order Pipeline

The admin UI presents a simplified **three-step progress bar** rather than exposing the raw order/payment/fulfillment statuses independently. This avoids confusing combinations (e.g., "pending" order status alongside "captured" payment).

```
  Paid  ────  Fulfilled  ────  Shipped
   ✓             ✓              ○
```

**Pipeline mapping:**
| Step | Condition | Order Status set to | Fulfillment Status set to |
|------|-----------|---------------------|---------------------------|
| Paid | Payment captured (via Stripe webhook) | `confirmed` | — |
| Fulfilled | Admin clicks "Fulfill Order" | `processing` | `fulfilled` |
| Shipped | Admin clicks "Mark Shipped" | `complete` | `shipped` |

**Guided actions:** Only the logical next step is shown as a primary button. Cancel and refund appear as secondary actions when valid. Cancelled/refunded orders skip the progress bar and show a badge instead.

**Order list:** Each row shows compact progress dots (●●○) with a label ("Paid", "Fulfilled", "Shipped") instead of separate status columns.

**Packing slip:** Available from order detail, opens in a new tab with `window.print()` auto-trigger. Shows ship-to address, customer info, line items with product names/SKUs, and order totals.

### 4.7 LineItem

A LineItem records one variant and quantity on an order. Prices are denormalized at the time of order placement to preserve historical accuracy.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| order_id | uuid FK | Parent order | `core` |
| variant_id | uuid FK | The variant purchased | `core` |
| quantity | int | Quantity ordered | `core` |
| unit_price | int | Price per unit at time of order | `core` |
| subtotal | int | unit_price × quantity | `core` |
| discount_total | int | Total discounts applied to this line | `core` |
| tax_total | int | Tax on this line item | `core` |
| total | int | Final line total | `core` |
| metadata | jsonb | Vertical-specific line attributes | `ext` |

### 4.8 Adjustment

Adjustments modify an order total. They can be positive (surcharges, fees) or negative (discounts, promotions). Every change to order totals is traceable through adjustments.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| order_id | uuid FK | Parent order | `core` |
| line_item_id | uuid FK | Null = order-level; set = line-item-level | `core` |
| label | text | Human-readable description e.g. "10% off" | `core` |
| amount | int | Negative for discounts, positive for fees | `core` |
| source_type | text | `promotion` \| `shipping` \| `tax` \| `manual` | `core` |
| source_id | uuid | FK to the promotion/shipping/tax record | `core` |

---

## Domain 5 — Fulfillment & Inventory

The fulfillment domain tracks the physical movement of goods from stock to customer. Inventory is managed at the location level — a variant's total stock is the sum across all locations.

### 5.1 StockLocation

A StockLocation represents a physical place where inventory is held: a warehouse, a retail store, a production floor.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| name | text | e.g. "Helena Warehouse" | `core` |
| address_id | uuid FK | Physical address | `ext` |
| is_active | bool | Inactive locations excluded from fulfillment | `core` |

### 5.2 InventoryItem

An InventoryItem is the abstract inventory record for a variant — independent of location. StockLevel records attach to it per-location.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| variant_id | uuid FK | One-to-one with Variant | `core` |
| track_inventory | bool | False = oversellable (e.g. made-to-order) | `core` |
| requires_shipping | bool | False for digital or service products | `core` |

### 5.3 StockLevel

A StockLevel records the quantity of an InventoryItem at a specific StockLocation. Reserved quantity is subtracted when an order is placed but before it ships.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| inventory_item_id | uuid FK | | `core` |
| location_id | uuid FK | | `core` |
| quantity_on_hand | int | Physical count in this location | `core` |
| quantity_reserved | int | Allocated to placed orders not yet shipped | `core` |
| quantity_available | int | Computed: on_hand − reserved | `core` |

### 5.4 Fulfillment

A Fulfillment represents a physical shipment event — picking, packing, and dispatching one or more line items. A single order can have multiple fulfillments (e.g. items ship from different locations).

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| order_id | uuid FK | Parent order | `core` |
| location_id | uuid FK | Stock location items ship from | `core` |
| status | enum | `pending` \| `packed` \| `shipped` \| `delivered` \| `cancelled` | `core` |
| tracking_number | text | | `ext` |
| tracking_url | text | | `ext` |
| provider | text | e.g. "UPS", "FedEx", "local" | `ext` |
| shipped_at | timestamptz | | `ext` |
| delivered_at | timestamptz | | `ext` |
| metadata | jsonb | Vertical-specific fulfillment data | `ext` |

### 5.5 FulfillmentItem

Links specific line items and quantities to a fulfillment. Supports partial fulfillment.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| fulfillment_id | uuid FK | Parent Fulfillment | `core` |
| line_item_id | uuid FK | The line item being fulfilled | `core` |
| quantity | int | Quantity included in this shipment | `core` |

---

## Domain 6 — Subscription

Subscriptions represent recurring purchase agreements. Rather than building a complex recurring billing engine, this design treats subscriptions as a **scheduling mechanism** that generates standard Orders on a cadence. All payment and fulfillment flow through the core Order domain.

### 6.1 SubscriptionPlan

A SubscriptionPlan is a merchant-configured recurring cadence — e.g. "Every 30 Days" or "Every 14 Days". Plans are decoupled from products; any product with `subscribable = true` can be subscribed to on any active plan. The customer chooses both the product/variant and the delivery frequency independently.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| name | text | e.g. "Every 30 Days" | `core` |
| interval | enum | `every_14_days` \| `every_21_days` \| `every_30_days` \| `every_60_days` | `core` |
| interval_count | int | Multiplier (default 1) | `core` |
| discount_pct | int | Subscriber discount percentage (e.g. 15 = 15% off) | `core` |
| is_active | bool | Inactive plans no longer accept new subscribers | `core` |
| metadata | jsonb | Vertical-specific plan attributes | `ext` |

Product eligibility is controlled by `products.subscribable` (boolean, default false). Admin toggles this per product — e.g. coffee = subscribable, t-shirts = not.

### 6.2 Subscription

A Subscription is a customer's active recurring agreement. It references both a Plan (cadence) and a Variant (what gets shipped), allowing customers to swap products without changing their delivery frequency.

| Field | Type | Description | Layer |
|---|---|---|---|
| id | uuid | Primary key | `core` |
| customer_id | uuid FK | | `core` |
| plan_id | uuid FK | Delivery cadence | `core` |
| variant_id | uuid FK | What gets shipped each period | `core` |
| status | enum | See Subscription States below | `core` |
| shipping_address_id | uuid FK | | `core` |
| current_period_start | timestamptz | Start of current billing/fulfillment period | `core` |
| current_period_end | timestamptz | End of current period; next order generated here | `core` |
| next_order_at | timestamptz | Scheduled time to generate next order | `core` |
| cancelled_at | timestamptz | Set on cancellation | `ext` |
| pause_until | timestamptz | Customer-requested pause | `ext` |
| metadata | jsonb | | `ext` |
| created_at | timestamptz | | `core` |

### 6.3 Subscription States

| State | Meaning | Terminal? |
|---|---|---|
| `active` | Running; orders generated on schedule | No |
| `paused` | Temporarily paused; resumes at pause_until date | No |
| `past_due` | Last renewal order failed payment | No |
| `cancelled` | Cancelled; no further orders generated | **Yes** |
| `expired` | Natural end of fixed-term subscription | **Yes** |

### 6.4 SubscriptionOrder

Join table linking generated Orders back to the Subscription that created them. Keeps renewal history queryable without polluting the core Order table.

| Field | Type | Description | Layer |
|---|---|---|---|
| subscription_id | uuid FK | Parent Subscription | `core` |
| order_id | uuid FK | Generated Order | `core` |
| period_start | timestamptz | Period this order covers | `core` |
| period_end | timestamptz | | `core` |

---

## Domain Relationships Summary

Key cross-domain relationships. Within-domain relationships are implied by the FK columns in each entity's schema.

| Entity | Relationship | Entity | Notes |
|---|---|---|---|
| Variant | has one | PriceSet | Pricing is external to catalog |
| PriceSet | has many | Price | Multiple currencies, tiers, groups |
| Price | belongs to (opt) | CustomerGroup | Null = applies to all customers |
| Price | belongs to (opt) | PriceList | Null = always active |
| Customer | has many | CustomerGroup | Via CustomerGroupMembership |
| Customer | has many | Address | |
| Order | has many | LineItem | |
| Order | has many | Adjustment | Discounts, fees, taxes |
| Order | has many | Fulfillment | Partial fulfillment supported |
| LineItem | belongs to | Variant | Denormalized price at order time |
| Fulfillment | has many | FulfillmentItem | |
| InventoryItem | has many | StockLevel | One per StockLocation |
| Subscription | has many | Order | Via SubscriptionOrder join table |
| Subscription | belongs to | SubscriptionPlan | Delivery cadence |
| Subscription | belongs to | Variant | What gets shipped each period |

---

## Extension Patterns

How to adapt the core model for specific verticals without modifying core tables.

### Pattern 1 — Metadata Columns

Every core entity has a `metadata jsonb` column. Vertical-specific attributes that don't need to be queried relationally go here. Examples:

- Coffee roastery: `product.metadata = { origin, process, varietal, elevation_masl }`
- Construction supplier: `variant.metadata = { unit_of_measure, lead_time_days }`
- Wholesale: `order.metadata = { po_number, payment_terms }`

### Pattern 2 — Extension Tables

For vertical-specific entities that need their own relationships and queries, create extension tables with a FK back to the core entity. Core tables are never altered. Examples:

- `RoastProfile`: id, variant_id FK, roast_date, roaster_name, cupping_notes, bag_notes
- `QuoteLine`: id, order_id FK, description, unit, quantity, unit_price
- `InspectionRecord`: id, order_id FK, inspector_id, status, notes

### Pattern 3 — Fulfillment Status Extension

The Order domain intentionally uses a domain-agnostic `fulfillment_status` enum. A vertical with a production step (roasting, fabrication, assembly) can introduce a parallel status on an extension table rather than modifying Order:

- `RoastStatus`: order_id FK, status enum (`queued | roasting | rested | packed`), updated_at
- The `fulfillment_status` on Order transitions to `fulfilled` only after `RoastStatus` reaches `packed`
- Keeps the core Order table clean while allowing rich vertical workflow

### Pattern 4 — Draft Orders for B2B

The Order table has a `draft_by_user_id` column. An order with this set is a staff-created draft — a quote or manual order for a wholesale customer. The checkout flow is bypassed; staff confirm the order directly through an admin interface. This supports:

- Sales reps creating orders on behalf of wholesale customers
- Quote-to-order workflows
- Phone or in-person order entry
