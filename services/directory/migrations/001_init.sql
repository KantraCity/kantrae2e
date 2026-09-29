CREATE TABLE key_packages (
    id         BIGSERIAL PRIMARY KEY,
    user_id    UUID NOT NULL,
    device_id  UUID NOT NULL,
    payload    BYTEA NOT NULL,
    used_at    TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Fast lookup of the unused stock of a device / user.
CREATE INDEX key_packages_unused_device_idx ON key_packages (device_id, id) WHERE used_at IS NULL;
CREATE INDEX key_packages_unused_user_idx ON key_packages (user_id, device_id) WHERE used_at IS NULL;
