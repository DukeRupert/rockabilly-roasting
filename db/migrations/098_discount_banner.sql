-- +goose Up

-- A line of copy staff write to promote a discount in the storefront's offer
-- strip. Empty means the discount is not promoted. It shows only while the
-- discount is live (active and inside its dates) and only to the shoppers who
-- could use it; see DiscountService.StorefrontBanners. Kept short because it
-- is one line above the header on a phone.
ALTER TABLE discounts ADD COLUMN banner_text text NOT NULL DEFAULT ''
    CONSTRAINT discounts_banner_text_length CHECK (char_length(banner_text) <= 120);

-- +goose Down
ALTER TABLE discounts DROP COLUMN banner_text;
