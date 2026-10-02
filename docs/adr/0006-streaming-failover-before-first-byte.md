# 0006. Streaming failover only before the first byte

- Status: Accepted
- Date: 2026-10-01

## Context

With `stream: true`, the gateway forwards chunks as they arrive. Once bytes reach the client, switching providers would produce a response stitched together from two models, which is incorrect.

## Decision

The gateway buffers nothing beyond the provider's first event. Retries and fallback are allowed until the first byte is written to the client. After that, an upstream failure terminates the stream with an OpenAI-style error event and the `[DONE]` sentinel is not sent. The ledger records the request as `failed_mid_stream`.

Anthropic SSE events are translated into OpenAI `chat.completion.chunk` events. For OpenAI upstreams, the gateway injects `stream_options.include_usage` so that token usage is always available for the ledger.

Cache hits on streaming requests are replayed as a chunked stream with the same event shape.

## Consequences

- Failover works for the dominant failure mode (connection refused, 5xx, timeout before headers).
- Mid-stream failures are visible to the client instead of silently corrupted.
- Time to first byte becomes a first-class metric.

## Alternatives considered

- Buffer the full response before sending: enables failover at any point but defeats streaming.
- Resume on another provider with the partial text as context: produces inconsistent output and is hard to reason about.
