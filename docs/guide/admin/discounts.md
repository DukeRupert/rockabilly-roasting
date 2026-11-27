# Discounts and Coupons

Discounts let you offer a percentage or a fixed-dollar saving to customers. Each discount has a **coupon code** that customers enter during checkout.

## Concepts

A **discount** is a rule that defines the type and amount of the reduction, and who can use it. A **coupon code** is what the customer actually types in.

Every discount has three settings that decide where its code works, who can use it and how often:

- **How often** -- chosen when the discount is created, and fixed after that:
  - **Single-use** -- the code works for one order. The first customer to pay with it uses it up for everyone. Good for a one-off code given to one person.
  - **Once per customer** -- the code can go out to everyone, and each customer can use it on one order. Good for a code sent to a whole mailing list.
- **Who can use it** -- can be changed at any time:
  - **Anyone** -- any shopper, signed in or not.
  - **Daily Grind members only** -- the shopper must be signed in, with an **active** subscription. Paused, past-due, cancelled and expired subscriptions don't count. The order must be for the signed-in member's own account. A shopper who isn't signed in is asked to sign in, and their cart is kept when they do.
  - **New subscribers only** -- the customer has never had a subscription, in any status. Someone who subscribed before and cancelled doesn't count as new.
- **Order type** -- can be changed at any time:
  - **One-time orders** -- the code works at cart checkout. Every code made before this setting existed is one of these.
  - **First subscription order** -- the code works only on the subscribe page, and takes money off the first order of the new subscription. Later deliveries are charged the plan's normal price. Use this for a welcome offer like WELCOME50.

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
- **Who can use it** -- Anyone, Daily Grind members only, or New subscribers only.
- **Order type** -- One-time orders or First subscription order.
- **Expires** -- Optional. The code works through the end of this day.

## Editing a Discount

Click a discount's name to open its page. The **Edit** panel on the right lets you change the name, a staff-only description, the type and value, the minimum order, **Who can use it**, **Order type**, the **Starts** date and the **Last valid day**.

- **Starts** -- The code doesn't work before this day.
- **Last valid day** -- The code works through the end of this day.

Changes only affect future orders. Orders already placed keep the discount they were given.

The **Status** panel turns the discount on or off. An inactive discount can't be used, even if it hasn't expired.

## Viewing the Discount List

The list page shows each discount with:

- **Name** -- Click it to open the discount's page.
- **Code** -- The coupon code. A used-up single-use code is struck through. Once-per-customer codes show how many orders have used them, members-only and new-subscriber discounts say so, and subscription codes say "First subscription order".
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

## Promoting a Discount in the Site Banner

A discount can be promoted in a strip of offers above the header on every storefront page. Open the discount and use the **Site banner** card on the right:

1. Write one short line, at most 120 characters, and **include the code**. For example: *Holiday gift: 20% off any bag with code GIFT20*. The referral offer is the exception: leave the code out, because each member has their own. For example: *Share your Daily Grind code: friends get 50% off, you get a free bag*.
2. Click **Save banner**.

The line links to where the code is used: the shop for one-time orders, the Daily Grind page for a first subscription order, and a member's subscriptions page for the referral offer. The card says who will see the line and where it links before you save.

The banner only shows while the discount is **active and within its dates**, so it goes away when the discount ends. Nobody has to remember to take it down. To stop showing it sooner, empty the line and save, or deactivate the discount.

The site shows each line only to the customers the discount is for:

| Discount | Who sees the banner |
|---|---|
| Anyone | Everyone |
| Daily Grind members only | Signed-in active members |
| New subscribers only | Everyone except signed-in customers who have subscribed before |
| The referral offer | Signed-in active members, who share their own codes. Friends arrive with a member's code instead. |

Wholesale accounts don't see storefront banners. If several discounts are promoted, each gets its own line, with the one ending soonest first.

A member who isn't signed in sees what a guest sees. Members-only offers need sign-in at checkout anyway, so email is still the way to reach every member.

## Referral Program

Each active Daily Grind member can share their own code. A friend who uses it gets the referral discount on their first subscription order, and the member earns a free bag.

**Setting it up.** Create a discount for the friend's offer -- for the Daily Grind referral, 50% off, Once per customer, New subscribers only, First subscription order. Open it and choose **Use as the referral offer** in the Referral offer panel. Only one discount can be the referral offer; choosing another replaces it. To be chosen, a discount must be Once per customer (each member's code goes to many friends), for the First subscription order, and a percentage or fixed amount off. The panel lists anything a discount is missing.

The code you typed when creating the discount stops working once it is the referral offer: the offer only works through a member's code, because the code is what says who referred the friend. So use a separate discount for referrals, not one whose code you've already shared, like WELCOME50. The confirmation names the code that will stop working.

Members only see their code while the offer can be used: switched on, past its start date and through its last valid day. An expired offer's card disappears.

**Members' codes.** Every member with an active subscription sees a "Share the Daily Grind" card on their subscriptions page with their own code (their first name and four characters, like LOGAN-7KQ2), a copy button, and how many friends have joined with it. A member can't use their own code. The discount's page shows how many member codes exist rather than listing them.

**Free bags.** When a friend's first order with a member's code is paid, the member is owed a free bag. Go to **Admin > Discounts > Referrals** to see the bags owed, oldest first, with the member, their code and the friend's order. Send the bag, then choose **Mark sent**. If the friend's order is cancelled before the bag goes out, it drops off the list.

**Ending the program.** Choose **Stop using as referral offer** on the discount. Members' codes stop working and the card disappears from their pages. Bags already earned stay on the Referrals page.
