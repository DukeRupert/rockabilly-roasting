-- +goose Up

-- Which Intuit app the shop connects QuickBooks through.
--
-- These were environment variables, and — unlike the item IDs that migration
-- 079 moved — they really are credentials, which is why they stayed behind.
-- The problem is not what they are, it is that putting them in the environment
-- makes connecting QuickBooks a deploy. QB_CLIENT_ID was also the on/off switch
-- for the entire module: unset, no workers registered, no settings card, no
-- webhook. So standing the integration up, moving from the sandbox app to the
-- production one, or rotating a leaked client secret each meant editing the
-- container's configuration and restarting the binary — for a change only the
-- bookkeeper can make and only the admin can verify.
--
-- Held per tenant rather than on the singleton store_settings row because it is
-- the other half of qb_credentials: the app the tokens in that table were
-- issued by. Keeping them side by side and keyed the same way is what lets the
-- code say "these tokens belong to this app" and refuse to use tokens minted by
-- a different one.
CREATE TABLE qb_app_config (
    tenant_id        uuid PRIMARY KEY,
    client_id        text NOT NULL,
    -- Encrypted with the same AES-256-GCM key as the OAuth tokens beside them
    -- (QB_TOKEN_ENCRYPTION_KEY, or derived from APP_SECRET). A client secret
    -- and a webhook verifier are exactly as sensitive as a refresh token — all
    -- three are bearer credentials for the same company file.
    client_secret    text NOT NULL,
    webhook_verifier text NOT NULL,
    -- 'sandbox' or 'production'. The environment variable it replaces fails
    -- toward production, because an unset or misspelled variable had to pick
    -- something and silently talking to a sandbox that does not exist is the
    -- worse failure. This column does not need that hedge: it is only ever
    -- written by a staffer choosing from two options in the admin, so it
    -- defaults to the safe one.
    environment      text NOT NULL DEFAULT 'sandbox',
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE qb_app_config
    ADD CONSTRAINT qb_app_config_environment_check
    CHECK (environment IN ('sandbox', 'production'));

-- No backfill, and none is possible: this migration cannot read the
-- environment the server will boot with. The environment variables therefore
-- stay as a fallback and win when set, exactly as migration 079 left the item
-- IDs — a running deployment keeps billing across this migration, and moves
-- into the table when someone saves the form.

-- +goose Down

DROP TABLE IF EXISTS qb_app_config;
