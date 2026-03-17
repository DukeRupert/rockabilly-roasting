# Catalog Management

This guide covers creating, editing, and organizing products in the Hiri admin panel.

## Product List

Navigate to **Admin > Products** (`/admin/catalog`) to see all products. The list supports:

- **Search** -- Type in the search box to filter products by name (debounced live search).
- **Status tabs** -- Filter by All, Draft, Active, or Archived.
- **Category filter** -- Use the dropdown to show only products in a specific category.
- **Pagination** -- 25 products per page with Previous/Next navigation.

Click a product title or the "Edit" link to open it. Click "Add product" to create a new one.

---

## Creating a New Product

Navigate to **Admin > Products > Add product** (`/admin/catalog/new`).

### Required Fields

- **Title** -- The product name displayed to customers (e.g., "Chop Top Espresso Blend"). Auto-generates the slug as you type.
- **Slug** -- The URL-friendly identifier (e.g., `chop-top-espresso-blend`). Auto-generated from the title, but you can edit it manually. Once edited manually, it stops auto-updating.

### Optional Fields

- **Description** -- Free-text product description shown on the storefront.
- **Category** -- Assign the product to a category (select from dropdown, or leave as "No category").
- **Available on** -- Date when the product becomes purchasable. Leave blank to make it available immediately upon activation.
- **Discontinue on** -- Date when the product is automatically removed from sale. Leave blank to keep it available indefinitely.

All new products are created with **draft** status. After creating the product, you are redirected to the edit page where you can add variants, images, pricing, and attributes.

---

## Editing Products

The product edit page (`/admin/catalog/{id}`) uses a tabbed interface with five sections:

### Details Tab

Contains the same form fields as the create page (Title, Slug, Description, Category, Available on, Discontinue on), plus a **Settings** panel:

- **Active toggle** -- Switches the product between draft and active status. When active, the product is visible on the storefront. Disabled when the product is archived.
- **Subscribable toggle** -- When enabled, customers can subscribe for recurring delivery of this product.
- **Archive / Delete** -- Archive hides the product from the storefront but preserves it in the database. Delete permanently removes it (requires confirmation). Archived products can be unarchived back to draft status.

### Attributes Tab

