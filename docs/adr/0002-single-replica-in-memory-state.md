# 0002. Single replica with in-memory hot state

- Status: Accepted
- Date: 2026-10-01

## Context

Circuit breakers, chaos rules, token buckets and the live panel event hub are read or written on every request. Keeping them consistent across replicas requires a shared store and adds a network round trip per request.

## Decision

Run exactly one gateway replica. Hot state lives in memory:

- breaker state per provider
- chaos rules per provider
- token buckets per API key
- SSE subscriber hub

Durable or shared data lives outside the process: exact cache in Redis, semantic cache, API keys and ledger in Postgres. Panel totals are rebuilt from the ledger on startup.

Each in-memory component sits behind an interface (`Limiter`, `BreakerSet`, `ChaosStore`, `Hub`) so that a Redis-backed implementation can replace it without touching the pipeline.

## Consequences

- No coordination cost; state transitions are exact and easy to test.
- A restart resets breakers, chaos and buckets. Chaos already auto-expires and breakers re-learn within one window, so this is acceptable.
- Horizontal scaling requires implementing the Redis-backed variants first. This is the documented limit of the design.

## Alternatives considered

- Redis-backed distributed state: horizontally scalable, but each request pays extra Upstash/Redis round trips and the code grows significantly for no benefit at demo scale.
- Hybrid with only rate limiting in Redis: still pays the round trip on the hottest path without enabling scale-out of breakers.
