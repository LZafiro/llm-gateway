# 0009. Cache keys, single-turn semantic cache and fail-open

- Status: Accepted
- Date: 2026-10-01

## Context

Two cache layers sit in front of providers: exact (Redis) and semantic (pgvector). Multi-turn conversations make semantic matching dangerous, because a short follow-up such as "and how much does it cost?" only has meaning in context. Cache infrastructure must never be the reason a request fails.

## Decision

**Lookup order**: exact, then semantic, then provider.

**Exact key**: SHA-256 of tenant, requested alias or model, and the normalized messages and output-affecting parameters (`temperature`, `top_p`, `max_tokens`, `stop`). Entries expire after 24h.

**Semantic cache** applies only when the conversation contains exactly one user message, with an optional system prompt. The user message is embedded and searched with an HNSW cosine index, filtered by tenant, requested alias or model, SHA-256 of the system prompt, a hash of the output-affecting parameters, and the embedding model. Entries expire after 7 days.

**Writes** happen only after a complete provider response with `finish_reason=stop`. Both layers store the response and its original cost. A semantic hit also backfills the exact cache.

**Bypass**: the request header `X-Gateway-Cache: bypass` or `Cache-Control: no-cache` skips lookup and write.

**Temperature**: responses are cached regardless of temperature. Callers who need fresh samples use bypass.

**Fail-open**: any cache error or timeout (budgets: 50ms for Redis, 500ms for the embedder, 150ms for the pgvector query) counts as a miss, is logged and metered, and never fails the request.

## Consequences

- Multi-turn traffic only benefits from exact hits.
- The public playground is one tenant, so visitors share cache entries, which is what makes the hit rate visible.
- Tests must prove that Redis or Postgres being down still yields a provider response.

## Alternatives considered

- Embedding the full conversation: high false-hit risk on follow-ups.
- Skipping the cache when `temperature > 0`: correct in theory, but it would disable caching for almost all real traffic.
