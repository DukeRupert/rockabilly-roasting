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
