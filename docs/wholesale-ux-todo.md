# Wholesale portal UX — survey findings & TODO

Survey of the wholesale ordering flow (quick-order portal → cart → checkout →
reorder), 2026-07-07. Goal: make ordering large quantities simple and fast for
wholesale partners. Items ordered by impact; check off as completed.

## High impact

- [x] **1. "In Stock" badge is hardcoded.** *(Done: removed the Stock column
  and `InStock` plumbing — inventory tables exist but nothing ever writes them,
  so there was no real data to wire. Re-add the column when inventory tracking
  is actually maintained.)* `internal/app/wholesale.go`
  (`QuickOrderCatalog`) sets `InStock: true // TODO: wire up inventory`, so
  every variant always shows a green "In Stock" chip regardless of reality.
  Either wire it to real inventory or remove the Stock column until it's real —
  a wrong availability signal is worse than none. When wired, also disable the
  qty input on out-of-stock rows (the template currently renders an editable
  input either way).

- [x] **2. MOQ violations surface as a dead-end error at the last step.**
  *(Done: `MOQViolationError` carries per-line violations out of
  `PlaceWholesaleOrder`; the checkout page — on GET, every htmx cart update,
  and confirm failure — shows a banner naming each line below minimum; the
  confirm handler re-renders instead of the generic error page; portal inputs
  block invalid submits via `setCustomValidity`; checkout qty inputs got
  `min`/`step` and a "min 6 · ×6" hint.)*
  "min 6" renders as a text hint only; nothing enforces it in the portal
  inputs, at bulk-add, or in checkout's inline qty editor. First enforcement is
  `ValidateWholesaleCart` inside `PlaceWholesaleOrder`, and the confirm handler
  lets `ErrMOQViolation` fall through to the generic error page
  (`web/wholesale.go` `handleWholesaleCheckoutConfirm`) — buyer loses the
  checkout page and isn't told which line is short. The service computes
  per-item violations and discards them. Fix in layers:
  - Catch `ErrMOQViolation` in the confirm handler and re-render checkout with
    the offending items named (existing error-banner pattern).
  - Validate at bulk-add / cart-update with a friendly re-render.
  - Add client-side nudges: `min` on portal inputs where sensible, and carry
    the `step`/min constraints onto the checkout qty editor (currently missing
    `step` entirely, so case-multiple rules vanish on the review page).

- [x] **3. Portal doesn't reflect the current cart, and re-adding doubles
  quantities.** *(Done: the sheet now pre-fills each row from the cart and
  bulk-add uses set semantics via a new `CartService.SetItemForCustomer` —
  what's on screen is exactly the order; clearing a row removes the line;
  resubmitting is idempotent. Button reads "Update Order" when a cart exists.
  Bonus fixes while in there: the checkout stale-price refresh used the
  incrementing upsert, so a price-drift confirm doubled quantities and never
  refreshed the price — now set-semantics; reorder is idempotent too.)*

- [x] **4. No "reorder last order" shortcut on the portal.** *(Done: a "Same
  as last time?" card at the top of the sheet shows the most recent order's
  number, date, and total with a one-click Reorder button. Shown only while
  the cart is empty, so it can't silently rewrite a sheet in progress.)*

## Medium impact

- [x] **5. Catalog order is `created_at DESC`.** *(Done: `QuickOrderCatalog`
  sorts alphabetically by title — buyers scan the same sheet weekly, so row
  order no longer reshuffles when a product launches. Global `ListProducts`
  order untouched.)*

- [x] **6. No search/filter on the portal.** *(Done: client-side Alpine filter
  over title/SKU/option values, shown when the sheet has more than 5 products.
  Filtered-out rows keep their values and still submit; Enter in the filter
  never submits the order; a "0 of N match" state points back to clearing.)*

- [x] **7. No per-row extended price in the portal.** *(Done: a live Total
  column per row, updated in the same `recompute()` pass as the sticky bar.)*

- [x] **8. Bulk-add errors lose all typed quantities.** *(Done: variants that
  retired between render and submit are skipped and counted — the buyer lands
  on checkout with a "left off N items" notice instead of a dead-end error
  page, mirroring the reorder pattern.)*

## Already good (no action)

Dense single-sheet ordering, live subtotal sticky bar, mobile card collapse,
one-click reorder with skipped-item counts, price-drift detection at checkout,
saved address book, PO number + notes, invoice-later (no payment friction),
labeled inputs / aria labels.
