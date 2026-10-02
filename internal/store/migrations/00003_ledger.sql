-- +goose Up
CREATE TABLE ledger (
    request_id         TEXT          PRIMARY KEY,
    created_at         TIMESTAMPTZ   NOT NULL,
    tenant_id          BIGINT        NOT NULL,
    source             TEXT          NOT NULL,
    end_user           TEXT,
    requested_model    TEXT          NOT NULL,
    provider           TEXT,
    model              TEXT,
    stream             BOOLEAN       NOT NULL,
    cache              TEXT          NOT NULL,
    similarity         REAL,
    status             SMALLINT      NOT NULL,
    error_code         TEXT,
    attempts           JSONB         NOT NULL,
    prompt_tokens      INTEGER       NOT NULL DEFAULT 0,
    completion_tokens  INTEGER       NOT NULL DEFAULT 0,
    usage_estimated    BOOLEAN       NOT NULL DEFAULT false,
    cost_usd           NUMERIC(12,8) NOT NULL DEFAULT 0,
    embedding_cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    saved_usd          NUMERIC(12,8) NOT NULL DEFAULT 0,
    latency_ms         INTEGER       NOT NULL,
    ttfb_ms            INTEGER,
    overhead_us        INTEGER
);

CREATE INDEX ledger_created_at ON ledger (created_at);
CREATE INDEX ledger_tenant_created ON ledger (tenant_id, created_at);

CREATE TABLE maintenance_runs (
    job      TEXT        PRIMARY KEY,
    last_run TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE maintenance_runs;
DROP TABLE ledger;
