# LLM Gateway

An OpenAI-compatible LLM gateway in Go with provider fallback, retries, circuit breaking, exact and semantic caching, per-key rate limiting, cost accounting and chaos injection, plus a live playground that shows failover as it happens.

Status: under construction, see [docs/roadmap.md](docs/roadmap.md).

## Documentation

- [CONTEXT.md](CONTEXT.md): domain glossary
- [docs/adr](docs/adr): architecture decision records
- [docs/spec](docs/spec): component specifications

## Development

Requirements: Go 1.26, Docker, golangci-lint v2.

```sh
cp .env.example .env
make up
curl localhost:8080/readyz
```

| Command | Purpose |
|---|---|
| `make test` | Unit tests with the race detector |
| `make test-integration` | Integration tests against Postgres and Redis containers |
| `make lint` | No-comments check and golangci-lint |
| `make fmt` | gofumpt and goimports |
