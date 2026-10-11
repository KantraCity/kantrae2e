-- Public profile, as in Telegram: a display name separate from the unique
-- @username, a short bio and a photo.
ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN bio TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar BYTEA;
ALTER TABLE users ADD COLUMN avatar_id TEXT NOT NULL DEFAULT '';
UPDATE users SET display_name = username;
CREATE INDEX users_display_name_idx ON users (lower(display_name) text_pattern_ops);
