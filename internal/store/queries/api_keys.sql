-- name: CreateAPIKey :one
INSERT INTO api_keys (name, key_hash, key_prefix, rate_per_sec, burst)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, name, key_prefix, rate_per_sec, burst, active, created_at;

-- name: ListAPIKeys :many
SELECT id, name, key_prefix, rate_per_sec, burst, active, created_at
FROM api_keys
ORDER BY id;

-- name: ListActiveAPIKeys :many
SELECT id, name, key_hash, key_prefix, rate_per_sec, burst
FROM api_keys
WHERE active;

-- name: RevokeAPIKey :execrows
UPDATE api_keys SET active = false WHERE name = $1 AND active;
