# Demo

The public playground: a chat with an assistant next to a live gateway panel, plus a button that takes a provider down so visitors can watch failover happen. The demo is a thin layer over the gateway core and adds no behaviour to it.

Related: [0001](../adr/0001-modular-monolith.md), [0013](../adr/0013-demo-safety.md), [0014](../adr/0014-observability.md).

## Page

- Static assets live in `web/` and are embedded with `embed`, served at `/`.
- Vanilla JS (ES modules, no build step), hand-written CSS, and uPlot vendored into `web/vendor/`.
- Layout: chat on the left and the gateway panel on the right on desktop; stacked on mobile.
- Tabs: **Playground** and **Results**.

### Chat

- The visitor's history is kept in browser memory only. Each send posts the last 10 messages.
- Responses stream token by token.
- Under each assistant message there is a badge line: provider, cache layer (with similarity on semantic hits), latency, cost or savings, and attempts.
- Starter prompts are offered, including some paraphrased pairs, so visitors can trigger semantic hits on purpose.

### Panel

| Widget | Source |
| --- | --- |
| Requests today, hit rate, cost today, saved today | `snapshot` totals plus `request.completed` deltas |
| Requests/s and latency sparklines (last 2 min) | `request.completed` events, bucketed per second client-side |
| Provider cards: breaker state, chaos state, countdown | `breaker.changed`, `chaos.changed` |
| "Take down" button per provider | `POST /demo/chaos/{provider}/down`, disabled during cooldown |
| Live event log (last 20 requests) | `request.completed` |
| Budget meter | `budget.changed` |
| Semantic cache status | `embedder.changed` |

### Results tab

Renders `GET /demo/results`: the threshold curve, cache on/off comparison, overhead percentiles and failover timeline, each with a short methodology note linking to the repository.

## Endpoints

### `POST /demo/chat`

Request:

```json
{"messages": [{"role": "user", "content": "..."}]}
```

Steps:

1. Per-IP limiter: 20 messages/hour. Exceeding it returns `429` with `Retry-After`.
2. Validation: at most 10 messages (older ones are dropped server-side as well), each message at most 2000 characters, roles limited to `user` and `assistant`.
3. The handler prepends a fixed system prompt and builds `ChatRequest{model: "fast", max_tokens: 512, stream: true}`.
4. The budget check selects the mode (see below).
5. It calls the gateway service **in-process** with the identity of `DEMO_API_KEY`. The full pipeline runs: rate limit, cache, router, ledger with `source = demo`. There is no HTTP hop to itself.
6. The response streams in OpenAI chunk format. Gateway metadata is sent as a final SSE event `event: meta` with `{provider, model, cache, similarity, attempts, latency_ms, cost_usd, saved_usd}` before `[DONE]`. The in-page client cannot read response headers mid-stream reliably.

Caps applied regardless of client input: `max_tokens` 512, `temperature` 0.3, model `fast`.

### `GET /demo/events`

SSE stream, `Content-Type: text/event-stream`, `Cache-Control: no-cache`, and a keep-alive comment every 15s.

On connect:

```
event: snapshot
data: {"totals":{"requests":120,"hits":48,"cost_usd":0.0312,"saved_usd":0.0207},
       "providers":{"anthropic":{"breaker":"closed","chaos":null},
                    "openai":{"breaker":"open","chaos":{"down":true,"until":"...","source":"public"}}},
       "budget":{"limit_usd":1.0,"spent_usd":0.0312,"mode":"live"},
       "embedder":"closed"}
```

Delta events:

| Event | Payload |
| --- | --- |
| `request.completed` | `{id, at, source, provider, model, cache, similarity, attempts, status, latency_ms, cost_usd, saved_usd}` |
| `breaker.changed` | `{provider, from, to, at}` |
| `chaos.changed` | `{provider, rule \| null, cooldown_until}` |
| `budget.changed` | `{spent_usd, limit_usd, mode}` |
| `embedder.changed` | `{state}` |

- Events cover all gateway traffic (`api` and `demo` sources). They never include message content, keys or IPs.
- Hub: each subscriber gets a buffered channel (capacity 64). Publishing is non-blocking. If a subscriber's buffer is full, that subscriber is disconnected and the client reconnects (`EventSource` auto-retry, with `retry: 3000` sent on connect).
- Subscriber limit: 200 concurrent. Beyond that the endpoint returns `503`.

### `POST /demo/chaos/{provider}/down`

See [chaos.md](chaos.md).

### `GET /demo/results`

Serves `results/summary.json`, embedded at build time and produced by the benchmark and experiment runs (see [benchmarks.md](benchmarks.md) and [threshold-experiment.md](threshold-experiment.md)). It is cached with `Cache-Control: public, max-age=3600`.

## Budget

```yaml
demo:
  daily_budget_usd: 1.00
  max_tokens: 512
  max_messages: 10
  max_message_chars: 2000
  ip_limit_per_hour: 20
  system_prompt: "You are a concise assistant for an LLM gateway demo. Answer in at most 120 words."
```

- `spent_usd` is the in-memory sum of `cost_usd + embedding_cost_usd` for all `source = demo` entries today (UTC). It is seeded from the ledger at startup and reset at UTC midnight.
- Modes:
  - `live`: `spent < limit`. Normal pipeline.
  - `cache_or_mock`: `spent >= limit`. The demo request runs the cache layers only. On a miss, it is answered by the in-process `mock` provider with a canned message ("The demo's daily budget is exhausted; this is a simulated response. Cached answers still work."). The mock response is marked `provider = mock` and is never written to the cache.
- A mode change emits `budget.changed`.
- The budget applies only to `source = demo`. Direct API keys are governed only by their rate limits.

## Client IP

The client IP is taken from the first value of `X-Forwarded-For`, which is trusted only when `RemoteAddr` belongs to the configured trusted proxy CIDR (`demo.trusted_proxies`, the Docker network of Caddy). Otherwise `RemoteAddr` is used.

## Acceptance criteria

- Opening two browser tabs and pressing "Take down" on Anthropic in one shows the outage, the breaker opening and the countdown in both within 1s.
- During the outage, chat replies keep arriving with provider `openai` and no visible errors.
- After the 30s expiry and one successful half-open probe, the Anthropic card returns to `closed`.
- Forcing `spent >= limit` switches new demo replies to cache hits or the mock message, and the meter shows `cache_or_mock`.
- A slow SSE client (paused reader) is disconnected without increasing completion latency for other requests.
