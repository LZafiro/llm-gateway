# Roadmap

Each phase ends with a working, tested, committed increment. Specs in `docs/spec/` are the acceptance criteria.

| Phase | Goal | Specs | Done when |
|---|---|---|---|
| 0. Bootstrap | Module, layout, tooling, local stack | deployment | `make up`, `make lint`, `make test` pass; CI runs lint, no-comments check and tests |
| 1. Passthrough | OpenAI-compatible API over Anthropic, OpenAI and mock, with aliases | api, routing-and-resilience | A stock OpenAI SDK chats through the gateway, streaming and non-streaming, on both providers |
| 2. Resilience | Error classification, retry with backoff, breaker, fallback, deadline, chaos | routing-and-resilience, chaos | E2E test: chaos takes provider A down and requests succeed through B |
| 3. Accounting | API keys, token bucket, ledger, cost, Prometheus metrics | rate-limiting, ledger-and-cost | Every request has a ledger row; 429s carry rate limit headers; `/metrics` exposes `gateway_*` |
| 4. Caching | Exact cache, embedder, semantic cache, fail-open | caching | Hits return identical bodies with cache headers; Redis or Postgres down still yields a provider response |
| 5. Playground | Demo endpoints, SSE hub, budget, IP limit, web page | demo | A visitor chats, sees the panel update live and watches failover after pressing the down button |
| 6. Measurements | Load generator, overhead, cache and failover benchmarks, threshold experiment | benchmarks, threshold-experiment | `results/*.json` committed, Results tab renders charts, ADR 0017 accepted |
| 7. Production | Image, Caddy, prod compose, GHCR deploy over SSH | deployment | Public URL serves the playground over TLS |
