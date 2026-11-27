# Discounts and Coupons

Discounts let you offer a percentage or a fixed-dollar saving to customers. Each discount has a **coupon code** that customers enter during checkout.

## Concepts

A **discount** is a rule that defines the type and amount of the reduction, and who can use it. A **coupon code** is what the customer actually types in.

Every discount has two settings that decide who can use its code and how often:

- **How often** -- chosen when the discount is created, and fixed after that:
  - **Single-use** -- the code works for one order. The first customer to pay with it uses it up for everyone. Good for a one-off code given to one person.
  - **Once per customer** -- the code can go out to everyone, and each customer can use it on one order. Good for a code sent to a whole mailing list.
- **Who can use it** -- can be changed at any time:
  - **Anyone** -- any shopper, signed in or not.
  - **Daily Grind members only** -- the shopper must be signed in, with an **active** subscription. Paused, past-due, cancelled and expired subscriptions don't count. The order must be for the signed-in member's own account. A shopper who isn't signed in is asked to sign in, and their cart is kept when they do.

"Once per customer" goes by the customer's account, which checkout finds from the email address entered. A shopper who checks out as a guest with a different email address counts as a new customer.

## Discount Types

| Type | How It Works | Example Display |
|------|-------------|-----------------|
| **Percentage** | Takes a percentage off the subtotal | `15%` |
| **Fixed Amount** | Takes a flat dollar amount off the subtotal, never more than the subtotal | `$5.00` |

Free-shipping discounts are not available.

## Creating a Discount

Go to **Admin > Discounts**. The **New Discount** form at the top of the page has:

- **Name** -- A label shown in the admin panel and to the customer when the code is applied (e.g., "Holiday Gift 20% Off").
- **Coupon code** -- What customers type. Stored in capitals; customers can type it in any case. A code can't be changed once created; to change it, create a new discount.
- **Type** -- Percentage or Fixed amount.
- **Value** -- A whole percent (e.g., `20`) or a dollar amount (e.g., `5.00`).
- **Min order $** -- Optional. The code only works when the subtotal is at least this much.
- **How often** -- Single-use or Once per customer. See above. This can't be changed later.
- **Who can use it** -- Anyone or Daily Grind members only.
- **Expires** -- Optional. The code works through the end of this day.

## Editing a Discount

Click a discount's name to open its page. The **Edit** panel on the right lets you change the name, a staff-only description, the type and value, the minimum order, **Who can use it**, the **Starts** date and the **Last valid day**.

- **Starts** -- The code doesn't work before this day.
- **Last valid day** -- The code works through the end of this day.

Changes only affect future orders. Orders already placed keep the discount they were given.

The **Status** panel turns the discount on or off. An inactive discount can't be used, even if it hasn't expired.

## Viewing the Discount List

The list page shows each discount with:

- **Name** -- Click it to open the discount's page.
- **Code** -- The coupon code. A used-up single-use code is struck through. Once-per-customer codes show how many orders have used them, and members-only discounts say "Daily Grind members only".
- **Type** and **Value** -- Including the minimum order, if any.
- **Status** -- Active or inactive.
- **Dates** -- The start and expiry dates, if set.
- **Created** -- When the discount was created (on larger screens).

Use the dropdown above the list to show all, active or inactive discounts. The list shows 25 discounts per page.

## Tracking Who Used a Code

A discount's page shows how it has been used:

- **Single-use** -- whether the code has been used, when, and a link to the order.
- **Once per customer** -- how many orders have used it, with the most recent 50 listed by date and linked to their orders.

A code only counts as used once the order is paid. An order that's started but never paid doesn't use it up, and cancelling an order makes the code available to that customer again.

Very rarely, two orders using the same code are paid at almost the same moment. The first payment gets the code; the second customer still keeps the discount they were charged, and the order's activity log shows "Coupon already used by another order".

## How Customers Apply Coupons

Customers enter a code in the order summary at checkout. A code is accepted only if:

- it exists and its discount is active;
- its start date has arrived and it hasn't expired;
- the cart meets any minimum order;
- it hasn't already been used up (single-use), or used by this customer before (once per customer);
- for members-only codes, the shopper is signed in with an active subscription.

If a code is refused, the customer sees a message saying why. The code is checked again when the payment is set up; if it no longer applies (for example, a once-per-customer code entered before checkout knew who the customer was), it's removed and the customer is told why before they pay.

Coupon entry is rate-limited by IP address to prevent abuse.
