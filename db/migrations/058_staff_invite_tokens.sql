-- +goose Up

-- Staff invites reuse the same shape as magic_link_tokens but bind to a staff
-- row instead of a customer. A new staff member is created with an unusable
-- random password; the invite token lets them set a real one via /staff/setup.
-- "Resend invite" / "reset password" simply mints a fresh token, so a single
-- table serves both first-time setup and later resets.
CREATE TABLE staff_invite_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    staff_id   uuid NOT NULL REFERENCES staff(id) ON DELETE CASCADE,
    token_hash text NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_staff_invite_tokens_staff ON staff_invite_tokens (staff_id);

-- +goose Down

DROP INDEX IF EXISTS idx_staff_invite_tokens_staff;
DROP TABLE IF EXISTS staff_invite_tokens;
