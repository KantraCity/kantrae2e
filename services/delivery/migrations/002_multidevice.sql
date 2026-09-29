-- Multi-device support: GroupInfo for External Commits, user of each member
-- device, and devices that were removed and may not rejoin by themselves.
ALTER TABLE groups
    ADD COLUMN group_info       BYTEA,
    ADD COLUMN group_info_epoch BIGINT;

ALTER TABLE group_members ADD COLUMN user_id UUID;
CREATE INDEX group_members_user_idx ON group_members(user_id, group_id);

ALTER TABLE group_messages ADD COLUMN sender_user_id UUID;

CREATE TABLE group_bans (
    group_id  TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    device_id UUID NOT NULL,
    banned_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, device_id)
);
