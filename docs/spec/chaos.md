# Chaos

Fault injection per provider, so failover can be shown and measured on demand. Chaos wraps the provider adapter. Retries, breakers, metrics and the ledger therefore see injected faults exactly as they would see real ones.

Related: [0012](../adr/0012-chaos-as-provider-decorator.md), [0013](../adr/0013-demo-safety.md).

## Rule

```go
type Rule struct {
	Down      bool
	ErrorRate float64
	Latency   time.Duration
	Jitter    time.Duration
	ExpiresAt time.Time
	Source    string
}
```

| Field | Range | Effect |
| --- | --- | --- |
| `Down` | bool | Every call fails immediately with a synthetic `KindConnection` error |
| `ErrorRate` | `[0,1]` | Each call fails with a synthetic HTTP 503 (`KindServer`) with this probability |
| `Latency` | `[0, 20s]` | Added before the call |
| `Jitter` | `[0, 5s]` | Uniform random `[0, Jitter]` added on top of `Latency` |
| `ExpiresAt` | timestamp | The rule is ignored after this time. A zero value means no expiry, which only the admin API may set |
| `Source` | `admin` or `public` | Shown on the panel |

The rule store is in memory (`map[provider]Rule` under an `RWMutex`). Expired rules are removed lazily on read, and a 1s ticker emits `chaos.changed` when a rule expires so the panel updates without polling.

## Decorator semantics

```
call(ctx, req):
    rule := store.Get(provider)
    if rule.Latency + jitter > 0: sleep honoring ctx
    if rule.Down: return synthetic connection error
    if rand() < rule.ErrorRate: return synthetic 503
    return inner.call(ctx, req)
```

- Injected latency counts toward the per-attempt timeout and the request deadline.
- For streams, faults are injected before the stream opens, so they fall in the pre-first-byte failover window. Mid-stream faults are out of scope.
- Synthetic errors are marked `injected=true` in the attempt trace and the ledger `attempts` JSON, and labeled in `gateway_chaos_injections_total{provider,kind}`.
- The randomness source and the clock are injected.
- Chaos never applies to the embedder, Redis or Postgres.

## Admin API

`Authorization: Bearer <ADMIN_TOKEN>`. Comparison is constant-time.

`PUT /admin/chaos/{provider}`

```json
{"down": false, "error_rate": 0.3, "latency_ms": 800, "jitter_ms": 200, "ttl_seconds": 300}
```

- `ttl_seconds` is optional. If omitted, the rule has no expiry.
- Returns `200` with the stored rule, `400` on out-of-range values, `404` for an unknown provider.

`DELETE /admin/chaos/{provider}` clears the rule and returns `204`.

## Public control

`POST /demo/chaos/{provider}/down`

- Sets `{Down: true, ExpiresAt: now + 30s, Source: "public"}`.
- Each provider has a 60s cooldown, measured from when the button was last pressed. During the cooldown the endpoint returns `429` with `Retry-After` and the remaining seconds.
- If an admin rule is active for that provider, the endpoint returns `409` and the admin rule is not overwritten.
- No other public mutations exist. Nobody can bring a provider back early, set latency, or set an error rate.
- The response is `202 {"provider":"anthropic","down_until":"..."}`, and the hub broadcasts `chaos.changed`.

The state is global: every visitor sees the same outage (see [0013](../adr/0013-demo-safety.md)).

## Configuration

```yaml
chaos:
  public:
    enabled: true
    down_duration: 30s
    cooldown: 60s
  max_latency: 20s
```

## Acceptance criteria

- With `ErrorRate = 0.3` and a seeded RNG over 1000 calls, the injected failure count is within 30% ± 3%.
- A `Down` rule on Anthropic plus alias `fast` produces OpenAI responses, with the breaker opening after 5 calls.
- A public down expires after exactly 30s under a fake clock and emits `chaos.changed`.
- A second public press within 60s returns 429 with a correct `Retry-After`.
- An injected error is indistinguishable from a real one to the router (same `provider.Error` kinds).
