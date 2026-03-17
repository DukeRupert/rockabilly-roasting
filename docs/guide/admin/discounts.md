# Discounts and Coupons

Discounts let you offer price reductions, fixed-dollar savings, or free shipping to customers. Each discount is linked to one or more **coupon codes** that customers enter during checkout.

## Concepts

A **discount** is a rule that defines the type and amount of the reduction. A **coupon code** is a redeemable string tied to a discount -- it is what the customer actually types in.

Coupon codes are single-use by default. Once a coupon has been redeemed against an order, it cannot be used again.

## Discount Types

| Type | How It Works | Example Display |
|------|-------------|-----------------|
| **Percentage** | Reduces the cart total by a percentage of the subtotal | `15%` |
| **Fixed Amount** | Subtracts a flat dollar amount from the cart total | `$5.00` |
| **Free Shipping** | Waives the shipping charge entirely | `Free` |

## Discount Fields

Each discount has the following properties:

- **Name** -- A human-readable label shown in the admin panel (e.g., "Summer Sale 15% Off").
- **Type** -- One of the three types listed above.
- **Value** -- The numeric value of the discount. For percentage discounts this is the percent (e.g., `15` for 15%). For fixed-amount discounts this is stored in cents (e.g., `500` for $5.00).
- **Minimum Order (cents)** -- Optional. If set, the discount only applies when the cart subtotal meets or exceeds this amount.
- **Starts At** -- Optional. The date the discount becomes valid.
- **Expires At** -- Optional. The date the discount stops being valid.
- **Active** -- Whether the discount is currently enabled. An inactive discount cannot be redeemed even if it has not expired.

## Viewing the Discount List

Navigate to **Admin > Discounts** in the sidebar.

The list page displays a table with the following columns:

- **Name** -- The discount name.
- **Type** -- Percentage, Fixed Amount, or Free Shipping.
- **Value** -- The formatted discount value.
- **Status** -- An active/inactive badge.
- **Dates** -- The start and expiration dates, if set. A dash indicates no date constraint on that side.
- **Created** -- When the discount was created (visible on larger screens).

### Filtering

Use the dropdown at the top of the page to filter by status:

- **All discounts** -- Shows everything.
- **Active** -- Only discounts that are currently enabled.
- **Inactive** -- Only disabled discounts.

The filter selection is preserved in the URL, so you can bookmark or share filtered views.

### Pagination

The list shows 25 discounts per page. Use the Previous and Next links at the bottom to navigate.

## Redemption Tracking

Each coupon code tracks:

- **Who redeemed it** -- The customer ID of the person who used the code.
- **When it was redeemed** -- The exact timestamp.
- **Which order** -- The order ID the coupon was applied to.

A coupon that has already been redeemed cannot be used again. If a customer tries to enter a used code, they will see the message "That code has already been used."

## How Customers Apply Coupons

During checkout, customers see a coupon code input field. The flow works as follows:

1. The customer enters a code and submits it.
2. The system validates the code:
   - The code must exist and be tied to an active discount.
   - The discount must not have expired.
   - The code must not have been previously redeemed.
3. If valid, the discount is applied to the cart and the customer sees the discount name and savings amount.
4. If invalid, the customer sees a specific error message explaining why (code not found, already used, expired, or no longer active).
5. Customers can remove an applied coupon before completing their purchase.

Coupon application is rate-limited by IP address to prevent abuse.

When the order is finalized, the coupon code is marked as redeemed and linked to the completed order.

## Creating Discounts and Coupons

Discount and coupon creation is not yet available through the admin panel UI. To create a new discount or generate coupon codes, contact the administrator.
