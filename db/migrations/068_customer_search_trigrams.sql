-- +goose Up
-- Fuzzy customer search for the admin list.
--
-- The admin customer search is an ILIKE '%term%' scan. It works when staff type
-- the term exactly right and falls off a cliff when they don't — a misremembered
-- spelling ("Kagen" vs "Kagan") returns an empty table with no way forward.
-- pg_trgm gives us a similarity fallback: when the exact search finds nothing,
-- the list offers "did you mean" candidates instead of a dead end.
--
-- The GIN indexes also accelerate the existing leading-wildcard ILIKE, which
-- no btree index could ever serve.

CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- Matches the expression used by both the ILIKE search and the similarity
-- fallback, so the planner can use it for either.
CREATE INDEX idx_customers_name_trgm
    ON customers USING gin ((first_name || ' ' || last_name) gin_trgm_ops);

CREATE INDEX idx_customers_email_trgm
    ON customers USING gin (email gin_trgm_ops);

CREATE INDEX idx_customers_company_trgm
    ON customers USING gin ((coalesce(company_name, '')) gin_trgm_ops);

-- Name sorting on the admin list orders by last name then first.
CREATE INDEX idx_customers_name_sort ON customers (last_name, first_name);

-- +goose Down
DROP INDEX IF EXISTS idx_customers_name_sort;
DROP INDEX IF EXISTS idx_customers_company_trgm;
DROP INDEX IF EXISTS idx_customers_email_trgm;
DROP INDEX IF EXISTS idx_customers_name_trgm;

-- The extension is left in place: dropping it would break any other object that
-- has since come to depend on it, and an unused extension costs nothing.
