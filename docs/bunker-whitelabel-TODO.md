# TODO — Bunker white-label products

**Status:** Bunker's account is migrated and live (Wholesale 2025, approved, NET 7). Its three
white-label coffees are **not built yet** — blocked on staff deciding which Rockabilly roast backs
each label. This note tracks that follow-up.

## What's decided

- **Mechanism:** per-customer **`private` visibility** (NOT a group). Products are granted directly
  to Bunker's customer via `product_customer_visibility`. Verified real, wired, and tested
  (migration 052 + `WhiteLabelService`, `internal/app/whitelabel.go`).
- **Customer:** Bunker Uniforms and Equipment — `cworman@thebunkertactical.com` (OS `cu_j35jlo2n`).

## The three labels (from Orderspace)

OS product "The Bunker White-label" (code `0010`), flat **$8.00**, no size/grind split:

| Label | OS SKU | Backing Rockabilly coffee/SKU | Sizes/price |
|---|---|---|---|
| Tactical Fuel | `0010-TF` | **TBD — staff** | TBD |
| Nucleur Fallout | `0010-NF` | **TBD — staff** | TBD |
| Kaboom Brew | `0010-KB` | **TBD — staff** | TBD |

Open modeling question: keep them as flat-$8 single units (matches OS) **or** clone a real coffee
and inherit its sizes/grinds/wholesale pricing (how `WhiteLabelService` works). Decide per label.

## How to build them (no code — admin only)

Once the backing coffee for each label is chosen, either path works:

1. **Manual admin (fastest for a few products):** create/edit the product → set visibility to
   **"Private — only selected customers (white-label)"** → add **Bunker** in the "Customers with
   access" panel. (`internal/web/admin_catalog.go` visibility handler.)
2. **Self-service invite:** staff send Bunker a white-label invite email; Bunker opens the
   token-gated page, picks a base coffee, and uploads their label art → a **draft private** product
   is created cloned from that coffee → staff review and publish.
   (`WhiteLabelService.SendInviteEmail` → `internal/web/whitelabel.go`.)

   Submissions surface in the admin panel, not just the notification email: the
   dashboard's **Pending review** band, a **White-label review** tab on
   `/admin/catalog` (`?white_label=pending`), and a count badge on the Catalog
   sidebar row. A submission stays on that queue until it's published (draft →
   active) or archived — there's no separate reviewed flag, so nothing gets lost
   if the email is missed.

Either way the product should end up: **private visibility, granted to Bunker only, active** once
reviewed. Confirm it appears in Bunker's wholesale catalog and nowhere else.

## Notes

- The importer's `0010-*` SKU skip stays — Bunker has no historical white-label orders, and going
  forward Bunker orders through Hiri.
- If Bunker ever needs custom white-label *pricing*, that's a price-list concern (not part of the
  visibility mechanism).
