# 0014. Observability: Prometheus in production, Grafana locally

- Status: Accepted
- Date: 2026-10-01

## Context

The project must expose Prometheus metrics. The VPS has limited memory, and the public live panel already serves as the visual showcase.

## Decision

- The gateway exposes `/metrics` with `prometheus/client_golang`. All metric names are prefixed with `gateway_`.
- Prometheus runs in production on the internal network with 7 day retention. Caddy blocks `/metrics` publicly.
- Grafana runs only in the local and benchmark compose stack. A versioned dashboard lives in `deploy/grafana/`, and screenshots of benchmark runs are committed with the results.
- The live panel does not query Prometheus. It uses in-process counters and ledger totals pushed over SSE.
- Logs are structured JSON via `log/slog`, with a request id that is also returned in `X-Gateway-Request-Id`.

## Consequences

- Production metrics exist for real incidents without paying for a public Grafana.
- The dashboard is reproducible from the repo.

## Alternatives considered

- Public anonymous Grafana: an extra 150MB of RAM and more attack surface for marginal portfolio value.
