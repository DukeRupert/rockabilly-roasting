# Customers

This guide covers the customer management screens in the admin panel.

## Customer List

Navigate to **Admin > Customers** to view all customer accounts. Both retail (B2C) and wholesale (B2B) customers appear in this single list.

### Columns

| Column | Description |
|--------|-------------|
| Name | First and last name, linked to the customer detail page |
| Email | Customer's email address |
| Status | Badges indicating email verification and account type (`verified`, `wholesale`) |
| Joined | Date the account was created |

### Search

The search box at the top filters customers by name or email. Results update automatically as you type (with a short delay). The search term is preserved in the URL, so you can bookmark or share filtered views.

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

## Customer Groups

Groups are used to assign tiered wholesale pricing. A customer can belong to multiple groups simultaneously.

### Viewing Group Memberships

Current group memberships appear as badges below the "Customer Groups" heading. Each badge shows the group name with an X button to remove the customer from that group.

If the customer is not in any groups, the message "Not in any groups" appears.

### Adding a Customer to a Group

If groups exist that the customer is not already a member of, a dropdown labeled "Add to group" appears. Select a group and click **Add**. The page reloads with the new membership.

If no groups have been created yet, a link to **Create one** directs you to the Customer Groups management page (`/admin/groups`).

### Removing a Customer from a Group

Click the X button on any group badge. The customer is removed immediately (no confirmation dialog).

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

- [Wholesale Management](wholesale.md) -- application review, groups, and pricing
- [Invoices](invoices.md) -- B2B invoice lifecycle and payment recording
