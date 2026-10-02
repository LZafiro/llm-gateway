# Architecture Decision Records

| # | Decision | Status |
|---|---|---|
| [0001](0001-modular-monolith.md) | Modular monolith in a single binary | Accepted |
| [0002](0002-single-replica-in-memory-state.md) | Single replica with in-memory hot state | Accepted |
| [0003](0003-vps-docker-compose.md) | Single VPS with Docker Compose | Accepted |
| [0004](0004-openai-compatible-surface.md) | OpenAI-compatible API surface, text only | Accepted |
| [0005](0005-model-aliases-and-fallback-chains.md) | Model aliases and fallback chains | Accepted |
| [0006](0006-streaming-failover-before-first-byte.md) | Streaming failover only before the first byte | Accepted |
| [0007](0007-hand-rolled-provider-clients.md) | Hand-rolled provider clients | Accepted |
| [0008](0008-circuit-breaker-and-retry-policy.md) | Hand-rolled circuit breaker and retry policy | Accepted |
| [0009](0009-cache-keys-and-fail-open.md) | Cache keys, single-turn semantic cache and fail-open | Accepted |
| [0010](0010-embeddings-graceful-degradation.md) | Embeddings via OpenAI with graceful degradation | Accepted |
| [0011](0011-async-ledger-and-cost-accounting.md) | Asynchronous ledger and cost accounting | Accepted |
| [0012](0012-chaos-as-provider-decorator.md) | Chaos as a provider decorator | Accepted |
| [0013](0013-demo-safety.md) | Public demo safety | Accepted |
| [0014](0014-observability.md) | Observability: Prometheus in production, Grafana locally | Accepted |
| [0015](0015-dependency-policy.md) | Dependency policy | Accepted |
| [0016](0016-no-comments-policy.md) | No comments in code | Accepted |
| [0017](0017-semantic-threshold.md) | Semantic cache similarity threshold | Proposed |

Format: lean MADR (context, decision, consequences, alternatives considered). New decisions get the next number; superseded records stay and link to their replacement.
