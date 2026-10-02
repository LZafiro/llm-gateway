# Rate Limiting and API Keys

Per-key token bucket limiting of request rate, plus the API key lifecycle. A tenant is an API key: there is no separate tenant entity.

Related: [0002](../adr/0002-single-replica-in-memory-state.md), [0013](../adr/0013-demo-safety.md).

## API keys

### Schema

```sql
CREATE TABLE api_keys (
    id          BIGSERIAL PRIMARY KEY,
    name        TEXT        NOT NULL UNIQUE,
    key_hash    BYTEA       NOT NULL UNIQUE,
    key_prefix  TEXT        NOT NULL,
    rate_per_sec DOUBLE PRECISION NOT NULL,
    burst       INTEGER     NOT NULL,
    active      BOOLEAN     NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

- `key_hash`: SHA-256 of the full key.
- `key_prefix`: the first 10 characters (`gw_` plus 7), for display in logs and the CLI. The plaintext key is never stored.

### Format

`gw_` followed by 32 characters drawn from `[0-9A-Za-z]` using `crypto/rand`.

### CLI

```
gateway keys create --name demo --rate 5 --burst 20
gateway keys list
gateway keys revoke --name demo
```

`create` prints the plaintext key exactly once. The demo key is created at deploy time and placed in `DEMO_API_KEY`.

### In-memory key set

- Loaded at startup, then reloaded every 30s (`auth.refresh_interval`).
- Stored as `map[[32]byte]KeyInfo`, swapped atomically.
- If a reload fails, the previous set is kept and the failure is logged and counted.
- When a reload changes a key's `rate_per_sec` or `burst`, that key's bucket is reset.
- Comparison is a hash map lookup on the digest, so there is no constant-time concern on the plaintext.

## Token bucket

Each request costs 1 token.

### State

```go
type bucket struct {
	tokens   float64
	updated  time.Time
	lastSeen time.Time
}
```

Buckets are held in a `map[int64]*bucket` keyed by `api_keys.id`, guarded by a mutex. They are sharded into 16 shards by key id so contention stays low.

### Algorithm (lazy refill)

```
elapsed := now - b.updated
b.tokens = min(burst, b.tokens + elapsed.Seconds() * rate)
b.updated = now
if b.tokens >= 1:
    b.tokens -= 1
    allow
else:
    deny, retryAfter = ceil((1 - b.tokens) / rate) seconds
```

- A new bucket starts full (`tokens = burst`).
- There is no background ticker. The clock is injected.
- Eviction: a sweep every 5 minutes deletes buckets whose `lastSeen` is older than 10 minutes. Evicting a bucket is equivalent to letting it refill to full, so correctness is preserved.

### Headers

| Header | Value |
| --- | --- |
| `X-RateLimit-Limit` | `burst` |
| `X-RateLimit-Remaining` | `floor(tokens)` after the decision |
| `X-RateLimit-Reset` | `ceil((burst - tokens) / rate)` seconds |
| `Retry-After` | On 429 only |

### Position in the pipeline

The limiter runs after authentication and **before** cache lookup. Cache hits consume tokens, because the limit protects the gateway itself and not only the upstream providers.

## Interfaces

```go
type Limiter interface {
	Allow(ctx context.Context, keyID int64, rate float64, burst int) Decision
}
```

The in-memory implementation is the only one built. A Redis-backed implementation using a Lua script is a documented extension behind the same interface (see [0002](../adr/0002-single-replica-in-memory-state.md)).

## Demo per-IP limit

`/demo/chat` applies a separate fixed-capacity limiter keyed by client IP **before** the gateway pipeline: 20 messages per hour. It is implemented as a token bucket with `burst = 20` and `rate = 20/3600`. The client IP comes from the first entry of `X-Forwarded-For`, which is trusted only when the request comes from the Caddy container network. See [demo.md](demo.md).

## Metrics

- `gateway_ratelimit_rejections_total{key_name}`
- `gateway_ratelimit_buckets` gauge, the current bucket count.
- `gateway_demo_ip_rejections_total`

## Acceptance criteria

- With rate 1/s and burst 3 under a fake clock: 3 immediate requests pass, the 4th returns 429 with `Retry-After: 1`, and after 1s one more request passes.
- 1000 concurrent requests on one key never admit more than `burst` plus the refill amount (verified with `-race`).
- A revoked key is rejected within 30s of revocation without a restart.
- A 429 response carries the OpenAI error envelope with code `rate_limit_exceeded` and all rate-limit headers.
