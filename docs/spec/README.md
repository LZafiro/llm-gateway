# Specifications

Implementable specifications for the LLM Gateway. Each spec describes behaviour, data shapes, configuration and acceptance criteria. The reasoning behind each choice lives in the ADRs under [`../adr/`](../adr/); the domain vocabulary lives in [`../../CONTEXT.md`](../../CONTEXT.md).

| Spec | Scope |
| --- | --- |
| [api.md](api.md) | Public HTTP surface: OpenAI-compatible endpoints, auth, errors, headers |
| [routing-and-resilience.md](routing-and-resilience.md) | Aliases, fallback chains, retries, circuit breaker, deadlines, streaming failover |
| [caching.md](caching.md) | Exact cache (Redis), semantic cache (pgvector), keys, TTLs, fail-open |
| [rate-limiting.md](rate-limiting.md) | Per-key token bucket and API key management |
| [ledger-and-cost.md](ledger-and-cost.md) | Usage accounting, pricing, async ledger writer, retention |
| [chaos.md](chaos.md) | Fault injection per provider, admin and public controls |
| [demo.md](demo.md) | Playground page, `/demo/*` endpoints, live panel events, safety limits |
| [benchmarks.md](benchmarks.md) | Overhead, cache and failover measurements, load generator |
| [threshold-experiment.md](threshold-experiment.md) | Semantic threshold curve experiment and dataset |
| [deployment.md](deployment.md) | Docker compose topology, VPS, Caddy, CI/CD, migrations, observability |

## Request pipeline at a glance

```
client
  -> api (decode, validate, request id)
  -> auth (api key lookup)
  -> ratelimit (token bucket per key)
  -> cache (exact, then semantic)
  -> router (alias -> chain; per provider: breaker -> chaos -> client, retries)
  -> response (stream or JSON) + cache write
  -> ledger (async) + metrics + demo event hub
```

## Configuration

A single YAML file (`config.yaml`, path from `GATEWAY_CONFIG`) holds non-secret settings. Secrets come from environment variables only. Every spec lists the keys it owns; the union is the full schema.

| Env var | Purpose |
| --- | --- |
| `GATEWAY_CONFIG` | Path to YAML config |
| `DATABASE_URL` | Postgres DSN |
| `REDIS_URL` | Redis URL |
| `OPENAI_API_KEY` | OpenAI chat and embeddings |
| `ANTHROPIC_API_KEY` | Anthropic chat |
| `ADMIN_TOKEN` | Bearer token for `/admin/*` |
| `DEMO_API_KEY` | Gateway key used internally by `/demo/chat` |
