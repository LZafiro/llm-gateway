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

By default the dev stack routes both `anthropic` and `openai` to a local mock provider that speaks both wire formats, so no API keys are needed. To use the real providers, set `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_BASE_URL=https://api.anthropic.com` and `OPENAI_BASE_URL=https://api.openai.com` in `.env`.

Create a gateway API key (printed once, active within 30s):

```sh
docker compose exec gateway gateway keys create --name local --rate 2 --burst 10
```

```sh
curl localhost:8080/v1/chat/completions \
  -H "Authorization: Bearer $GATEWAY_KEY" \
  -d '{"model":"fast","messages":[{"role":"user","content":"Hello"}]}'
```

Every request is recorded in the `ledger` table and exported as `gateway_*` metrics on `/metrics` (Prometheus on `localhost:9090`).

| Command | Purpose |
|---|---|
| `make test` | Unit tests with the race detector |
| `make test-integration` | Integration tests against Postgres and Redis containers |
| `make lint` | No-comments check and golangci-lint |
| `make fmt` | gofumpt and goimports |
