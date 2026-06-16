-- +goose Up

-- magic_link_tokens is reused for several single-use flows (magic-link sign-in,
-- wholesale password setup, and now white-label onboarding invites). Without a
-- discriminator, a token minted for one flow could be redeemed by another route
-- (e.g. an invite token redeemed at /wholesale/setup to set a password). Add a
-- purpose so each route only redeems tokens it minted.
ALTER TABLE magic_link_tokens
    ADD COLUMN purpose text NOT NULL DEFAULT 'magic_link';

-- +goose Down

ALTER TABLE magic_link_tokens
    DROP COLUMN IF EXISTS purpose;
