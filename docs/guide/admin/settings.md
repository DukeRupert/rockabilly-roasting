# Settings

Settings is a group of pages, reached from **Settings** in the user menu at the bottom of the sidebar. **The whole section is admin-only** — these are store-wide rules rather than any one department's work, so the menu entry does not appear for fulfillment, finance, catalog, or support logins.

A tab strip across the top moves between the pages:

| Tab | What lives there |
|---|---|
| **Shipping** | Rates, local zip codes, delivery and pickup, the pickup origin address, packaging tare weight |
| **Box presets** | The cartons labels are quoted against |
| **Wholesale** | The default price list unassigned wholesale accounts see |
| **Integrations** | QuickBooks Online |
| **Team** | Invite staff and set roles |

## What needs attention

Any setting that is currently breaking work appears in a **Needs attention** list at the top of *every* Settings page, and the tab that owns the fix carries a count. Each row links straight to the field.

Rows are marked either **Broken** -- something is failing right now, such as a missing origin phone number that makes every label purchase fail -- or **Check**, meaning it will bite soon, such as a QuickBooks connection a fortnight from expiry. Nothing wrong means no list at all; the panel only appears when it has something to say.

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
- **Access expires** -- How many days remain before the OAuth connection needs to be renewed. The display changes colour based on urgency: muted above 30 days, amber at 14--30, rust below 14. From 14 days out it also appears in the Needs attention list on every Settings page, and from 7 days out it is marked Broken -- reconnecting needs someone with the QuickBooks login free, so the warning has to arrive before the day it stops working.

#### Connecting

1. Click **Connect to QuickBooks** (or **Reconnect** if already connected).
2. You will be redirected to Intuit's authorization page.
3. Sign in with your QuickBooks credentials and authorize the connection.
4. You will be redirected back to the Integrations tab with a success or failure message. Failures arrive in a red panel, successes in green -- if the message is red, nothing was saved.

The OAuth flow uses CSRF protection via a signed state cookie. The authorization window is valid for 10 minutes.

#### Invoice items

Every QuickBooks invoice line has to name a product or service to bill against, and that choice decides which income account the money lands in. It is worth agreeing with whoever keeps the books rather than picking the first plausible entry.

The **Invoice items** panel lists the connected company's active items, each shown with the account it posts to:

- **Product lines** -- what every coffee line on the invoice bills against. Required; invoices cannot be created without it.
- **Shipping line** -- optional. Left as *Same as product lines*, shipping bills against the same item.

The panel is marked **Not set** when nothing is billing — neither a choice here nor a fallback configured on the server. Changing it affects new invoices only; invoices already in QuickBooks are untouched.

#### Billing mode

Wholesale billing ships switched off, and a deploy never switches it on.

- **Test mode** (amber badge) -- Wholesale orders are costed exactly as they would be billed and recorded for review, but nothing reaches QuickBooks and no customer is emailed. This is where a new connection starts.
- **Live** (green badge) -- Orders are invoiced in QuickBooks and emailed to the customer.

**Review** opens the list of what would be billed: customer, terms, due date, bill-to address and total for each order. Rows needing attention carry a flag -- most importantly *"No matching QuickBooks customer"*, which means going live would create a duplicate customer in your books rather than billing the one already there. Fix those in QuickBooks (usually by putting the right email on the customer record) before going live.

While in test mode, a weekly digest of the same information is emailed to the staff notification address.

**Go live** asks for confirmation and records who did it. From then on new wholesale orders are billed for real.

Going live does **not** bill orders recorded during test mode -- those were almost certainly invoiced by hand while the trial ran. To bill one deliberately, use **Bill now** on its row in the review list. Check first that it was not already invoiced by hand: the duplicate guard recognises invoices raised by this system, not ones typed into QuickBooks directly, so the customer could otherwise be billed twice.

Switching back to test mode stops new billing immediately. Invoices already in QuickBooks are untouched.

The full connection procedure, including production keys and the trial period, is in the [QuickBooks go-live runbook](../../quickbooks-go-live.md).

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

### If a save is rejected

The shipping form is long, so a rejected save does not throw it away. The page comes back with everything you typed still in place, the offending field outlined in rust with a note under it, and a red banner saying nothing was saved. Fix the marked field and save again.

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
