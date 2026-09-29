CREATE TABLE media_objects (
    hash          TEXT PRIMARY KEY,
    owner_user_id UUID NOT NULL,
    size_bytes    BIGINT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
