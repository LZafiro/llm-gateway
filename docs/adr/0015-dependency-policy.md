# 0015. Dependency policy

- Status: Accepted
- Date: 2026-10-01

## Context

A small dependency graph keeps the codebase readable, the image small and the design visible.

## Decision

Use the standard library first. Each third-party dependency must replace something unreasonable to write by hand.

| Concern | Choice |
|---|---|
| HTTP routing | `net/http` `ServeMux` with method patterns |
| Postgres | `jackc/pgx/v5` |
| SQL access | `sqlc`, with hand-written SQL and generated typed code |
| Vectors | `pgvector/pgvector-go` |
| Migrations | `pressly/goose`, SQL files embedded in the binary |
| Redis | `redis/go-redis/v9` |
| Metrics | `prometheus/client_golang` |
| Logging | `log/slog` |
| Config | `gopkg.in/yaml.v3` plus environment variables for secrets |
| Integration tests | `testcontainers/testcontainers-go` |

Tests use the standard `testing` package without assertion libraries. Provider SDKs, web frameworks, DI containers and config frameworks are excluded.

## Consequences

- Every import is explainable in one sentence.
- Some code that a framework would provide is written by hand, deliberately.
