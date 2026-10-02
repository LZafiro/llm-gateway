# Routing and Resilience

How a request resolves to providers and how the gateway survives provider failure: aliases, fallback chains, retries with backoff, circuit breakers, deadlines and streaming failover.

Related: [0005](../adr/0005-model-aliases-and-fallback-chains.md), [0006](../adr/0006-streaming-failover-before-first-byte.md), [0007](../adr/0007-hand-rolled-provider-clients.md), [0008](../adr/0008-circuit-breaker-and-retry-policy.md), [0002](../adr/0002-single-replica-in-memory-state.md).

## Provider interface

```go
type Provider interface {
	Name() string
	Complete(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Stream(ctx context.Context, req ChatRequest) (ChunkStream, error)
}

type ChunkStream interface {
	Next() (Chunk, error)
	Close() error
}
```

`ChatRequest`, `ChatResponse` and `Chunk` are the gateway's internal canonical types, close to the OpenAI shape. Each adapter (`openai`, `anthropic`, `mock`) translates to and from its wire format. Errors returned by adapters are wrapped in `*provider.Error`:

```go
type Error struct {
	Provider   string
	Status     int
	Kind       ErrorKind
	RetryAfter time.Duration
	Err        error
}
```

`ErrorKind`: `KindTimeout`, `KindConnection`, `KindRateLimited`, `KindOverloaded`, `KindServer`, `KindClient`, `KindMalformed`, `KindCanceled`.

### Classification

| Upstream signal | Kind | Retryable | Counts as breaker failure |
| --- | --- | --- | --- |
| Dial, TLS or reset error | `KindConnection` | yes | yes |
| Per-attempt timeout | `KindTimeout` | yes | yes |
| HTTP 429 | `KindRateLimited` | yes | yes |
| HTTP 529, or Anthropic `overloaded_error` | `KindOverloaded` | yes | yes |
| HTTP 500, 502, 503, 504 | `KindServer` | yes | yes |
| Other HTTP 4xx | `KindClient` | no, no failover | no |
| Unparseable body or stream | `KindMalformed` | no, failover allowed | yes |
| Client canceled the request context | `KindCanceled` | no | no |

## Routes

```yaml
routes:
  aliases:
    fast:
      - anthropic/claude-haiku-4-5
      - openai/gpt-4o-mini
  direct:
    - anthropic/claude-haiku-4-5
    - openai/gpt-4o-mini
```

- `model` matching an alias resolves to its ordered chain.
- `model` matching `provider/model` in `direct` resolves to a chain of length 1: no fallback.
- A bare model name (`claude-haiku-4-5`) resolves to the unique `direct` entry whose model part matches. If no entry or more than one matches, the request fails with `404 model_not_found`.
- Config validation at startup: every chain entry must exist in `pricing` (see [ledger-and-cost.md](ledger-and-cost.md)) and reference a configured provider.

## Execution algorithm

```
deadline := now + 30s
for each target in chain:
    if breaker(target.provider).Allow() == false:
        record skip(reason=breaker_open); continue
    for attempt in 1..maxAttempts:
        res, err := chaos(provider).call(ctx(deadline, perAttemptTimeout), target)
        breaker.Record(err)
        if err == nil: return res
        if !retryable(err): break or fail (see below)
        if attempt == maxAttempts: break
        wait := backoff(attempt, err.RetryAfter)
        if now + wait >= deadline: break
        sleep(wait) honoring ctx
    record exhausted(target)
return 503 all_providers_unavailable
```

- `KindClient` errors return immediately to the client and are not retried or failed over.
- `KindMalformed` skips the remaining attempts on this provider and fails over to the next one.
- `KindCanceled` aborts everything. No response is written and the ledger records status `499`.
- When the deadline elapses before a provider commits, the request returns `504 deadline_exceeded`.
- `attempt_timeout` and `request_deadline` bound the time until the response commits: the full body for non-stream requests, the first chunk for streams. A committed stream is not cut by either timer, only by client disconnect or upstream failure.
- Every attempt is recorded in an in-memory attempt trace (`provider`, `model`, `status`, `kind`, `latency_ms`). The total count goes to `X-Gateway-Attempts`, and the trace goes to the ledger (`attempts` jsonb).