Manage structured product metadata (origin, roast level, tasting notes, etc.) through attribute sets. See the [Attributes](#product-attributes) section below for details.

### Media Tab

Upload and manage product images. See the [Images](#uploading-and-managing-product-images) section below.

### Variants Tab

Define product options and their purchasable combinations. See the [Options](#product-options) and [Variants](#managing-variants) sections below.

### Pricing Tab

Set base prices and customer-group-specific prices for each variant. See the [Pricing](#variant-pricing) section below.

On mobile, the tabs appear as a dropdown selector instead of a tab bar.

---

## Product Status Lifecycle

Products move through three statuses:

| Status | Storefront Visibility | How to Enter |
|---|---|---|
| **Draft** | Hidden | Default for new products. Also reached by toggling Active off, or unarchiving. |
| **Active** | Visible | Toggle the Active switch on in the Settings panel. |
| **Archived** | Hidden | Click the "Archive" button in the Settings panel. |

- **Draft to Active** -- Toggle the Active switch on.
- **Active to Draft** -- Toggle the Active switch off.
- **Draft or Active to Archived** -- Click "Archive" in the Settings panel.
- **Archived to Draft** -- Click "Unarchive" in the Settings panel.
- The Active toggle is disabled while a product is archived. You must unarchive first.

---

## Product Options

Options define the axes of variation for a product (e.g., Size, Grind). Options must be created before you can add variants.

### Creating an Option

1. Go to the **Variants** tab on a product.
2. In the **Options** panel, enter a name (e.g., "Grind" or "Size") in the "Add option" form.
3. Click "Add option."

### Adding Option Values

After creating an option, add values to it:

1. Under the option name, enter a value in the text field (e.g., "Whole Bean", "Medium Grind", "Fine Grind").
2. Click "Add value."
3. Repeat for each value.

Values appear as tags. Click the X on a tag to delete that value.

### Deleting an Option

Click "Delete option" next to the option name. This removes the option and all its values. You will be prompted to confirm.

---

## Managing Variants

Variants represent the specific purchasable items (SKUs) for a product. Each variant is a unique combination of option values.

### Creating a Variant

1. Go to the **Variants** tab.
2. In the "Add variant" form at the bottom:
   - Select a value for each defined option (e.g., Grind = "Whole Bean").
   - **SKU** -- Auto-generated from the category, product title, and selected option values (e.g., `COF-CHOPTOP-WB`). You can override it manually; once edited, it stops auto-updating.
   - **Barcode** -- Optional. Enter a UPC or other barcode if applicable.
   - **Weight (oz)** -- The shipping weight in ounces. Used for shipping rate calculation.
   - **Default** -- Check this to make the variant the default selection on the storefront.
3. Click "Add."

The system prevents creating duplicate variant combinations (same set of option values).

### Variant Table

The variants table shows:

- **SKU** -- With a "Default" badge on the default variant.
- **Options** -- The selected option values for this variant, shown as tags.
- **Base Price** -- The retail price (set on the Pricing tab).
- **Weight** -- Displayed in ounces.
- **Delete** -- Removes the variant (requires confirmation).

---

## Variant Pricing

The **Pricing** tab provides inline price editing for all variants.

### Base Prices

Each variant has a base price in USD. To set or change it:

1. Go to the **Pricing** tab.
2. In the "Base Prices" card, enter the dollar amount in the input next to each SKU.
3. The price saves automatically when you change the value (no submit button needed).

Prices are entered in dollars (e.g., `14.99`) and stored internally in cents.

### Group Prices

If customer groups exist (e.g., Wholesale), additional pricing cards appear below the base prices. Each card shows the group name and lets you set per-variant prices for that group.

- Enter a price to set a group-specific price for that variant.
- Clear the price field to remove the group price (the customer will see the base price instead).

If no customer groups exist, a link to create them is shown.

---

## Uploading and Managing Product Images

The **Media** tab handles product photography.

### Uploading Images

Images upload directly to cloud storage (R2) from your browser -- they do not pass through the application server.

1. Go to the **Media** tab.
2. Either click the upload zone or drag and drop files onto it.
3. Supported formats: PNG, JPG, GIF (up to 10MB each).
4. Multiple files can be uploaded at once. A progress indicator shows "Uploading 1 of 3..." during batch uploads.

### Image Order

The **first image** in the gallery is the primary image, shown as the product thumbnail across the storefront. It is labeled with a "Primary" badge.

Images can be reordered via the reorder endpoint (`POST /admin/catalog/{id}/images/reorder`) by passing an ordered list of image IDs.

### Deleting Images

Hover over an image to reveal the delete button (red X in the top-right corner). Click it and confirm to delete. The image is removed from the database and a background job cleans up the file from cloud storage.

---

## Product Attributes

Attributes store structured product metadata that goes beyond the basic title and description. For a coffee roaster, this includes origin, roast level, tasting notes, processing method, and similar specs.

Attributes work through a two-level system: **attribute sets** contain **attribute keys**.

### Assigning Attribute Sets to a Product

1. Open a product and go to the **Attributes** tab.
2. Use the "Assign attribute set" dropdown to select an available set (e.g., "Coffee Profile").
3. Click "Assign."
4. The set's keys appear as form fields. Fill in the values.
5. Click "Save attributes" to persist all values at once.

To remove a set from a product, click "Remove set" in the set's header. This removes the set assignment and any saved values for its keys.

### Attribute Value Types

When filling in attribute values, the form field type depends on the key's configured value type:

| Type | Input | Example |
|---|---|---|
| **Text** | Free text field | Origin: "Huila, Colombia" |
| **Enum** | Dropdown select | Roast Level: "Medium" |
| **Multi Text** | Comma-separated text | Tasting Notes: "chocolate, cherry, caramel" |
| **Multi Enum** | Checkboxes | Processing: select from "Washed", "Natural", "Honey" |
| **Boolean** | Checkbox | Single Origin: yes/no |

---

## Attribute Sets

Manage attribute sets at **Admin > Attribute Sets** (`/admin/attributes`).

### Creating an Attribute Set

1. At the bottom of the attribute sets page, fill in:
   - **Name** -- e.g., "Coffee Profile"
   - **Slug** -- Auto-generated from the name if left blank. Used internally.
   - **Order** -- Controls display order (lower numbers appear first).
2. Click "Create."

### Editing an Attribute Set

Click the set name or "Edit" to open the edit page (`/admin/attributes/{id}`). From here you can:

- Update the set's name, slug, and order.
- Manage the set's attribute keys (see below).
- Delete the entire set (danger zone at the bottom -- this removes the set, all its keys, and all product values using those keys).

### Managing Attribute Keys

Keys are the individual attributes within a set.

#### Adding a Key

On the attribute set edit page, use the "Add key" form:

- **Name** -- The display name (e.g., "Tasting Notes").
- **Type** -- One of: Text, Enum, Multi Text, Multi Enum, Boolean.
- **Order** -- Controls display order within the set.
- **Filterable** -- Check if customers should be able to filter products by this attribute on the storefront.
- **Sortable** -- Check if customers should be able to sort by this attribute.
- **Allowed values** -- Comma-separated list of valid values. Required for Enum and Multi Enum types (e.g., "light, medium, medium-dark, dark").

#### Editing a Key

Click "Edit" next to a key in the table to open the key edit page (`/admin/attributes/{setID}/keys/{keyID}`). All fields can be updated.

#### Deleting a Key

Click "Delete" next to a key in the table. Requires confirmation. This removes the key and any product values stored for it.

---

## Categories

Manage categories at **Admin > Categories** (`/admin/categories`).

Categories (called "taxons" internally) organize products into groups. Products can belong to one category.

### Creating a Category

Use the form on the right side of the categories page:

- **Name** -- The display name (e.g., "Single Origin").
- **Slug** -- URL-friendly identifier (e.g., `single-origin`). Required.
- **Position** -- Controls sort order in navigation (lower numbers appear first, default 0).

### Editing a Category

Click "Edit" next to a category in the table. The form on the right switches to edit mode, pre-filled with the current values. Make changes and click "Save changes." Click "Cancel" to return to create mode.

### Deleting a Category

Click "Delete" next to a category. You will be prompted to confirm. Products in the deleted category become uncategorized -- they are not deleted.

---

## Typical Workflow

1. **Create categories** -- Set up categories like "Single Origin", "Blends", "Decaf" at `/admin/categories`.
2. **Create attribute sets** -- Define a "Coffee Profile" attribute set with keys like Origin, Roast Level, Tasting Notes, Processing Method at `/admin/attributes`.
3. **Create a product** -- Add title, description, and assign a category at `/admin/catalog/new`.
4. **Add options** -- On the Variants tab, create options like "Size" (with values "12 oz", "2 lb", "5 lb") and "Grind" (with values "Whole Bean", "Drip", "Espresso", "French Press").
5. **Add variants** -- Create a variant for each purchasable combination (e.g., 12oz Whole Bean, 12oz Drip, 2lb Whole Bean).
6. **Set prices** -- On the Pricing tab, enter the base price for each variant. Add group prices for wholesale customers if applicable.
7. **Upload images** -- On the Media tab, upload product photos. The first image becomes the primary.
8. **Add attributes** -- On the Attributes tab, assign the "Coffee Profile" set and fill in origin, roast level, tasting notes.
9. **Activate** -- On the Details tab, toggle the Active switch to make the product visible on the storefront.
