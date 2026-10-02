# 0007. Hand-rolled provider clients

- Status: Accepted
- Date: 2026-10-01

## Context

The gateway needs precise control over timeouts, error classification (retryable or not), the first byte of a stream, and usage extraction. It must already parse and emit the OpenAI wire format because that is its own API.

## Decision

Implement thin HTTP clients for Anthropic and OpenAI using `net/http` and a small shared SSE reader. They implement a common interface:

```go
type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (Response, error)
	Stream(ctx context.Context, req Request) (Stream, error)
}
```

Errors are mapped to a typed `*provider.Error` carrying a kind (`Timeout`, `Connection`, `RateLimited`, `Overloaded`, `Server`, `Client`, `Malformed`, `Canceled`) and an optional `RetryAfter`. Retry and fallback decisions use only this type.

Adapters are tested against `httptest` servers replaying real SSE fixtures captured once from each provider.

## Consequences

- No SDK abstraction hides the behaviour the project exists to demonstrate.
- Provider API changes must be tracked manually. This is acceptable for the small text-only surface (see 0004).

## Alternatives considered

- Official `openai-go` and `anthropic-sdk-go`: less code, but they hide retries, error types and stream internals, and add large dependency trees.
