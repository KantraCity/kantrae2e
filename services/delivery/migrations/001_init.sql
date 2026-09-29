CREATE TABLE groups (
    id            TEXT PRIMARY KEY,
    -- The epoch owner: a Commit is accepted only via
    -- UPDATE ... SET current_epoch = current_epoch + 1 WHERE current_epoch = <expected>.
    current_epoch BIGINT NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Routing metadata only: which devices receive the group's messages.
CREATE TABLE group_members (
    group_id  TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    device_id UUID NOT NULL,
    -- First epoch this device can decrypt (0 for the creator, commit epoch + 1
    -- for added devices). Older application messages are not routed to it.
    joined_epoch BIGINT NOT NULL DEFAULT 0,
    joined_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (group_id, device_id)
);

CREATE INDEX group_members_device_idx ON group_members(device_id);

-- message_type: 1 commit, 2 application, 3 welcome (see delivery.proto).
CREATE TABLE group_messages (
    id               BIGSERIAL PRIMARY KEY,
    group_id         TEXT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    epoch            BIGINT NOT NULL,
    message_type     SMALLINT NOT NULL,
    payload          BYTEA NOT NULL,
    sender_device_id UUID NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX group_messages_group_idx ON group_messages(group_id, id);

-- Per-device delivery queue (fan-out). The payload is referenced, not copied.
CREATE TABLE message_queue (
    id           BIGSERIAL PRIMARY KEY,
    device_id    UUID NOT NULL,
    message_id   BIGINT NOT NULL REFERENCES group_messages(id) ON DELETE CASCADE,
    delivered_at TIMESTAMPTZ
);

CREATE INDEX message_queue_pending_idx ON message_queue(device_id, id) WHERE delivered_at IS NULL;
