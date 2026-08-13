# Settings

The Settings page lets you manage platform integrations and configuration. Navigate to **Admin > Settings** in the sidebar.

## Integrations

### QuickBooks Online

The QuickBooks integration connects your store to QuickBooks Online for B2B wholesale invoicing and ACH payment collection.

#### Connection Status

The QuickBooks card shows the current connection state:

- **Connected** (green badge) -- Your store is linked to a QuickBooks company.
- **Not connected** (grey badge) -- No active connection.

If QuickBooks environment variables (`QB_CLIENT_ID` and related keys) are not configured on the server, the card will display a notice explaining that the integration is not available.

#### When Connected

The card displays:

- **Company ID (Realm)** -- The QuickBooks company identifier your store is linked to.
- **Refresh token expires** -- How many days remain before the OAuth connection needs to be renewed. The display changes color based on urgency:
  - Green/muted text: more than 30 days remaining.
  - Amber text: 14--30 days remaining.
  - Red text: fewer than 14 days remaining, with a warning that you should reconnect soon to avoid billing interruption.

#### Connecting

1. Click **Connect to QuickBooks** (or **Reconnect** if already connected).
2. You will be redirected to Intuit's authorization page.
3. Sign in with your QuickBooks credentials and authorize the connection.
4. You will be redirected back to the Settings page with a success or failure message.

The OAuth flow uses CSRF protection via a signed state cookie. The authorization window is valid for 10 minutes.

#### Disconnecting

1. Click **Disconnect** on the QuickBooks card.
2. Confirm the action in the browser dialog.
3. The connection is removed and automated B2B invoicing stops until you reconnect.

#### What QuickBooks Syncs

The integration handles:

- Creating and syncing customer records in QuickBooks.
- Creating invoices for wholesale/B2B orders.
- Syncing payment records.

These sync operations are tracked in the audit log under actions prefixed with `qb.*` (e.g., `qb.customer_created`, `qb.invoice_created`, `qb.payment_synced`).

#### Reconnecting

QuickBooks OAuth tokens expire periodically. If the refresh token is close to expiring (shown on the settings page), click **Reconnect** to re-authorize. This replaces the existing credentials without losing your company link.

## Shipping

### Local delivery schedule

Two controls decide which day a local-delivery order is promised:

- **Delivery days** -- checkboxes for the days the van actually runs. This is not display copy: it is the schedule the system computes each order's delivery date from. At least one day is required while local delivery is switched on.
- **Order-by cutoff** -- the time on a delivery day after which an order rolls to the next run. Times are in the merchant timezone (`MERCHANT_TIMEZONE`, currently `America/Los_Angeles`).

The rule, with the default Monday/Thursday and a 9:00am cutoff:

| Order placed | Goes out |
|---|---|
| Monday 8:59am | Monday |
| Monday 9:00am | Thursday |
| Tuesday, any time | Thursday |
| Thursday 9:01am | the following Monday |
| Saturday | Monday |

An order placed on a day the van doesn't run always rolls to the next scheduled day, whatever the hour -- there's no run that day to miss.

Below the controls, the page echoes back the exact sentence customers see, so you can confirm the rule reads the way you meant without opening the storefront.

**Changing the schedule does not move orders already placed.** Each order's date is frozen when it is placed, because it has already been sent to the customer in their confirmation email. If you need to move an existing order, do it on the order itself.

### Free pickup at the shop

Turning **Free pickup at the shop** on does two things: it offers pickup at checkout, and it lets the confirmation email offer a customer facing a wait the option to collect instead (see below).

With pickup **off**, local-delivery confirmations still show the delivery date, but the switch offer is replaced with "reply to this email" -- the system will not offer something the shop can't do.

### The switch-to-pickup link

Every local-delivery confirmation email tells the customer their delivery date and, when pickup is enabled, offers a one-click link to switch to collecting at the shop instead.

- The link is signed and expires after 14 days.
- It works straight from the inbox with no login, and authorizes exactly one change to exactly one order -- it never signs anyone in.
- Clicking it opens a confirmation page; nothing changes until the customer presses the button. (Corporate mail scanners fetch every link in an incoming email, so acting on the click alone would let a customer's IT department silently cancel their delivery.)
- It stops working once the order is packed, dispatched, or cancelled. The customer gets a page telling them to reply to the shop instead.
- Switches are recorded in the audit log as `order.switched_to_pickup`, attributed to the customer, and record the delivery date the order gave up.

Local delivery and pickup are both free, so the switch never changes what was charged.

**Requires a signing secret.** The link is signed with `ORDER_ACTION_SECRET`, falling back to `UNSUBSCRIBE_SECRET` if that isn't set. With neither set, the server warns at boot and confirmation emails ask the customer to reply instead of printing a link that couldn't be verified.
