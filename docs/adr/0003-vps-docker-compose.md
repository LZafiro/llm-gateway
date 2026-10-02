# 0003. Single VPS with Docker Compose

- Status: Accepted
- Date: 2026-10-01

## Context

The demo must be cheap and predictable to host, with the same topology locally and in production. Serverless options were considered, but they need managed Postgres and Redis, cannot host Prometheus, and scale-to-zero cold starts weaken a live failover demo.

## Decision

Run everything on one small VPS with Docker Compose, one container per concern:

| Container | Image | Memory limit |
|---|---|---|
| caddy | caddy | 64MB |
| gateway | ghcr.io/lzafiro/llm-gateway | 256MB |
| migrate | same image, `gateway migrate`, one-shot | n/a |
| postgres | pgvector/pgvector | 512MB |
| redis | redis, `maxmemory 48mb`, `allkeys-lru` | 64MB |
| prometheus | prom/prometheus, 7 day retention | 256MB |

The VPS vendor is left open. The minimum requirement is 2 vCPU, 4GB RAM and Docker with the Compose plugin.

Caddy is the only container that publishes ports (80 and 443). It terminates TLS automatically on a subdomain of the author's site and blocks `/metrics` and `/admin/*` from the public internet. All other containers share an internal network.

Compose files:

- `compose.yaml`: base services.
- `compose.override.yaml`: local development, with exposed ports, Grafana, the mock provider and local builds.
- `compose.prod.yaml`: production, with the GHCR image, Caddy and resource limits.

Deployment runs from GitHub Actions: lint and test, build a multi-stage static image, push it to GHCR, then over SSH run `docker compose pull`, the `migrate` one-shot, and `up -d`.

Volumes are named and survive redeploys. There are no backups. Ledger and caches are disposable demo data, and the ledger keeps 30 days.

## Consequences

- Fixed, low monthly cost with no surprise bills.
- `make up` reproduces production locally.
- A single host is a single point of failure, which is accepted for a demo.

## Alternatives considered

- Cloud Run with Neon and Upstash: zero idle cost, but multi-instance state, no in-cluster Prometheus, CPU throttling outside requests and cold starts.
- Managed Postgres and Redis: reliable, but they cost more than the whole VPS.
