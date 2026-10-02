# 0001. Modular monolith in a single binary

- Status: Accepted
- Date: 2026-10-01

## Context

The project is a portfolio demo that must be cheap to run, simple to deploy and easy to read. It contains a gateway core (OpenAI-compatible API, routing, resilience, caching, rate limiting, ledger) and a demo layer (playground page, live panel, public chaos button).

## Decision

Ship one Go binary, `cmd/gateway`, that serves the OpenAI-compatible API, the demo endpoints, the SSE live panel and the embedded static page. It also exposes `migrate` and `keys create` subcommands.

Boundaries are enforced through packages under `internal/`. `internal/gateway` owns the request pipeline and depends only on interfaces. Concrete adapters (providers, caches, store, ledger) are wired in `cmd/gateway`. `internal/demo` calls the gateway service in process rather than over HTTP, so playground traffic goes through exactly the same pipeline as external clients without an extra network hop.

Two auxiliary binaries exist only for development and benchmarking: `cmd/mockprovider` and `cmd/loadgen`.

## Consequences

- One image, one container, one deploy unit.
- The demo layer can be removed without touching the core.
- The core cannot be scaled independently of the demo, which is acceptable at this scale (see 0002).

## Alternatives considered

- Separate gateway and demo services: cleaner runtime isolation, but doubles deployment and adds an internal hop that distorts overhead measurements.
