# Customers

This guide covers the customer management screens in the admin panel.

## Customer List

Navigate to **Admin > Customers** to view customer accounts. A **Retail | Wholesale** toggle at the top switches channels — the same list of people, scoped to one side of the business, with the columns and actions that side needs. A count on the Wholesale segment shows how many applications are waiting for review.

### Retail columns

| Column | Description |
|--------|-------------|
| Name | First and last name, linked to the customer detail page |
| Email | Customer's email address |
| Status | Email verification (`verified` / `unverified`) |
| Joined | Date the account was created |

### Wholesale columns

| Column | Description |
|--------|-------------|
| Company | Company name (falls back to the contact's name), linked to the detail page |
| Contact | The person on the account |
| Email | Login/contact email |
| Terms | Payment terms (e.g. Net 7), or a dash if unset |
| Applied | Date the application came in |
| Price list | Assigned price list — an editable selector while the application is pending |
| Status | Application status, when no status filter is active |
| Actions | Approve, Decline, Suspend or Reactivate, plus View |

Wholesale also gets an **Application** filter row (Pending / Approved / Suspended / Declined) with counts.

The old `/admin/wholesale` page redirects here.

### Filters

The **Email** pills narrow to verified or unverified accounts on either channel.

### Search

The search box at the top filters customers by name, email, or company. Results update automatically as you type (with a short delay). The search term is preserved in the URL, so you can bookmark or share filtered views.

If a search finds nothing, closest matches appear under "Did you mean" — these ignore the filters *and* the channel, so searching the retail side for a cafe still finds it.

### Pagination

Results are shown 25 per page. Use the Previous and Next links at the bottom to navigate. The counter between them shows the current range and total (e.g., "1-25 of 142").

---

## Customer Detail

Click any customer row to open their detail page. The header shows:

- Full name
- Account type badge: **B2C** (retail) or **B2B** (wholesale)
- Email address
- Company name (wholesale accounts only)

### Info Cards

Three summary cards appear below the header:

- **Email verified** -- whether the customer has confirmed their email address
- **Account type** -- `retail` or `wholesale`
- **Tax exempt** -- whether the customer is exempt from sales tax, with the reason if one was provided

### Details Section

Shows the customer's phone number (if provided) and the date they became a customer.

### Wholesale Application Banner

For wholesale customers, a banner shows the current application status with available actions:

- **Pending** -- Approve or Decline buttons
- **Approved** -- Suspend button
- **Suspended** -- no action buttons on this page (use the Wholesale list to reactivate)

See the [Wholesale guide](wholesale.md) for more on application management.

---

## Billing Settings (Wholesale Only)

This section only appears for wholesale (B2B) customers. It contains two settings that auto-save when changed.

### Payment Terms

Controls the net payment window for invoices. Select from the dropdown:

- **Not set** -- no specific terms configured
- **Net 7** -- payment due within 7 days
- **Net 15** -- payment due within 15 days
- **Net 21** -- payment due within 21 days
- **Net 30** -- payment due within 30 days

The selected value is saved immediately when you change the dropdown. The system uses this value when generating invoice due dates.

### Billing Method

Controls how the customer is expected to pay. Options:

- **Manual** -- the customer pays offline (check, cash, wire transfer). You record payments manually against invoices.
- **ACH** -- the customer pays via ACH bank transfer.
- **Credit Card** -- the customer pays via credit card.

Like payment terms, this saves immediately on change.

---

## Addresses

The Addresses section shows all shipping/billing addresses on file for the customer, displayed as cards in a two-column grid.

Each address card shows:

- Name (may differ from account name for gift addresses)
- Company (if provided)
- Street address (line 1 and line 2)
- City, state, and postal code
- Country code
- A **default** badge if this is the customer's default address

Addresses are managed by the customer through their account. The admin view is read-only.

---

## Related Pages

- [Wholesale Management](wholesale.md) -- application review and account management
- [Invoices](invoices.md) -- B2B invoice lifecycle and payment recording
