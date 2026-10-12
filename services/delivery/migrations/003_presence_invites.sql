-- Presence ("last seen") and its privacy, per user.
-- visibility: 0/1 everybody, 2 people sharing a chat, 3 nobody.
CREATE TABLE presence (
    user_id    UUID PRIMARY KEY,
    last_seen  TIMESTAMPTZ,
    visibility SMALLINT NOT NULL DEFAULT 0
);

-- Invite links: SHA-256 of the link's secret (the secret itself is known
-- only to the members, through MLS).
ALTER TABLE groups ADD COLUMN invite_hash BYTEA;