## Retry and backoff

```yaml
resilience:
  max_attempts_per_provider: 2
  backoff_base: 100ms
  backoff_cap: 1s
  attempt_timeout: 15s
  request_deadline: 30s
```

- Full jitter: `wait = rand(0, min(cap, base * 2^(attempt-1)))`.
- If the upstream sent `Retry-After`, then `wait = max(wait, retryAfter)`. If that `wait` exceeds `backoff_cap`, the gateway does not retry this provider and fails over immediately.
- The randomness source and the clock are injected, so tests are deterministic.

## Circuit breaker

One breaker per provider, held in memory and process-wide.

```yaml
breaker:
  window_size: 20
  min_calls: 5
  failure_ratio: 0.5
  open_duration: 10s
  half_open_probes: 1
```

### State machine

```
           failures/calls >= 0.5 and calls >= 5
 CLOSED ──────────────────────────────────────▶ OPEN
   ▲                                              │ open_duration elapsed
   │ probe succeeds                               ▼
   └──────────────────────────────────────── HALF_OPEN
                    probe fails ──────────────▶ OPEN (timer resets)
```

- **Closed**: every call is allowed. Outcomes go into a ring buffer of the last `window_size` results. The breaker evaluates the ratio after each record.
- **Open**: `Allow()` returns false until `open_duration` elapses. The gateway does not wait: the router skips this provider immediately.
- **Half-open**: `Allow()` returns true for exactly `half_open_probes` concurrent callers and false for all others. A successful probe moves the breaker to closed and clears the window. A failed probe moves it back to open.
- Only outcomes that "count as breaker failure" per the classification table are failures. `KindClient` and `KindCanceled` are not recorded at all.
- Each transition emits `breaker.changed` to the demo hub and updates `gateway_breaker_state{provider}` (0 closed, 1 half-open, 2 open).
- The breaker is written by hand (around 100 lines) behind a `Clock` interface. No breaker library.

## Streaming failover

Rule: **failover is allowed only before the first byte is written to the client.**

1. The router opens an upstream stream and pulls the first chunk before writing response headers.
2. If opening the stream or reading the first chunk fails, that counts as a failed attempt, and retry and failover apply as usual.
3. Once the first chunk arrives, headers (including `X-Gateway-Provider` and `X-Gateway-Attempts`) are written and the chunk is flushed. From this point the provider is committed.
4. A failure after commit is recorded as a breaker failure. The gateway writes `data: {"error":{"message":"upstream stream interrupted","type":"upstream_error","code":"stream_interrupted"}}`, then `data: [DONE]`, and closes the connection. The ledger records status `502` with partial usage when known.
5. Partial responses are never written to the cache.

For OpenAI upstream streams, the adapter always sets `stream_options.include_usage = true` so cost accounting works. The usage chunk is forwarded to the client only if the client requested it.

## Concurrency and cancellation

- The client disconnecting cancels the request context, which cancels in-flight upstream calls and backoff sleeps.
- Upstream HTTP clients use a dedicated `http.Transport` per provider with `MaxIdleConnsPerHost: 32`, `ResponseHeaderTimeout` equal to `attempt_timeout`, and HTTP/2 enabled.

## Metrics

- `gateway_provider_attempts_total{provider,model,outcome}`, where `outcome` is `success` or an `ErrorKind`.
- `gateway_provider_latency_seconds{provider,model}` histogram, measuring time to the first byte for streams.
- `gateway_breaker_state{provider}` gauge.
- `gateway_failovers_total{from,to}`.

## Acceptance criteria

- A table-driven test covers every classification row.
- Breaker tests with a fake clock: it opens after 3 failures out of 5 calls, stays open for exactly 10s, and lets exactly one half-open probe through under concurrency.
- With chaos `down` on Anthropic and alias `fast`, the response comes from OpenAI with `X-Gateway-Attempts` = 3 (two Anthropic attempts, one OpenAI) while the breaker is closed. The breaker opens on the fifth Anthropic failure, so the third request makes 2 attempts. From then on `X-Gateway-Attempts` = 1.
- A stream that fails before its first chunk fails over transparently. A stream that fails after its first chunk ends with the error event and `[DONE]`.
- The time until commit never exceeds `request_deadline` plus 50ms.
