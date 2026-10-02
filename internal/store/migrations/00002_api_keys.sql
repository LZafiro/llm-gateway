-- +goose Up
CREATE TABLE api_keys (
    id           BIGSERIAL PRIMARY KEY,
    name         TEXT             NOT NULL UNIQUE,
    key_hash     BYTEA            NOT NULL UNIQUE,
    key_prefix   TEXT             NOT NULL,
    rate_per_sec DOUBLE PRECISION NOT NULL,
    burst        INTEGER          NOT NULL,
    active       BOOLEAN          NOT NULL DEFAULT true,
    created_at   TIMESTAMPTZ      NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE api_keys;
