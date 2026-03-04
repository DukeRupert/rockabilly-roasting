-- +goose Up
CREATE TABLE sessions (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type    text NOT NULL,
    actor_id      uuid NOT NULL,
    token_hash    text NOT NULL,
    ip_address    text,
    user_agent    text,
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_sessions_token_hash ON sessions(token_hash);
CREATE INDEX idx_sessions_actor ON sessions(actor_type, actor_id);

CREATE TABLE reset_tokens (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_type  text NOT NULL,
    actor_id    uuid NOT NULL,
    token_hash  text NOT NULL,
    expires_at  timestamptz NOT NULL,
    used_at     timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_reset_tokens_token_hash ON reset_tokens(token_hash);

CREATE TABLE email_verifications (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_id   uuid NOT NULL REFERENCES customers(id) ON DELETE CASCADE,
    token_hash    text NOT NULL,
    expires_at    timestamptz NOT NULL,
    verified_at   timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_email_verifications_token_hash ON email_verifications(token_hash);

-- +goose Down
DROP TABLE IF EXISTS email_verifications;
DROP TABLE IF EXISTS reset_tokens;
DROP TABLE IF EXISTS sessions;
