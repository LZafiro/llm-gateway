# API

Public HTTP surface of the gateway. Clients written against the OpenAI Chat Completions API must work unchanged by pointing their base URL at the gateway and using a gateway key.

Related: [0004](../adr/0004-openai-compatible-surface.md), [0005](../adr/0005-model-aliases-and-fallback-chains.md), [0006](../adr/0006-streaming-failover-before-first-byte.md).

## Endpoints

| Method | Path | Auth | Purpose |
| --- | --- | --- | --- |
| `POST` | `/v1/chat/completions` | API key | Chat completion, JSON or SSE stream |
| `GET` | `/v1/models` | API key | Aliases and directly routable models |
| `GET` | `/healthz` | none | Liveness: process is up |
| `GET` | `/readyz` | none | Readiness: Postgres reachable, migrations applied |
| `GET` | `/metrics` | none, internal only | Prometheus exposition, blocked at Caddy |
| `POST` | `/demo/chat` | none, IP limited | See [demo.md](demo.md) |
| `GET` | `/demo/events` | none | See [demo.md](demo.md) |
| `POST` | `/demo/chaos/{provider}/down` | none, cooldown | See [chaos.md](chaos.md) |
| `GET` | `/demo/results` | none | See [demo.md](demo.md) |
| `PUT` | `/admin/chaos/{provider}` | admin token | See [chaos.md](chaos.md) |
| `DELETE` | `/admin/chaos/{provider}` | admin token | See [chaos.md](chaos.md) |

Redis being unreachable does not fail `/readyz`: the cache fails open (see [caching.md](caching.md)).

## Authentication

- Header: `Authorization: Bearer gw_<32 base62 chars>`.
- The gateway hashes the presented key with SHA-256 and looks it up in `api_keys` (schema in [rate-limiting.md](rate-limiting.md)).
- Active keys are loaded into memory at startup and refreshed every 30s. Lookup never hits Postgres on the request path.
- Missing, malformed or unknown keys and inactive keys return `401` `invalid_api_key`.

## `POST /v1/chat/completions`

### Supported request fields

| Field | Notes |
| --- | --- |
| `model` | Required. Alias (e.g. `fast`) or a direct model (`anthropic/claude-haiku-4-5`, `openai/<model>`). Bare provider model names are accepted when unambiguous. |
| `messages` | Required, non-empty. Roles `system`, `user`, `assistant`. `content` must be a string or an array of `{"type":"text","text":...}` parts. |
| `stream` | Optional, default `false`. |
| `stream_options.include_usage` | Accepted. The gateway always computes usage internally and emits the final usage chunk only if the client asked for it. |
| `max_tokens`, `max_completion_tokens` | Optional. If both are present, `max_completion_tokens` wins. Anthropic requires a value: the default is `1024`. |
| `temperature`, `top_p` | Optional, passed through. Anthropic `temperature` is clamped to `[0,1]`. |
| `stop` | Optional, string or array of up to 4 strings. |
| `user` | Optional, recorded in the ledger as `end_user`. |

### Rejected request fields

The following return `400` `unsupported_parameter` with `param` set to the field name: `tools`, `tool_choice`, `functions`, `function_call`, `n` (when `!= 1`), `logprobs`, `top_logprobs`, `response_format` (other than `{"type":"text"}`), `audio`, `modalities`, image or audio content parts, and the roles `tool` and `function`.

Unknown fields not listed anywhere are ignored, matching OpenAI client tolerance.

### Non-stream response

Standard OpenAI `chat.completion` object:

```json
{
  "id": "chatcmpl-<request id>",
  "object": "chat.completion",
  "created": 1767225600,
  "model": "claude-haiku-4-5",
  "choices": [{"index": 0, "message": {"role": "assistant", "content": "..."}, "finish_reason": "stop"}],
  "usage": {"prompt_tokens": 12, "completion_tokens": 40, "total_tokens": 52}
}
```

- `model` is the effective upstream model, not the alias.
- `finish_reason` mapping from Anthropic: `end_turn` → `stop`, `stop_sequence` → `stop`, `max_tokens` → `length`.
- On cache hits, `usage` reflects the original response's usage. The cost recorded in the ledger is `0` (see [ledger-and-cost.md](ledger-and-cost.md)).

### Stream response

