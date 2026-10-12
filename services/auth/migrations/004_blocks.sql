-- Block list (like Telegram): a blocked user cannot start a direct chat with
-- the blocker, nor write or call them in one.
CREATE TABLE blocks (
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    blocked_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, blocked_id)
);
CREATE INDEX blocks_blocked_idx ON blocks (blocked_id);
