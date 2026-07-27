# One-off SQL scripts

Data changes that were applied to production by hand, kept here as the record of
what was run and why.

**These are not migrations.** Goose does not look in this directory — the
entrypoint runs `goose -dir db/migrations` only. Nothing here executes
automatically, on deploy or otherwise. A script in this directory has already
been run; it is history, not pending work.

Why they live outside `db/migrations/`:

- They encode decisions specific to production rows (which duplicate account
  survives, what a product costs) that would be meaningless against a fresh or
  test database.
- They are not idempotent and must not re-run. `retail_bags.sql` would fail on
  the slug unique index; `merge_duplicate_customers.sql` would fail on rows that
  no longer exist.

If a change belongs in every environment, it is a migration and belongs in
`db/migrations/`. If it is a repair or a data-entry task against real customer
rows, it belongs here.

## Conventions

Each script should wrap its work in an explicit transaction, end with
verification `SELECT`s before `COMMIT`, and state in a header comment what it
does, when it was run, and anything it deliberately leaves alone.

## Scripts

### `retail_bags.sql` — run 2026-07-27

Creates the `Retail 12oz Bags` wholesale product: 8 flavors × 2 grinds = 16
variants, priced into all four price lists (80 price rows).

Orderspace sold retail bags as their own product (`0009`, flavor option, no
grind) so packaging cost could be priced apart from bulk beans. The importer
folded that into each coffee's `12O` variant — `cmd/os-migrate/skumap.go:71` maps
`0009-<flavor>` → `<CODE>-12O-WB`, and `sizeMap` maps `1LB` → `12O` — so the
distinction was lost and wholesale customers had no way to order retail bags.
This restores the Orderspace shape rather than reusing `12O`, because one
variant carries one price per list and the two products need different prices.

Created as `draft` deliberately; published separately from admin. Existing `12O`
variants were left `wholesale_available = false` — they remain the retail
storefront SKUs.

Also trims a trailing space from the product title `Cloud 9 `.

### `merge_duplicate_customers.sql` — run 2026-07-27

Merges two pairs of customer accounts that differed only by email
capitalization: `dswallace1@comcast.net` and `rubyyyn28@yahoo.com`.

The pairs existed because customer lookup was an exact match (`WHERE email = $1`)
with no normalization, so signup could not see a row differing only by case and
created a second account beside it. Fixed in `domain.NormalizeEmail`; migration
`061_normalize_customer_email_case.sql` backfills the stored addresses and adds
unique indexes on `lower(email)`. **This script had to run before that
migration**, which aborts while any collision remains.

The surviving row in each pair is the already-lowercase one that also holds the
credentials, so no customer lost the ability to sign in. Beyond reparenting
orders / subscriptions / addresses / carts / coupons:

- The absorbed row's `stripe_customer_id` is preserved in
  `metadata.merged_from`. That Stripe customer still exists with its own payment
  methods; dropping the ID would have stranded it.
- Magic-link tokens on the absorbed row are deleted rather than moved — a live
  one would be a working credential for the surviving account.
- Sessions are cleared explicitly, since `sessions` keys off
  `actor_type`/`actor_id` and is not FK-bound to `customers`.
- A `customer.merged` audit row is written per pair.

**Left alone deliberately:** Ruby Navarro has two active subscriptions for the
same SKU (`2ST-12O-WB`) to the same address. Merging consolidated them under one
customer but cancelled neither — which one survives, and whether anything is
owed back, is a customer-service decision rather than a data-integrity one.
