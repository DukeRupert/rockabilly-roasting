-- +goose Up

-- Dealer's choice packs: the customer buys "N bags, roaster's pick" and the
-- crew chooses which coffees go in the box when they pack it.
--
-- A pick rule marks a variant as a dealer's choice pack. It lives beside the
-- variant rather than on it so the dozens of sqlc queries that select variant
-- columns are untouched; a variant with no row here is an ordinary variant.
--
-- exclude_decaf is per variant, not per order, on purpose: the "no decaf"
-- choice is a product option the customer picks on the storefront, so it rides
-- through the cart, checkout, and every subscription renewal as the variant
-- itself, with nothing extra to carry.
CREATE TABLE variant_pick_rules (
    variant_id    uuid PRIMARY KEY REFERENCES variants(id) ON DELETE CASCADE,
    pick_count    int NOT NULL CHECK (pick_count BETWEEN 1 AND 24),
    exclude_decaf boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

-- What the crew actually packed: one row per bag of a dealer's choice line.
-- A line needs (pick_count x quantity) rows before the order can be fulfilled,
-- labelled, or handed off. Suggestions are computed on the fly and never
-- stored, so a row here always means a person confirmed it.
--
-- product_id restricts deletes so the record of what a customer received
-- survives a coffee being retired.
CREATE TABLE line_item_picks (
    line_item_id uuid NOT NULL REFERENCES line_items(id) ON DELETE CASCADE,
    position     int  NOT NULL CHECK (position >= 0),
    product_id   uuid NOT NULL REFERENCES products(id) ON DELETE RESTRICT,
    picked_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (line_item_id, position)
);

CREATE INDEX line_item_picks_product_id_idx ON line_item_picks (product_id);

-- +goose Down
DROP TABLE line_item_picks;
DROP TABLE variant_pick_rules;
