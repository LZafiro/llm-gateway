# Caching

Two cache layers sit in front of the router: an exact cache in Redis and a semantic cache in Postgres with pgvector. Both are best-effort. A cache failure never fails a request.

Related: [0009](../adr/0009-cache-keys-and-fail-open.md), [0010](../adr/0010-embeddings-graceful-degradation.md), [0017](../adr/0017-semantic-threshold.md).

## Lookup order

```
bypass requested? ── yes ──▶ router, no cache write, X-Gateway-Cache: bypass
        │ no
exact lookup (Redis) ── hit ──▶ respond, X-Gateway-Cache: exact
        │ miss / error
semantic eligible? ── no ──▶ router
        │ yes
embed prompt ── error ──▶ router (semantic degraded)
        │
vector search ── sim >= threshold ──▶ respond, X-Gateway-Cache: semantic
        │ below / error
router ──▶ on success: write exact (+ semantic if eligible)
```

## Normalization

The canonical request used for keys:

- `model` is the requested value as sent (an alias or direct model), not the effective upstream model. Hits are therefore scoped to what the client asked for.
- `messages`: each content is converted to a single string (text parts joined with `\n`), trimmed, and runs of internal whitespace are collapsed to one space. Roles are kept.
- Params that affect output: `temperature`, `top_p`, `max_tokens` (after `max_completion_tokens` resolution), `stop`. Absent params are encoded explicitly as `null`.
- Serialized as canonical JSON (sorted keys, no insignificant whitespace).
- `stream` does not participate. Stream and non-stream requests share entries.

## Exact cache

- Key: `gw:exact:{tenant_id}:{hex(sha256(canonical_json))}`.
- Value: JSON `CachedResponse`.

```json
{
  "content": "...",
  "finish_reason": "stop",
  "provider": "anthropic",
  "model": "claude-haiku-4-5",
  "usage": {"prompt_tokens": 12, "completion_tokens": 40},
  "cost_usd": 0.000172,
  "created_at": "2026-10-01T12:00:00Z"
}
```

- TTL: 24h (`cache.exact.ttl`).
- Redis client timeouts: dial 200ms, read and write 50ms. On timeout or error the result counts as `error`, the request continues as a miss, and the next layer runs.
- Responses are written only when `finish_reason` is `stop`. Truncated (`length`), failed or partial responses are never cached.

## Semantic cache

### Eligibility

A request is eligible when all of the following hold:

- After normalization, the conversation has **exactly one `user` message** and no `assistant` messages. An optional `system` message is allowed.
- The request is not bypassed.
- The embedder is available.

Multi-turn conversations use only the exact cache.

### Scope

A vector search runs only within the same `(tenant_id, model, system_hash, params_hash)`:

- `system_hash`: sha256 of the normalized system message, or of the empty string.
- `params_hash`: sha256 of the canonical JSON of the output-affecting params.

### Schema

```sql
CREATE TABLE semantic_cache (
    id            BIGSERIAL PRIMARY KEY,
    tenant_id     BIGINT      NOT NULL REFERENCES api_keys(id) ON DELETE CASCADE,
    model         TEXT        NOT NULL,
    system_hash   BYTEA       NOT NULL,
    params_hash   BYTEA       NOT NULL,
    prompt        TEXT        NOT NULL,
    embedding     VECTOR(1536) NOT NULL,
    embedding_model TEXT      NOT NULL,
    response      JSONB       NOT NULL,
    cost_usd      NUMERIC(12,8) NOT NULL,
    hits          INTEGER     NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX semantic_cache_embedding_hnsw
    ON semantic_cache USING hnsw (embedding vector_cosine_ops);

CREATE INDEX semantic_cache_scope
    ON semantic_cache (tenant_id, model, system_hash, params_hash, expires_at);
```

`response` holds the same `CachedResponse` JSON as the exact cache.

### Query

```sql
SELECT id, response, cost_usd, 1 - (embedding <=> $1) AS similarity
FROM semantic_cache
WHERE tenant_id = $2 AND model = $3 AND system_hash = $4 AND params_hash = $5
  AND embedding_model = $6 AND expires_at > now()
ORDER BY embedding <=> $1
LIMIT 1;
```

- The query runs inside a transaction with `SET LOCAL hnsw.ef_search = 40`.
- A hit requires `similarity >= cache.semantic.threshold`.
- On a hit, `hits = hits + 1` is updated asynchronously (best effort, not on the response path).
- Query timeout: 150ms. A timeout or error counts as a miss.

### Write

After a successful upstream response with `finish_reason = stop`, the gateway inserts a row with the embedding already computed during lookup. The embedding is not recomputed. The insert runs after the response is fully written to the client, within the request goroutine, with a 200ms timeout. Failures are counted and ignored.

When a semantic hit is served, the exact cache is also populated for that request's key so the next identical request is an exact hit.

## Embedder

```go
type Embedder interface {
	Model() string
	Embed(ctx context.Context, text string) ([]float32, error)
}
```

- Implementation: OpenAI `text-embedding-3-small`, 1536 dims, through a dedicated client separate from the chat provider client.
- Timeout 500ms. Chaos rules do **not** apply to the embedder.
- Degradation: a failure-ratio tracker for the embedder (same breaker type and parameters as providers). While it is open, the semantic layer is skipped entirely and the demo panel shows "semantic cache degraded".
- The threshold experiment uses a second implementation (`text-embedding-3-large` truncated to 1536 dims via the `dimensions` request parameter). That keeps the schema unchanged.

## Configuration

```yaml
cache:
  exact:
    enabled: true
    ttl: 24h
  semantic:
    enabled: true
    threshold: 0.95
    ttl: 168h
    embedding_model: text-embedding-3-small
    query_timeout: 150ms
```

The provisional `threshold` of 0.95 is replaced by the value chosen in [threshold-experiment.md](threshold-experiment.md) and recorded in [0017](../adr/0017-semantic-threshold.md).

## Expiry

The daily maintenance job (see [ledger-and-cost.md](ledger-and-cost.md)) runs `DELETE FROM semantic_cache WHERE expires_at < now()` in batches of 1000. Redis expiry is native TTL plus `allkeys-lru` eviction under the 64MB cap.

## Metrics

- `gateway_cache_lookups_total{layer="exact|semantic",result="hit|miss|error|skipped"}`
- `gateway_cache_similarity` histogram of the best similarity on every semantic lookup, hit or miss. The threshold experiment uses it as a sanity check against production traffic.
- `gateway_cache_writes_total{layer,result}`
- `gateway_embedder_state` gauge, same encoding as the breaker.

## Acceptance criteria

- With Redis stopped, completions still succeed. The response carries `X-Gateway-Cache: miss` and the `error` counter increments.
- With Postgres slow (more than 150ms) on the semantic query, completions still succeed with no added latency beyond 150ms.
- Two requests that differ only in whitespace produce an exact hit.
- A two-turn conversation never produces a semantic hit.
- Requests with different `system` messages or different `temperature` never share a semantic hit.
- Cached responses replay correctly as both JSON and stream.
