# 0012. Chaos as a provider decorator

- Status: Accepted
- Date: 2026-10-01

## Context

The demo and the failover benchmark need to inject faults on demand. Faults must be indistinguishable from real failures to the retry, breaker and metrics code, otherwise the demo proves nothing.

## Decision

Wrap each chat `Provider` in a `chaos.Provider` decorator that reads the current rule for that provider from an in-memory `ChaosStore`:

```yaml
down: true|false
error_rate: 0.0..1.0
latency_ms: 0
jitter_ms: 0
expires_at: timestamp
```

- `down` returns a `Connection` error immediately.
- `error_rate` returns a `Server` error with that probability.
- Latency is added before delegating.

Rules expire automatically.

Chaos applies only to chat providers. Redis, Postgres and the embedder are not targets. Their failure handling is covered by fail-open tests (see 0009).

Rules are set through `PUT` and `DELETE /admin/chaos/{provider}` (admin token) or, in a restricted form, through the public demo button (see 0013).

## Consequences

- The breaker, retries, metrics and ledger react to chaos exactly as to real outages.
- Zero cost when no rule is active: a single atomic load per attempt.

## Alternatives considered

- Network-level fault injection (Toxiproxy): more realistic transport failures, but an extra container, and it cannot be toggled from the playground.
