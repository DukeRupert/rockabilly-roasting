# Checkout Guide

This guide covers everything from starting checkout to receiving your order confirmation at Rockabilly Roasting Co.

---

## Starting Checkout

From your cart page, click the red "Checkout" button. This takes you to `/checkout`, where a dedicated checkout application loads in your browser.

You do not need an account to check out. The system creates a guest customer record using the email address you provide during checkout.

## The Checkout Flow

Checkout is handled by a step-by-step interface. Here is what to expect:

### Step 1: Review Your Cart

The checkout page loads your current cart and displays each item with its name, SKU, quantity, unit price, and line total, along with your cart subtotal. This is your last chance to confirm what you are ordering before entering your details.

### Step 2: Shipping Address

You will be asked to fill in:

| Field | Required |
|-------|----------|
| Email address | Yes |
| First name | Yes |
| Last name | Yes |
| Street address (Line 1) | Yes |
| Apartment, suite, etc. (Line 2) | No |
| City | Yes |
| State | Yes |
| Postal code | Yes |
| Country | Defaults to US |

All required fields must be filled in before you can continue. If anything is missing, you will see a specific message next to the field that needs attention.

After you submit your address, the system saves it and either finds your existing customer account by email or creates a new guest account.

### Step 3: Apply a Coupon Code (Optional)

If you have a coupon or discount code, you can enter it during checkout. The system validates the code and shows you the discount details if it is accepted.

**What can go wrong with coupon codes:**

- "That code doesn't look right." -- The code was not recognized. Double-check for typos.
- "That code has already been used." -- Single-use codes can only be redeemed once.
- "That code is no longer active." -- The promotion has been turned off.
- "That code has expired." -- The promotion's end date has passed.

If a code is accepted, you will see the discount name and how much you are saving. You can remove an applied coupon if you change your mind.

**Discount types:**

- **Percentage discount** -- Takes a percentage off your subtotal (e.g., 15% off)
- **Fixed amount discount** -- Takes a flat dollar amount off your subtotal (e.g., $5 off). If the discount exceeds your subtotal, it reduces your total to zero (before tax).

Some discounts have a minimum order amount. If your cart does not meet the minimum, the coupon will not apply.

### Step 4: Payment

After your address is saved, the system calculates your final total:

- **Subtotal** -- The total price of all items in your cart
- **Discount** -- Subtracted from the subtotal if a coupon is applied (tax is calculated on the discounted amount)
- **Tax** -- Calculated based on your shipping address and the store's tax configuration
- **Total** -- What you will be charged

Payment is processed securely through Stripe. You will see a credit card form where you enter your card number, expiration date, and security code (CVC). Your card details go directly to Stripe and are never stored on the Rockabilly Roasting website.

Click the payment button to submit your order.

### What Happens During Payment

When you click pay:

1. A Stripe PaymentIntent is created for your exact order total
2. Stripe processes your card
3. If the payment succeeds, the system verifies the charged amount matches your order total
4. Your order is created with all line items, shipping address, and any applied discount
5. Your cart is cleared
6. A confirmation email is queued for delivery
7. You are redirected to the confirmation page

## Order Confirmation

After a successful payment, you land on the order confirmation page. You will see:

- A checkmark icon
- "Thank You for Your Order"
- Your order number (format: ORD-XXXXXXXXXX)
- A note that a confirmation email is on its way
- A "Continue shopping" link back to the catalog

**Save your order number.** You will need it if you contact customer support about your order.

The confirmation page stays available if you refresh your browser, so you do not need to worry about losing it.

## What Happens After You Order

Once your order is confirmed:

1. **Confirmation email** -- You will receive an email at the address you provided during checkout with your order details.
2. **Order processing** -- The Rockabilly Roasting team reviews and prepares your order. Your coffee is roasted fresh.
3. **Fulfillment** -- Your order is packed and shipped. You will be notified when it is on its way.

## Common Issues

### Payment Declined

If your card is declined, Stripe will show an error message in the payment form. Common reasons include:

- Incorrect card number, expiration date, or CVC
- Insufficient funds
- Card issuer declined the transaction (contact your bank)
- Card expired

You can correct the information and try again without losing your cart or address details.

### "Cart is empty"

If you see this error at checkout, your cart may have expired (carts last 30 days) or been cleared. Go back to the catalog, add your items again, and return to checkout.

### "Payment amount does not match order total"

This rare error means the amount Stripe charged does not match what the server calculated. Your payment may need to be reviewed. Contact Rockabilly Roasting at 509-585-2320 or info@rockabillyroasting.com for help.

### Page Loads But Nothing Appears

The checkout uses a JavaScript application that loads separately from the rest of the site. If it does not appear:

- Make sure JavaScript is enabled in your browser
- Try refreshing the page
- Clear your browser cache and try again
- Try a different browser

### Coupon Code Not Working

See the coupon error messages in Step 3 above. If you believe a code should be valid, contact customer support with the code and your order details.

## Tips

- **Your cart persists for 30 days** -- You can leave and come back without losing your items.
- **You do not need an account** -- Guest checkout is fully supported. An account is created automatically using your email address.
- **Secure payment** -- All payment processing happens through Stripe. Your card information is never stored on the Rockabilly Roasting servers.
- **Confirmation email** -- Check your spam folder if you do not see it within a few minutes.
