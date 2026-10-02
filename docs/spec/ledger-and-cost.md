# Ledger and Cost Accounting

Every completion request produces one ledger entry with tokens, cost, savings, latency and routing details. Entries are written asynchronously in batches so the response path never waits on Postgres.

Related: [0011](../adr/0011-async-ledger-and-cost-accounting.md), [0014](../adr/0014-observability.md).

## Pricing

```yaml
pricing:
  anthropic/claude-haiku-4-5:
    input_per_mtok: 1.00
    output_per_mtok: 5.00
  openai/MINI_MODEL_TBD:
    input_per_mtok: 0.00
    output_per_mtok: 0.00
  mock/mock-1:
    input_per_mtok: 0.00
    output_per_mtok: 0.00
embedding_pricing:
  text-embedding-3-small:
    input_per_mtok: 0.02
```

Prices are USD per million tokens and are verified against the providers' pricing pages at bootstrap. A model missing from `pricing` fails config validation at startup.

```
cost_usd = prompt_tokens * input_per_mtok / 1e6 + completion_tokens * output_per_mtok / 1e6
```

Embedding cost is accounted separately in `embedding_cost_usd` on the same entry, so savings stay honest: a semantic lookup costs a little even when it misses.

## Token sources

| Source | Prompt tokens | Completion tokens |
| --- | --- | --- |
| OpenAI non-stream | `usage.prompt_tokens` | `usage.completion_tokens` |
| OpenAI stream | final usage chunk (`include_usage` forced) | same |
| Anthropic non-stream | `usage.input_tokens` | `usage.output_tokens` |
| Anthropic stream | `message_start.message.usage.input_tokens` | last `message_delta.usage.output_tokens` |
| Interrupted stream | known value or `0` | known value or `0`, `usage_estimated = true` |

## Savings

- On a cache hit: `cost_usd = 0` and `saved_usd = cached.cost_usd`, the cost of the original upstream response.
- On a miss: `saved_usd = 0`.
- Embedding cost is always charged when an embedding was computed.

## Schema

```sql
CREATE TABLE ledger (
    request_id      TEXT        PRIMARY KEY,
    created_at      TIMESTAMPTZ NOT NULL,
    tenant_id       BIGINT      NOT NULL,
    source          TEXT        NOT NULL,
    end_user        TEXT,
    requested_model TEXT        NOT NULL,
    provider        TEXT,
    model           TEXT,
    stream          BOOLEAN     NOT NULL,
    cache           TEXT        NOT NULL,
    similarity      REAL,
    status          SMALLINT    NOT NULL,
    error_code      TEXT,
    attempts        JSONB       NOT NULL,
    prompt_tokens   INTEGER     NOT NULL DEFAULT 0,
    completion_tokens INTEGER   NOT NULL DEFAULT 0,
    usage_estimated BOOLEAN     NOT NULL DEFAULT false,
    cost_usd        NUMERIC(12,8) NOT NULL DEFAULT 0,
    embedding_cost_usd NUMERIC(12,8) NOT NULL DEFAULT 0,
    saved_usd       NUMERIC(12,8) NOT NULL DEFAULT 0,
    latency_ms      INTEGER     NOT NULL,
    ttfb_ms         INTEGER,
    overhead_ms     INTEGER
);

CREATE INDEX ledger_created_at ON ledger (created_at);
CREATE INDEX ledger_tenant_created ON ledger (tenant_id, created_at);
```

- `source`: `api` or `demo`.
- `cache`: `miss|exact|semantic|bypass`.
- `attempts`: an array of `{provider, model, status, kind, latency_ms}`.
- `status`: the HTTP status returned to the client, or `499` for a client cancel.
- `overhead_ms`: `latency_ms` minus the sum of the upstream attempt latencies and backoff waits. This is the time the gateway itself spent. `NULL` on cache hits.

No message content is stored.

## Async writer

```yaml
ledger:
  buffer_size: 10000
  batch_size: 100
  flush_interval: 1s
  retention: 720h
```

- `Record(entry)` does a non-blocking send on a channel of capacity `buffer_size`. If the channel is full, the entry is dropped and `gateway_ledger_dropped_total` is incremented. The request path never blocks.
- A single writer goroutine accumulates entries and flushes when the batch reaches `batch_size` or `flush_interval` elapses, whichever comes first.
- A flush uses `pgx.CopyFrom` into `ledger`. On failure it retries twice with 200ms and 1s waits, then drops the batch and counts it in `gateway_ledger_dropped_total{reason="flush_failed"}`.
- On graceful shutdown (SIGTERM), the HTTP server stops accepting requests, in-flight requests drain for up to 10s, then the writer drains the channel and does a final flush with a 5s timeout.

## Daily maintenance job

An in-process scheduler runs at 03:00 UTC and once at startup if the last run (tracked in a `maintenance_runs` table) was more than 24h ago:

1. `DELETE FROM ledger WHERE created_at < now() - retention`, in batches of 5000.
2. Semantic cache expiry (see [caching.md](caching.md)).

```sql
CREATE TABLE maintenance_runs (
    job        TEXT PRIMARY KEY,
    last_run   TIMESTAMPTZ NOT NULL
);
```

## Aggregates for the demo panel

The panel snapshot reads today's (UTC) totals with a single query:

```sql
SELECT count(*), count(*) FILTER (WHERE cache <> 'miss' AND cache <> 'bypass'),
       sum(cost_usd + embedding_cost_usd), sum(saved_usd)
FROM ledger WHERE created_at >= date_trunc('day', now() AT TIME ZONE 'UTC');
```

After the snapshot, the hub keeps in-memory running totals, updated by every `request.completed` event and reset at UTC midnight. The demo budget (see [demo.md](demo.md)) uses the same in-memory total, seeded from this query at startup.

## Metrics

- `gateway_cost_usd_total{provider,model}`
- `gateway_saved_usd_total{layer}`
- `gateway_tokens_total{provider,model,direction="prompt|completion"}`
- `gateway_ledger_dropped_total{reason="buffer_full|flush_failed"}`
- `gateway_ledger_flush_duration_seconds` histogram
- `gateway_overhead_seconds` histogram, from `overhead_ms`

## Acceptance criteria

- Cost for a known fixture response matches a hand-computed value to 8 decimal places.
- With Postgres stopped, requests keep succeeding, latency is unaffected, and dropped entries are counted.
- Sending SIGTERM with 50 entries buffered results in all 50 persisted.
- A semantic hit records `cost_usd = 0`, `saved_usd` equal to the original cost, and a non-zero `embedding_cost_usd`.
