# Getting Started with Your Wholesale Account

## Receiving Your Approval

After the Rockabilly Roasting team approves your wholesale application, you will receive an email containing a one-time setup link. This link takes you to a page where you create a password for your account.

Keep an eye on the inbox for the email address you used on your application. Check your spam or junk folder if you do not see it within a few hours of approval.

## Setting Up Your Password

Click the link in your approval email. It will take you to the **Set Your Password** page at `/wholesale/setup`. This page requires a valid setup token, which is embedded in the link from your email.

On this page:

1. Enter a password in the **Password** field. Your password must be at least 10 characters long.
2. Re-enter the same password in the **Confirm password** field.
3. Click **Set Password**.

If the passwords do not match, you will see an error message and can try again. If the setup link has expired or has already been used, you will see a message indicating that. In that case, contact the Rockabilly Roasting team to request a new setup link.

Once your password is set successfully, you will see a confirmation screen with a **Sign in** button that takes you to the login page.

## Logging In

Go to the wholesale login page:

```
https://rockabillyroasting.com/wholesale/login
```

You can also reach this page from the wholesale landing page by clicking **Sign in here**, or from the application page by clicking **Sign in**.

On the login page:

1. Enter the **email address** you used on your application.
2. Enter the **password** you created during setup.
3. Optionally check **Remember me** to stay signed in longer.
4. Click **Sign in**.

If your credentials are correct and your account is approved, you will be redirected to the wholesale portal (Quick Order page). If your account is pending approval or has been suspended, you will be redirected to a dedicated status page after login explaining your account's current state. If there is a different problem -- such as a wrong password -- you will see an error message on the login page.

Note: the wholesale login is rate-limited to protect accounts. If you enter incorrect credentials too many times in a short period, you may need to wait before trying again.

## What You Can Do in the Wholesale Portal

Once logged in, you land on the **Quick Order** page. This is the main hub of your wholesale account. From here you can:

- **Browse the full product catalog** -- all active products are listed with their variants, SKUs, and wholesale unit prices.
- **Enter quantities and bulk-add items** -- type quantities next to the variants you want and add everything to your order in one action.
- **Review your order** -- see a detailed breakdown of items, quantities, unit prices, and line totals before submitting.
- **Adjust quantities or remove items** -- change quantities or remove items from your order on the review page.
- **Place orders on invoice** -- submit your order with an optional PO number and notes. No payment is collected at the time of ordering; an invoice is generated after the order is confirmed.

The portal is tailored to wholesale customers. It uses a separate cart from the retail storefront, so your wholesale orders and any retail browsing do not interfere with each other.

## Logging Out

To log out, use the logout action available in the site navigation. This posts to `/wholesale/logout`, ends your session, and redirects you away from the portal.

After logging out, you will need to sign in again to access the wholesale portal. If you checked "Remember me" during login, your session will persist across browser restarts until you explicitly log out or the session expires.
