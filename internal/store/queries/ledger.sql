-- name: InsertLedgerEntries :copyfrom
INSERT INTO ledger (
    request_id, created_at, tenant_id, source, end_user, requested_model, provider, model,
    stream, cache, similarity, status, error_code, attempts, prompt_tokens, completion_tokens,
    usage_estimated, cost_usd, embedding_cost_usd, saved_usd, latency_ms, ttfb_ms, overhead_ms
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23
);

-- name: DeleteLedgerBefore :execrows
DELETE FROM ledger
WHERE request_id IN (
    SELECT old.request_id FROM ledger AS old
    WHERE old.created_at < sqlc.arg(cutoff)
    LIMIT sqlc.arg(batch_size)
);

-- name: GetMaintenanceRun :one
SELECT last_run FROM maintenance_runs WHERE job = $1;

-- name: UpsertMaintenanceRun :exec
INSERT INTO maintenance_runs (job, last_run) VALUES ($1, $2)
ON CONFLICT (job) DO UPDATE SET last_run = EXCLUDED.last_run;
