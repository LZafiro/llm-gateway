# 0008. Hand-rolled circuit breaker and retry policy

- Status: Accepted
- Date: 2026-10-01

## Context

Resilience is the core of the project. A library breaker would reduce the central piece of the design to an import.

## Decision

**Retry**, per provider attempt:

- Only Retryable Errors are retried. Client errors (4xx other than 429) never retry or fall back.
- At most 2 attempts per provider.
- Exponential backoff with full jitter, base 100ms, cap 1s.
- Each attempt has its own 15s timeout.
- `Retry-After` is honoured when it fits within the backoff cap; a longer `Retry-After` fails over immediately instead of waiting.
- A malformed upstream response skips straight to the next provider without retrying.

**Circuit breaker**, one per provider, hand-written with an injected `Clock`:

- Count-based sliding window of the last 20 calls.
- Opens when there are at least 5 calls and a failure rate of at least 50%.
- Stays open for 10s, then goes half-open and admits a single probe.
- The probe's success closes the breaker and its failure re-opens it.
- Only Retryable Errors count as failures.

**Fallback**: an open breaker or an exhausted provider moves immediately to the next model in the route.

**Deadline**: every request has a total deadline of 30s, propagated through `context.Context`, so that retries plus fallbacks cannot exceed it.

All values are configurable. State transitions are published to the live panel and exported as metrics.

## Consequences

- Deterministic tests with a fake clock, with no sleeps in tests.
- About a hundred lines of focused code that reviewers can read in one sitting.

## Alternatives considered

- `sony/gobreaker` or `failsafe-go`: mature, but they hide the logic that the project is meant to showcase.
- Time-based error-rate window: smoother under bursty traffic, but harder to reason about at demo traffic levels.
