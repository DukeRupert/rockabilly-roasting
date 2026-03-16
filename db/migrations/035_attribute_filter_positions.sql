-- +goose Up

-- Fix filterable flags: body, acidity, sweetness are detail-page only per coffee-attributes.md
UPDATE attribute_keys SET filterable = false WHERE slug IN ('body', 'acidity', 'sweetness');

-- Set display positions for filterable attributes (storefront filter bar ordering)
UPDATE attribute_keys SET position = 1 WHERE slug = 'roast-level';
UPDATE attribute_keys SET position = 2 WHERE slug = 'origin-type';
UPDATE attribute_keys SET position = 3 WHERE slug = 'caffeine-level';
UPDATE attribute_keys SET position = 4 WHERE slug = 'tasting-notes';
UPDATE attribute_keys SET position = 5 WHERE slug = 'regions';
UPDATE attribute_keys SET position = 6 WHERE slug = 'process';
UPDATE attribute_keys SET position = 7 WHERE slug = 'brew-methods';
UPDATE attribute_keys SET position = 8 WHERE slug = 'certifications';
UPDATE attribute_keys SET position = 9 WHERE slug = 'seasonal';

-- Detail-page only attributes ordered after filterable ones
UPDATE attribute_keys SET position = 10 WHERE slug = 'body';
UPDATE attribute_keys SET position = 11 WHERE slug = 'acidity';
UPDATE attribute_keys SET position = 12 WHERE slug = 'sweetness';
UPDATE attribute_keys SET position = 13 WHERE slug = 'finish';

-- +goose Down
UPDATE attribute_keys SET filterable = true WHERE slug IN ('body', 'acidity', 'sweetness');
UPDATE attribute_keys SET position = 0;
