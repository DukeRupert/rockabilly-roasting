# Placing Wholesale Orders

## The Quick Order Page

After logging in, you arrive at the **Quick Order** page (`/wholesale/portal`). This page displays a welcome message with your company name and lists every active product in the catalog.

At the top of the page you will find:

- A **Review Order** button that takes you to the Review Order page to see what is currently in your order.

Below that is the quick-order form with all available products.

## Browsing Products

Products are displayed in cards, each showing:

- **Product image** (or a placeholder if no image is available)
- **Product title**
- A **variant table** with columns for:
  - **Code** -- the SKU for that variant
  - **Option columns** -- variant attributes like size, grind, roast level, etc.
  - **Unit Price** -- the wholesale price per unit, shown in dollars. If a minimum order quantity applies, it is noted next to the price (e.g., "min 5").
  - **Qty** -- a quantity input field where you enter how many units you want
  - **Stock** -- whether the variant is currently in stock or out of stock. Note: inventory tracking is not yet live, so all items currently show as "In Stock."

The variant table gives you a complete view of every purchasable option for each product on a single page, so you can build your entire order without navigating between product detail pages.

## Adding Items to Your Order

To add items:

1. Scan through the product tables and enter quantities in the **Qty** field next to each variant you want to order. Leave the field empty or at zero for variants you do not need.
2. Some variants have a minimum order quantity or must be ordered in multiples (e.g., cases of 6). The quantity input enforces the correct step size, and the minimum is noted next to the unit price.
3. You can enter quantities for multiple products and variants at the same time -- there is no need to add one product at a time.
4. When you are ready, scroll to the bottom of the page and click the **Add to Order** button in the sticky bar.

All items with a quantity greater than zero are added to your wholesale cart at once. After adding, you are automatically redirected to the checkout review page.

## Reviewing Your Order

The **Review Order** page (`/wholesale/checkout`) shows everything currently in your wholesale cart:

| Column     | Description                                    |
|------------|------------------------------------------------|
| Product    | The product name                               |
| SKU        | The variant code                               |
| Unit Price | Price per unit                                 |
| Qty        | Current quantity (editable)                    |
| Total      | Line total (unit price multiplied by quantity) |

At the bottom of the table, the **Subtotal** shows the total for all line items.

If your order is empty, you will see a message with a link back to the Quick Order page.

## Updating Quantities

On the review page, you can change the quantity of any item directly in the **Qty** column. When you change a number and move to the next field (or press Enter), the page updates automatically to reflect the new quantity and recalculated line total and subtotal. There is no need to click a separate "update" button -- the change takes effect immediately.

## Removing Items

Each line item has a remove button (an X icon) on the right side of the row. Click it to remove that item from your order. The page updates immediately to show the remaining items and the recalculated subtotal.

## Placing Your Order

Below the order summary table, you will find two optional fields:

- **PO Number** -- enter your internal purchase order number if your business uses them for tracking. This is optional but will appear on your invoice if provided.
- **Order Notes** -- add any special instructions for this order, such as delivery timing preferences or packaging requests.

When you are satisfied with your order:

1. Review the items and subtotal one more time.
2. Fill in the PO number and notes if needed.
3. Click **Place Order**.

A note below the button confirms: "An invoice will be sent after your order is confirmed. No payment is collected now."

## How Wholesale Checkout Differs from Retail

Wholesale orders work differently from retail purchases in several important ways:

- **No immediate payment.** Retail customers pay by credit card at checkout through Stripe. Wholesale orders are placed on invoice -- you order now and pay later according to your agreed terms.
- **No shipping or tax calculated at checkout.** The review page shows a subtotal only. Shipping and tax details are handled on the invoice.
- **PO number support.** Wholesale orders can carry your internal purchase order number for easier reconciliation with your accounting.
- **Separate cart.** Your wholesale cart is completely independent of the retail cart. You can browse the retail storefront without affecting your wholesale order, and vice versa.
- **Order numbers.** Wholesale orders are prefixed with "WO-" to distinguish them from retail orders.

## After You Place an Order

Once you click **Place Order**, the following happens:

1. The system creates a confirmed order with a payment status of **pending invoice**.
2. Your wholesale cart is cleared.
3. You are redirected back to the Quick Order page, ready to place another order if needed.
4. If QuickBooks integration is active, the system automatically queues a job to sync your customer record and generate an invoice in QuickBooks.
5. You will receive an invoice (via email or through your agreed invoicing channel) with payment terms. Pay according to those terms.

Your order is visible to the Rockabilly Roasting fulfillment team immediately and enters their standard fulfillment workflow.

## Tips for Efficient Ordering

- **Use the Quick Order page to build your full order in one pass.** Enter quantities for everything you need across all products, then click "Add to Order" once. This is faster than adding items one at a time.
- **Check stock status before ordering.** Out-of-stock variants are clearly marked in the variant table. If something you need is out of stock, contact the team to ask about availability.
- **Keep your PO numbers consistent.** If your business tracks purchase orders, always include your PO number at checkout. It will appear on your invoice for easy matching.
- **Bookmark the portal.** Save `https://rockabillyroasting.com/wholesale/portal` for quick access to the ordering page.
