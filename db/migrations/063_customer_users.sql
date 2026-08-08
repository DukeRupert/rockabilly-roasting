-- +goose Up

-- A wholesale account can have more than one person signing in. The account
-- itself is still the customers row — orders, carts, addresses, subscriptions
-- and price lists all stay keyed on customers.id — and a customer_user only
-- ever resolves TO an account.
--
-- Deliberately additive: the primary login stays in customers.email /
-- customers.password_hash and is NOT duplicated here. That means no backfill,
-- no dropping the UNIQUE on customers.email, and no change to how any existing
-- wholesale or retail customer signs in. The trade is that "the primary user"
-- is implicit (it is the customers row), so the team screen reads from both
-- sources.
--
-- role is inert in v1 — every invited user is 'member' and the app performs no
-- role checks. It exists so that introducing real permissions later is a code
-- change plus a CHECK widening, not a migration over live rows.
CREATE TABLE customer_users (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id   uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    -- Stored normalized (lower/trimmed) by the app, matching customers.email —
    -- see domain.NormalizeEmail and migration 061. An exact-match lookup against
    -- a non-normalized row is the bug 061 existed to fix; do not reintroduce it.
    email         text NOT NULL UNIQUE,
    password_hash text,
    name          text NOT NULL DEFAULT '',
    role          text NOT NULL DEFAULT 'member' CHECK (role IN ('member')),
    -- Transactional mail (order confirmations, the weekly reminder). Defaults
    -- to true to match customers.order_reminders_enabled (migration 062):
    -- everyone is subscribed until they say otherwise, and the opt-out is the
    -- unsubscribe link in the mail itself, which acts on this row alone.
    receives_notifications boolean NOT NULL DEFAULT true,
    invited_at    timestamptz NOT NULL DEFAULT now(),
    last_login_at timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_customer_users_customer ON customer_users (customer_id);

-- Invited users need their own token table for the same reason staff do
-- (migration 058): magic_link_tokens.customer_id is a NOT NULL FK to customers,
-- so it cannot carry a customer_users row. Single-use, and re-inviting simply
-- mints a fresh token, so one table serves both first-time setup and resets.
CREATE TABLE customer_user_invite_tokens (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_user_id uuid NOT NULL REFERENCES customer_users(id) ON DELETE CASCADE,
    token_hash       text NOT NULL UNIQUE,
    expires_at       timestamptz NOT NULL,
    used_at          timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_customer_user_invite_tokens_user
    ON customer_user_invite_tokens (customer_user_id);

-- +goose Down

DROP INDEX IF EXISTS idx_customer_user_invite_tokens_user;
DROP TABLE IF EXISTS customer_user_invite_tokens;
DROP INDEX IF EXISTS idx_customer_users_customer;
DROP TABLE IF EXISTS customer_users;
