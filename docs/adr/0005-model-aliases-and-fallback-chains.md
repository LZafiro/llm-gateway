# 0005. Model aliases and fallback chains

- Status: Accepted
- Date: 2026-10-01

## Context

Falling back from one provider to another necessarily changes the model. If clients request concrete model names, the gateway would have to silently pick an "equivalent" model, which is opaque and surprising.

## Decision

Clients choose between two kinds of model names:

- **Alias** (for example `fast`): resolves to an ordered route such as `[anthropic/claude-haiku-4-5, openai/gpt-4o-mini]`. Fallback walks the route in order.
- **Concrete model** (for example `claude-haiku-4-5` or `anthropic/claude-haiku-4-5`): routed to that provider only, with retries but no fallback.

Aliases, routes and per-model prices are declared in the YAML config. The response reports the model that actually answered in both the `model` body field and the `X-Gateway-Model` header, and the provider in `X-Gateway-Provider`.

## Consequences

- Fallback semantics are explicit and opt-in.
- Drop-in clients that send real model names still work.
- Cache keys include the requested alias or model, so different routes never share entries.

## Alternatives considered

- Equivalence table between providers' models: implicit, hard to explain and easy to get wrong.
- Aliases only: breaks drop-in compatibility.
