-- Device approval: a new device is pending until an approved device of the
-- same account (or the recovery key) approves it. Existing devices stay usable.
ALTER TABLE devices ADD COLUMN approved_at TIMESTAMPTZ;
UPDATE devices SET approved_at = created_at;

-- Recovery: SHA-256 of a verifier derived from the recovery key, and the
-- history key encrypted with another key derived from it (client-side).
CREATE TABLE recovery (
    user_id       UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    verifier_hash BYTEA NOT NULL,
    wrapped_key   BYTEA NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX users_username_prefix_idx ON users (lower(username) text_pattern_ops);
