-- +goose Up

-- Customer groups are retired. They were the pricing dimension once (removed in
-- v1.54.0/v1.55.0) and after that only gated 'restricted' products — a third
-- access mechanism nothing used, since the wholesale tier covers the channel and
-- 'private' (product_customer_visibility) covers per-customer white-labelling.
--
-- The guard below is deliberate: if any environment actually has group-gated
-- products, this migration stops the deploy instead of silently unpublishing
-- them. Convert those products to 'private' with per-customer grants first, then
-- re-run.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM product_group_visibility) THEN
        RAISE EXCEPTION
            'product_group_visibility is not empty: % row(s) still grant group access. Convert those products to private visibility with per-customer grants before running this migration.',
            (SELECT count(*) FROM product_group_visibility);
    END IF;
    IF EXISTS (SELECT 1 FROM products WHERE visibility = 'restricted') THEN
        RAISE EXCEPTION
            'products still use restricted visibility: %. Move them to public, wholesale or private first.',
            (SELECT string_agg(title, ', ') FROM products WHERE visibility = 'restricted');
    END IF;
END
$$;
-- +goose StatementEnd

DROP TABLE IF EXISTS product_group_visibility;
DROP TABLE IF EXISTS customer_group_memberships;
DROP TABLE IF EXISTS customer_groups;

-- 'restricted' has no mechanism behind it any more, so it comes out of the tier
-- list. The guard above has already established that no row uses it.
ALTER TABLE products DROP CONSTRAINT products_visibility_check;
ALTER TABLE products ADD CONSTRAINT products_visibility_check
    CHECK (visibility IN ('public', 'wholesale', 'private'));

-- +goose Down

CREATE TABLE customer_groups (
    id       uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name     text NOT NULL,
    metadata jsonb NOT NULL DEFAULT '{}'
);

CREATE TABLE customer_group_memberships (
    customer_id       uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    customer_group_id uuid NOT NULL REFERENCES customer_groups(id) ON DELETE CASCADE,
    assigned_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (customer_id, customer_group_id)
);

CREATE TABLE product_group_visibility (
    product_id        uuid NOT NULL REFERENCES products(id) ON DELETE CASCADE,
    customer_group_id uuid NOT NULL REFERENCES customer_groups(id) ON DELETE CASCADE,
    PRIMARY KEY (product_id, customer_group_id)
);

ALTER TABLE products DROP CONSTRAINT products_visibility_check;
ALTER TABLE products ADD CONSTRAINT products_visibility_check
    CHECK (visibility IN ('public', 'wholesale', 'restricted', 'private'));
