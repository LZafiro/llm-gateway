# 0011. Asynchronous ledger and cost accounting

- Status: Accepted
- Date: 2026-10-01

## Context

Every request must record tokens and cost in Postgres. A synchronous insert adds database latency to every response and couples availability to the database.

## Decision

**Pricing**: per-model prices (US$ per 1M input and output tokens) live in the YAML config.

**Usage**: tokens come from the provider's `usage` field. For streams, usage comes from the final OpenAI chunk (with `include_usage` injected) or from Anthropic's `message_delta` event.

**Cache accounting**: cache entries store the original cost. A hit is recorded with `cost_usd = 0` and `saved_usd = original cost`.

**Writer**: each completed request produces one Ledger Entry, which is pushed onto a bounded channel. A writer goroutine flushes in batches with `COPY` every 100 rows or 1s, whichever comes first, and drains on graceful shutdown. When the buffer is full, the entry is dropped and `gateway_ledger_dropped_total` is incremented. The response path never blocks on the ledger.

**Retention**: a daily in-process job deletes ledger rows older than 30 days and expired semantic cache rows.

## Consequences

- Ledger writes do not show up in the overhead measurements.
- Up to one batch can be lost on a crash. This is acceptable for demo accounting.
- The ledger doubles as the source of panel totals and of benchmark cost figures.

## Alternatives considered

- Synchronous insert per request: simpler and lossless, but adds latency and a hard database dependency.
- External queue: unjustified infrastructure for one consumer.
