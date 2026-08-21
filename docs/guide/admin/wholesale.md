# Wholesale Management

This guide covers wholesale application review and account management in the admin panel.

## Wholesale Applications List

Navigate to **Admin > Customers** and switch the channel toggle to **Wholesale**. (The old `/admin/wholesale` URL redirects here.) A count on the Wholesale segment shows how many applications are waiting, so the queue is visible from the retail side too.

### Application filters

The **Application** pills narrow the list by status, each with a count:

- **Pending** -- new applications awaiting review
- **Approved** -- active wholesale accounts
- **Suspended** -- accounts that have been temporarily deactivated
- **Declined** -- rejected applications

With no status pill active, every wholesale account is listed and a Status column shows where each one stands.

### Columns

See [Customers > Wholesale columns](customers.md#wholesale-columns) for the full list. The rightmost column carries the context-sensitive actions plus a View link to the customer detail page:

- **Pending applications** -- Approve and Decline buttons
- **Approved accounts** -- Suspend button
- **Suspended accounts** -- Reactivate button

On narrower screens the actions collapse into a kebab menu.

---

## Reviewing Applications

### Approving an Application

Click the **Approve** button next to a pending application (available on both the wholesale list and the customer detail page). This:

1. Sets the wholesale status to `approved`
2. Records the approving staff member and timestamp
3. Sends a welcome/setup email to the customer so they can create their account password
4. If QuickBooks is connected, creates a corresponding QB customer record

The customer can then log in, access the wholesale portal, and place orders at their price list.

### Declining an Application

Click the **Decline** button next to a pending application. A confirmation dialog appears. When confirmed:

1. The application status remains on the customer record with internal notes
2. An audit log entry is created

Declined applications are not shown in any filter tab. The customer record remains in the main customer list.

---

## Managing Approved Accounts

### Suspending an Account

From the wholesale list (Approved tab) or the customer detail page, click **Suspend**. A confirmation dialog appears. When confirmed:

1. The wholesale status changes to `suspended`
2. A notification email is sent to the customer
3. The customer can no longer access the wholesale portal or place wholesale orders

### Reactivating an Account

From the wholesale list (Suspended tab), click **Reactivate**. A confirmation dialog appears noting that a new setup email will be sent. When confirmed:

1. The wholesale status changes back to `approved`
2. Internal notes are cleared
3. A welcome/setup email is sent so the customer can regain access

---

## Wholesale Pricing

Wholesale pricing is set with **price lists**, under **Catalog > Pricing**. A price list is a named set of per-variant overrides on the base price; a customer assigned to one pays those prices, and anything the list does not override falls back to base.

- Assign a customer's list from the Price list column here (while their application is pending) or from **Billing Settings** on their detail page.
- Edit a list's prices by opening it under **Catalog > Pricing**, or edit one coffee's prices across every list on that product's **Pricing** tab.
- Volume breaks (e.g. 24+ at a lower price) are set per product, per list, via the **Breaks** link in a list's column.

See the [Catalog guide](catalog.md#variant-pricing) for the pricing grid itself.

Customer groups have been removed. They had not been a pricing mechanism since v1.54.0, and nothing used them for product access — the wholesale visibility tier covers the channel, and `private` visibility covers per-customer white-labelling.

---

## Related Pages

- [Customers](customers.md) -- customer detail and billing settings
- [Invoices](invoices.md) -- B2B invoice lifecycle and payment recording
