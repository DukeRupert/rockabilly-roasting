# Wholesale Management

This guide covers the wholesale application review, account management, customer groups, and group pricing features in the admin panel.

## Wholesale Applications List

Navigate to **Admin > Wholesale** to view wholesale applications and accounts. The page defaults to showing pending applications.

### Status Filters

Three filter buttons at the top let you switch between views:

- **Pending** -- new applications awaiting review
- **Approved** -- active wholesale accounts
- **Suspended** -- accounts that have been temporarily deactivated

The active filter is highlighted. Each filter shows its own paginated list.

### Columns

| Column | Description |
|--------|-------------|
| Company | The applicant's company name |
| Contact | First and last name |
| Email | Contact email address |
| Terms | Payment terms if set (e.g., "Net 30"), or a dash |
| Applied | Date the application was submitted |
| Status | Badge showing pending, approved, or suspended |

### Actions Column

The rightmost column shows context-sensitive action buttons and a View link to the customer detail page:

- **Pending applications** -- Approve and Decline buttons
- **Approved accounts** -- Suspend button
- **Suspended accounts** -- Reactivate button

---

## Reviewing Applications

### Approving an Application

Click the **Approve** button next to a pending application (available on both the wholesale list and the customer detail page). This:

1. Sets the wholesale status to `approved`
2. Records the approving staff member and timestamp
3. Sends a welcome/setup email to the customer so they can create their account password
4. If QuickBooks is connected, creates a corresponding QB customer record

The customer can then log in, access the wholesale portal, and place orders at group pricing.

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

## Customer Groups

Navigate to **Admin > Customer Groups** (or click "Manage groups" from the Group Pricing page). Groups represent wholesale pricing tiers -- for example, "Wholesale Tier 1", "Wholesale Tier 2", or "Restaurant Partners".

### Creating a Group

Enter a name in the "New group" field and click **Create group**. The group appears in the table immediately. Group names should be descriptive of the pricing tier or customer segment.

### Deleting a Group

Click **Delete** next to any group in the table. A confirmation dialog warns that customers will be removed from the group. Deleting a group also removes all group-specific pricing overrides associated with it.

### Group Table

The table shows:

- **Name** -- the display name for the group
- **ID** -- the internal UUID (useful for API integrations or debugging)

### Assigning Customers to Groups

Customers are assigned to groups from the individual customer detail page. See the [Customer Groups section](customers.md#customer-groups) in the Customers guide.

---

## Group Pricing Matrix

Navigate to **Admin > Customer Groups > Manage prices** (or directly to `/admin/groups/prices`). This page shows a pricing matrix for every product variant in the catalog.

### How the Page is Organized

The page lists each product by name. Under each product, pricing is split into stacked cards:

1. **Base Prices** -- the standard retail price for each variant (applies to all customers not in a group)
2. **[Group Name] Prices** -- one card per customer group, showing the override price for that group

Each card is a table with two columns:

| Column | Description |
|--------|-------------|
| SKU | The variant's SKU code, with a "Default" badge on the default variant |
| Unit Price | An editable dollar input with a Save button |

### Setting Base Prices

In the "Base Prices" card, enter a dollar amount (e.g., `14.99`) next to a variant's SKU and click **Save**. This is the price all non-grouped customers see.

### Setting Group Prices

In any group's pricing card (displayed in teal text), enter a dollar amount and click **Save**. This price overrides the base price for customers in that group.

To remove a group price override (reverting to the base price), clear the input field and click **Save**.

### How Wholesale Pricing Works

When a wholesale customer browses the catalog or places an order, the system resolves pricing in this order:

1. Check if the customer belongs to any customer group
2. If yes, look for a group-specific price for the variant
3. If a group price exists, use it; otherwise, fall back to the base price
4. If no base price exists, the variant cannot be purchased

This means you can set discounted prices for specific wholesale tiers without affecting retail pricing. A customer in "Wholesale Tier 1" might pay $10.00 for a bag that retails at $14.99.

### Navigating Between Groups and Prices

- From the Groups page, click **Manage prices** to go to the pricing matrix
- From the Pricing page, click **Manage groups** to return to group management
- From any product's pricing card, click **Edit product** to go to the catalog editor for that product

---

## Related Pages

- [Customers](customers.md) -- customer detail, group assignment, billing settings
- [Invoices](invoices.md) -- B2B invoice lifecycle and payment recording
