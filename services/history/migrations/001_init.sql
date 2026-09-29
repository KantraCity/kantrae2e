CREATE TABLE history_manifest (
    user_id    UUID NOT NULL,
    chunk_hash TEXT NOT NULL,
    size_bytes BIGINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, chunk_hash)
);

CREATE INDEX history_manifest_user_created_idx ON history_manifest(user_id, created_at);
