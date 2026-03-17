# Shopping Guide

This guide walks you through browsing, selecting, and adding coffee to your cart at Rockabilly Roasting Co.

---

## The Home Page

When you visit the site, you land on the home page. Here is what you will find:

- **Hero section** -- A full-width banner with the Rockabilly Roasting Co. name, a short tagline, and two buttons: "Shop Coffee" (takes you to the catalog) and "The Daily Grind" (takes you to subscriptions).
- **Popular Roasts** -- A grid of featured products, up to four across on a wide screen. Each card shows the product image, name, price, roast level dots, and tasting note tags. Click any card to go to that product's detail page. A "View All" link takes you to the full catalog.
- **Subscription callout** -- A banner encouraging you to subscribe for recurring delivery at a discount. Click "Join the Daily Grind" to explore subscription options.
- **Testimonials** -- Real customer quotes near the bottom of the page.

## Browsing the Catalog

Click "Shop" in the top navigation bar (or "Shop Coffee" on the home page) to open the catalog at `/catalog`.

### The Product Grid

Products appear in a grid -- two columns on a phone, three on a laptop, and four on a wide screen. Each product card shows:

- **Product image** (or a placeholder with the first letter of the product name if no image is available)
- **Badges** -- A gold "Seasonal" badge in the top-left corner if the coffee is seasonal, and a teal "Decaf" badge in the top-right if applicable
- **Product name** -- Turns red on hover
- **Starting price** -- The base price of the default variant
- **Roast level** -- A row of small dots from light (amber) to dark (red) showing where this coffee falls on the roast spectrum
- **Tasting notes** -- Up to three small tag pills (e.g., "Chocolate," "Berry," "Caramel")

Click any product card to go to its detail page.

### Category Filters

If the catalog has categories (called taxons), a row of pill-shaped buttons appears above the grid. Click a category name to filter the grid to that category. Click "All" to remove the category filter.

### Search

A search box appears in the top-right corner of the catalog page. Type a search term and results update automatically after a short pause. Clearing the search box resets all filters and shows the full catalog again.

### Attribute Filters

A sidebar on the left (on desktop) or a filter drawer (on mobile) lets you narrow results by product attributes:

- **Roast Level** -- A clickable row of five dots from light to dark. Click a dot to show only coffees at that roast level. Click the same dot again to clear the filter.
- **Checkbox filters** -- For attributes like origin or process, check one or more values to filter the grid. Click a checked value to uncheck it.
- **Boolean filters** -- Toggle-style filters for things like "Fair Trade" or "Organic."
- **Clear All** -- Removes all active attribute filters at once.

On mobile, tap the "Filters" button to open the filter drawer from the bottom of the screen. It works the same way as the desktop sidebar. Tap "Close" or the backdrop to dismiss it.

**Active filter tags** appear above the product grid showing exactly which filters are applied. Click the "X" on any tag to remove that individual filter.

### Pagination

If there are more products than fit on one page, page numbers appear below the grid. Click a number or the arrow buttons to move between pages. Changing a filter or search term resets you back to page 1.

## Product Detail Page

Click a product card to open its detail page at `/catalog/{product-slug}`.

### Layout

The page is split into two columns on wide screens:

- **Left column** -- Product information and purchase controls (this appears first so you see the name, price, and buy button without scrolling)
- **Right column** -- Product image with thumbnail gallery below it (if the product has multiple images, click a thumbnail to view it)

A breadcrumb trail at the top ("Shop > Product Name") lets you navigate back to the catalog.

### Product Information

From top to bottom, the left column shows:

1. **Product name** in large display type
2. **Origin and roast level** -- Where the coffee comes from (country, region) and a visual roast scale with labeled dots
3. **Price** -- Large and prominent; updates automatically when you select different options
4. **Tasting notes** -- Tag pills listing flavor notes (e.g., "Dark Chocolate," "Stone Fruit," "Honey")
5. **Description** -- A short paragraph about the coffee

### Selecting Options

If a product has options like grind type or bag size, they appear as groups of pill-shaped buttons below the description.

- The first value in each group is selected by default
- Click a different pill to select it -- the selected pill gets an amber border
- When you change options, the displayed price updates to match the selected variant
- The system automatically finds the variant that matches your combination of selected options

For example, a coffee might have:
- **Grind:** Whole Bean, Drip, Espresso, French Press
- **Size:** 12 oz, 2 lb, 5 lb

Selecting "Espresso" and "2 lb" shows the price for that specific combination.

### How Pricing Works

Each combination of options (called a "variant") has its own price. The price displayed on the product card in the catalog is the base price of the default variant (usually the smallest or most common option). When you select different options on the detail page, the price updates in real time to show the exact price for your selection.

### One-Time Purchase vs. Subscription

If a product is available as a subscription, you will see two tabs above the buy button:

- **One-Time** -- Buy a single bag (selected by default)
- **Subscribe** -- Set up recurring delivery

Selecting "Subscribe" reveals frequency options (e.g., "Every Week," "Every 2 Weeks," "Every Month") and a quantity selector. Each frequency shows its discount percentage (e.g., "Save 10%"). The price display updates to show the original price crossed out and the discounted price.

Click "Subscribe & Save" to proceed to the subscription signup flow.

### Adding to Cart

Click the red "Add to Cart" button to add the selected variant to your cart. You do not need to be signed in to add items.

- A quantity of 1 is added by default for one-time purchases
- For subscriptions, you choose quantity with the +/- controls before clicking "Subscribe & Save"
- After adding, the cart badge in the header updates to show your new item count

An "In stock and ready to ship" indicator appears below the buy button.

## Your Cart

Click the cart icon in the top-right corner of any page to go to your cart at `/cart`.

### Cart Contents

If your cart has items, you will see a table with:

| Column | Description |
|--------|-------------|
| Product | Product name and SKU code |
| Price | Unit price for this variant |
| Quantity | A number input with an "Update" button |
| Total | Line total (unit price times quantity) |
| Remove | A "Remove" link to delete the item |

### Updating Quantities

Change the number in the quantity field and click "Update" to recalculate the line total and subtotal. The minimum quantity is 1.

### Removing Items

Click "Remove" next to any item to take it out of your cart. The cart updates immediately.

### Subtotal and Next Steps

Below the item list you will see:

- **Subtotal** -- The total before shipping and tax
- A note that shipping and taxes are calculated at checkout
- A red **Checkout** button to proceed
- A "Continue shopping" link back to the catalog

If your cart is empty, you will see a message with a link back to the catalog.

## Cart Icon and Badge

The cart icon appears in the site header on every page. When your cart has items, a small red circle (badge) on the icon shows the total number of items. This count updates automatically when you add, update, or remove items -- you do not need to refresh the page.

Your cart is stored in a browser cookie that lasts 30 days. If you close your browser and come back later, your cart will still be there.
