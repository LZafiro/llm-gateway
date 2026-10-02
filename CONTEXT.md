# Context

LLM Gateway is an OpenAI-compatible HTTP gateway written in Go that sits between clients and LLM providers. It adds fallback, retries, circuit breaking, exact and semantic caching, per-key rate limiting, cost accounting and fault injection. A public playground lets visitors chat through the gateway while a live panel shows what the gateway is doing, including a button that takes a provider down to demonstrate failover.

## Glossary

**Tenant**
The owner of an API key. Caches, rate limits and ledger entries are scoped to a tenant. The public playground is a single tenant.

**API Key**
A bearer credential with the `gw_` prefix. Only its SHA-256 hash is stored, together with its token bucket `rate` and `burst`.

**Provider**
An upstream LLM vendor reached through a hand-rolled client: `anthropic`, `openai`, or `mock`.

**Model**
A concrete provider model, written `provider/model` (for example `anthropic/claude-haiku-4-5`).

**Alias**
A virtual model name (for example `fast`) that a client may send instead of a concrete model. An alias resolves to a Route.

**Route**
An ordered fallback chain of Models. Requests for a concrete model have a single-element route and never fall back.

**Attempt**
One call to one Provider for one request. A request may make several attempts across retries and fallbacks.

**Retryable Error**
A transient failure (timeout, connection error, 429, 500, 502, 503, 504, provider overload) that permits a retry on the same provider or a fallback to the next one. Client errors never retry.

**First Byte**
The moment the gateway writes the first response byte to the client. Fallback is allowed only before it.

**Breaker**
A per-provider circuit breaker with states `closed`, `open` and `half-open`, driven by a count-based sliding window.

**Chaos Rule**
A per-provider fault injection setting (`down`, `error_rate`, `latency_ms`, `jitter_ms`, `expires_at`) applied by a decorator around the provider client.

**Exact Hit**
A response served from Redis because the normalized request hash matched a stored entry.

**Semantic Hit**
A response served from pgvector because a single-turn prompt's embedding is at least `threshold` similar to a stored prompt with the same alias and system prompt.

**Threshold**
The minimum cosine similarity for a Semantic Hit. Chosen by the threshold experiment.

**False Hit**
A Semantic Hit whose stored answer is wrong for the new prompt, measured against labelled pairs.

**Ledger Entry**
One row per request recording tenant, route, effective provider and model, attempts, cache outcome, tokens, cost, savings, latency and status.

**Saved Cost**
The original cost of a cached response, credited to the ledger when the response is served from cache.

**Budget**
The daily US$ cap for the public playground. When exhausted, playground traffic is served from cache or by the mock provider.

**Overhead**
Latency added by the gateway compared with calling the same upstream directly.