`Content-Type: text/event-stream`. Each event is `data: <chat.completion.chunk JSON>\n\n` and the stream ends with `data: [DONE]\n\n`.

- The first chunk carries `delta.role = "assistant"`.
- Anthropic events are translated: `content_block_delta` (`text_delta`) → `delta.content`, `message_delta.stop_reason` → `finish_reason`, `message_start` and `message_delta` usage → final usage chunk.
- If the client requested `stream_options.include_usage`, a final chunk with `choices: []` and `usage` precedes `[DONE]`.
- Cache hits are replayed as a stream: the cached content is split into chunks of about 20 characters and written without artificial delay.
- An upstream failure after the first byte is emitted as an error event `data: {"error":{...}}` followed by `[DONE]`, then the connection is closed (see [routing-and-resilience.md](routing-and-resilience.md)).

### Response headers

Present on every completion response, stream or not, success or error where applicable:

| Header | Value |
| --- | --- |
| `X-Gateway-Request-Id` | ULID, also used in `id` and ledger |
| `X-Gateway-Provider` | `anthropic`, `openai`, `mock`, or `cache` on hits |
| `X-Gateway-Model` | Effective upstream model |
| `X-Gateway-Cache` | `miss`, `exact`, `semantic`, `bypass` |
| `X-Gateway-Attempts` | Total upstream attempts across the chain (`0` on cache hits) |
| `X-Gateway-Similarity` | Cosine similarity, semantic hits only, 4 decimals |
| `X-RateLimit-Limit` | Bucket capacity (burst) |
| `X-RateLimit-Remaining` | Whole tokens left after this request |
| `X-RateLimit-Reset` | Seconds until the bucket is full |
| `Retry-After` | Seconds, on `429` and `503` |

On cache hits, `X-Gateway-Provider` reports `cache` and `X-Gateway-Model` reports the model that originally produced the cached answer.

### Request headers

| Header | Effect |
| --- | --- |
| `X-Gateway-Cache: bypass` | Skip cache lookup and cache write |
| `Cache-Control: no-cache` | Same as bypass |

## `GET /v1/models`

```json
{
  "object": "list",
  "data": [
    {"id": "fast", "object": "model", "created": 0, "owned_by": "gateway"},
    {"id": "anthropic/claude-haiku-4-5", "object": "model", "created": 0, "owned_by": "anthropic"}
  ]
}
```

Lists every alias, then every directly routable model from the pricing table.

## Errors

All errors use the OpenAI envelope:

```json
{"error": {"message": "human readable", "type": "invalid_request_error", "param": "tools", "code": "unsupported_parameter"}}
```

| Status | `type` | `code` | When |
| --- | --- | --- | --- |
| 400 | `invalid_request_error` | `invalid_json` | Body is not valid JSON |
| 400 | `invalid_request_error` | `missing_required_parameter` | `model` or `messages` missing |
| 400 | `invalid_request_error` | `unsupported_parameter` | See rejected fields |
| 400 | `invalid_request_error` | `invalid_value` | Bad role, empty messages, out of range values |
| 401 | `authentication_error` | `invalid_api_key` | Missing or unknown key |
| 404 | `invalid_request_error` | `model_not_found` | Unknown alias or model |
| 413 | `invalid_request_error` | `request_too_large` | Body > 1 MiB |
| 429 | `rate_limit_error` | `rate_limit_exceeded` | Token bucket empty |
| 429 | `rate_limit_error` | `budget_exceeded` | Demo budget exhausted and no fallback possible |
| 502 | `upstream_error` | `upstream_error` | Upstream returned non-retryable 5xx or malformed output |
| 503 | `upstream_error` | `all_providers_unavailable` | Chain exhausted (breakers open, retries spent) |
| 504 | `upstream_error` | `deadline_exceeded` | 30s request deadline hit |

Upstream `4xx` client errors (e.g. context length exceeded) are passed through with the upstream status and translated into the OpenAI envelope with `type` `invalid_request_error`. They never trigger retry or failover.

## Acceptance criteria

- The official OpenAI Python and Node SDKs, with only `base_url` and `api_key` changed, can do non-stream and stream completions against both `fast` and a direct Anthropic model.
- Each rejected field returns `400` with the correct `param`.
- Stream output from an Anthropic upstream is byte-for-byte valid OpenAI chunk JSON (validated against fixture-based golden files).
- All listed response headers are present on success, cache hit and `429` responses.
