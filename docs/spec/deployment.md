# Deployment

The gateway runs as a single replica on a small VPS with docker compose, one container per service. The same compose base runs locally.

Related: [0003](../adr/0003-vps-docker-compose.md), [0002](../adr/0002-single-replica-in-memory-state.md), [0014](../adr/0014-observability.md), [0015](../adr/0015-dependency-policy.md), [0016](../adr/0016-no-comments-policy.md).

## Host requirements

Provider-agnostic. Minimum 2 vCPU, 4 GB RAM, 40 GB disk, x86_64 or arm64, Docker Engine with the compose plugin, and a public IPv4. Only ports 22, 80 and 443 are open in the host firewall.

## Services

| Service | Image | Memory limit | Network | Ports exposed to host |
| --- | --- | --- | --- | --- |
| `caddy` | `caddy:2` | 64 MB | edge, internal | 80, 443 (prod) |
| `gateway` | `ghcr.io/lzafiro/llm-gateway:<sha>` | 256 MB | internal | 8080 (dev only) |
| `migrate` | same image, `gateway migrate up` | 128 MB | internal | none |
| `postgres` | `pgvector/pgvector:pg17` | 512 MB | internal | 5432 (dev only) |
| `redis` | `redis:7-alpine` | 64 MB | internal | 6379 (dev only) |
| `prometheus` | `prom/prometheus` | 256 MB | internal | 9090 (dev only) |
| `grafana` | `grafana/grafana` | 256 MB | internal | 3000 (dev/bench only) |
| `mockprovider` | same image, `mockprovider` binary | 64 MB | internal | none (dev/bench only) |

### Service settings

- **postgres**: `shared_buffers=128MB`, `effective_cache_size=256MB`, `work_mem=4MB`, `max_connections=30`. Named volume `pgdata`.
- **redis**: `--maxmemory 48mb --maxmemory-policy allkeys-lru --save "" --appendonly no`. The cache is disposable.
- **prometheus**: `--storage.tsdb.retention.time=7d`, scraping `gateway:8080/metrics` every 15s. Named volume `promdata`.
- **gateway**: `GOMEMLIMIT=200MiB`, `depends_on: migrate (service_completed_successfully), postgres (healthy)`, healthcheck on `/healthz`, `restart: unless-stopped`, `stop_grace_period: 20s`.

## Compose files

| File | Purpose |
| --- | --- |
| `compose.yaml` | Base: gateway, migrate, postgres, redis, prometheus, networks, volumes |
| `compose.override.yaml` | Dev, auto-loaded: local build, exposed ports, grafana, mockprovider, debug logging |
| `compose.bench.yaml` | Bench profile: provider base URLs pointing at the mockproviders, grafana |
| `compose.prod.yaml` | Prod: GHCR image by tag, caddy, memory limits, no exposed ports except caddy |

The Makefile wraps them: `make up` (dev), `make bench-up`, and on the VPS `docker compose -f compose.yaml -f compose.prod.yaml up -d`.

## Caddy

```
gateway.example.dev {
	encode gzip
	@metrics path /metrics
	respond @metrics 404
	reverse_proxy gateway:8080 {
		flush_interval -1
	}
}
```

- `flush_interval -1` is needed so SSE and stream responses are not buffered.
- TLS is automatic via Let's Encrypt. The domain is a subdomain of the author's site, set through `GATEWAY_DOMAIN`.
- `/admin/*` is reachable over TLS and is protected only by `ADMIN_TOKEN`.

## Image

Multi-stage Dockerfile:

1. `golang:<version>` builder: `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=<sha>"` for `gateway`, `mockprovider` and `loadgen`.
2. `gcr.io/distroless/static-debian12:nonroot` runtime with the binaries copied in. `web/`, migrations and `results/summary.json` are embedded in `gateway`.

Images are published to `ghcr.io/lzafiro/llm-gateway:<git sha>` and `:main`.

## Migrations

- `goose` with SQL files in `internal/store/migrations/`, embedded via `embed`.
- `gateway migrate up` runs in the one-shot `migrate` service before `gateway` starts. Startup fails if migrations are pending (checked in `/readyz` as well).
- `CREATE EXTENSION IF NOT EXISTS vector` is the first migration.
- Migrations are forward-only in production. `down` files exist for local use.

## Secrets

The VPS holds `/opt/llm-gateway/.env` with mode `600`: `POSTGRES_PASSWORD`, `DATABASE_URL`, `REDIS_URL`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `ADMIN_TOKEN`, `DEMO_API_KEY`, `GATEWAY_DOMAIN`. The file is never committed. `.env.example` lists the keys.

## CI/CD (GitHub Actions)

`ci.yml` runs on every push and PR:

1. `gofumpt -l` check, `golangci-lint run`.
2. No-comments check: `scripts/check-no-comments.sh` fails on any `//` or `/* */` comment in `*.go` outside `//go:` directives, `//nolint:` and files with the `Code generated ... DO NOT EDIT.` header (see [0016](../adr/0016-no-comments-policy.md)).
3. `sqlc diff` to ensure generated code is up to date.
4. `go test -race ./...`
5. `go test -race -tags integration ./...` using testcontainers (Docker available on the runner).
6. `docker build`.

`deploy.yml` runs on push to `main` after CI passes:

1. Build and push the image to GHCR with tags `<sha>` and `main`.
2. SSH to the VPS (key in the `VPS_SSH_KEY` secret, host in `VPS_HOST`) and run:

```
cd /opt/llm-gateway
export IMAGE_TAG=<sha>
docker compose -f compose.yaml -f compose.prod.yaml pull
docker compose -f compose.yaml -f compose.prod.yaml up -d
```

3. Smoke test: `curl -fsS https://$GATEWAY_DOMAIN/readyz`, retried for 60s. If it fails, the job redeploys the previous tag (stored in `/opt/llm-gateway/.last_tag`).

Deploys cause a few seconds of downtime. That is accepted for a single replica: in-memory state (breakers, chaos, buckets) resets on restart (see [0002](../adr/0002-single-replica-in-memory-state.md)).

## Backups

None. The ledger and caches are disposable demo data with 30-day retention. Named volumes survive redeploys.

## Logs

The gateway writes JSON to stdout via `slog`. Docker's `json-file` driver is configured with `max-size: 10m` and `max-file: 3`. Request logs include `request_id`, `key_prefix`, `model`, `provider`, `cache`, `status`, `latency_ms` and `attempts`. Message content is never logged.

## Acceptance criteria

- `make up` on a clean clone with Docker brings the full stack up, and `curl localhost:8080/readyz` returns 200 within 60s.
- On a fresh VPS, following `docs/runbook.md` (written during implementation) produces a working HTTPS deployment.
- `https://<domain>/metrics` returns 404, while the Prometheus target is up internally.
- Stream responses through Caddy arrive incrementally (first byte in under 1s with the mock provider).
