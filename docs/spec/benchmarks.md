# Benchmarks

Reproducible measurements of what the gateway costs and what it buys: overhead, cache impact and failover behaviour. Every benchmark runs locally with docker compose and writes machine-readable results to `results/`.

Related: [0014](../adr/0014-observability.md), [0003](../adr/0003-vps-docker-compose.md).

## Tooling

- `cmd/loadgen`: a Go load generator with subcommands `overhead`, `cache`, `failover` and `threshold` (see [threshold-experiment.md](threshold-experiment.md)).
- `cmd/mockprovider`: an HTTP server that speaks both OpenAI (`/v1/chat/completions`) and Anthropic (`/v1/messages`) wire formats, stream and non-stream, with configurable fixed latency (`MOCK_LATENCY`, default 200ms), time between chunks (`MOCK_CHUNK_INTERVAL`, default 10ms) and response length.
- Compose profile `bench`: gateway, postgres, redis, mockprovider, prometheus and grafana. In this profile the gateway points both providers' base URLs at `mockprovider`.
- Make targets: `make bench-overhead`, `make bench-cache`, `make bench-failover`, `make experiment-threshold`, `make results` (merges outputs into `results/summary.json`).

Load is open-loop at a fixed arrival rate: requests start on schedule regardless of whether earlier ones have completed. This avoids coordinated omission. Latencies are recorded in an HDR histogram (or exact sorted samples when n ≤ 100k).

Each run records environment metadata: CPU model, core count, Go version, git SHA, compose profile and parameters.

## B1: gateway overhead

Question: how many milliseconds does the gateway add over calling the provider directly?

Setup: mock latency 200ms, non-stream, fixed prompt. Six scenarios:

| Scenario | Path |
| --- | --- |
| `direct` | loadgen → mockprovider |
| `gw-miss` | loadgen → gateway → mockprovider, cache bypass |
| `gw-miss-full` | cache enabled, unique prompts (miss path including embedding against the mock embedder) |
| `gw-exact` | repeated prompt, exact hits |
| `gw-semantic` | paraphrase set, semantic hits |
| `gw-stream-ttfb` | stream, measures time to first byte versus direct |

Parameters: 50 req/s for 60s after a 10s warm-up that is discarded. Each scenario is run 3 times.

Reported per scenario: p50, p90, p99 and max latency. **Overhead = gw p50 − direct p50 and gw p99 − direct p99.** These are cross-checked against the in-gateway `gateway_overhead_seconds` histogram.

For B1 the mockprovider also serves `/v1/embeddings` with deterministic hash-based vectors, so no network call leaves the machine.

Output: `results/overhead.json`.

## B2: cache impact with simulated traffic

Question: how do latency and cost change with caching on versus off, and what hit rate does realistic traffic get?

- Dataset: `experiments/traffic/prompts.jsonl`, about 300 base questions, each with 0 to 3 paraphrases.
- Traffic: 1000 requests sampling base questions by Zipf (s = 1.1). Each draw picks the base question or one of its paraphrases uniformly.
- Run against **real providers** (alias `fast`) at 2 req/s with a fixed seed. Expected cost is under US$0.50.
- Two runs: cache disabled, then cache enabled (both layers, threshold from config). The cache is flushed before each run.

Reported: hit rate per layer, p50/p99 latency for hits versus misses, total cost, total saved, and cost per 1000 requests in each mode.

Output: `results/cache.json`.

## B3: failover

Question: how long until traffic is fully on the fallback provider, and how many requests fail during the outage?

- Setup: bench profile with mock providers. Each provider gets its own mockprovider instance (`mock-a` and `mock-b`), so they can be told apart.
- Traffic: alias `fast`, 20 req/s, non-stream, cache bypassed, for 90s.
- At t = 30s: `PUT /admin/chaos/anthropic {"down": true}`. At t = 60s: `DELETE`.
- Repeated with `{"error_rate": 0.5}` and with `{"latency_ms": 20000}` (the timeout path).

Reported:

- **Time to failover**: from the chaos start to the first request at or after which 100% of responses come from the fallback for 5 consecutive seconds.
- **Breaker open time**: from the chaos start to `breaker.changed` → open.
- **Requests lost**: client-visible non-2xx responses during the window (expected 0 for `down`, because retries fail over within the deadline).
- **Latency penalty**: p99 during the outage versus before it.
- **Recovery time**: from the chaos end to the first successful half-open probe.

A timeline of per-second success, failure and provider share is also recorded for the Results chart.

Output: `results/failover.json`.

## Grafana

`deploy/grafana/dashboards/gateway.json` is a versioned dashboard covering request rate, latency percentiles, overhead, cache hit rate, breaker states, cost and savings, and ledger drops. Screenshots taken during each benchmark are stored in `docs/img/`.

## Acceptance criteria

- `make bench-overhead` runs end to end on a clean clone with only Docker installed.
- Every result file includes its metadata block and can be re-generated deterministically except for latency values.
- `results/summary.json` validates against `results/summary.schema.json`, and the Results tab renders it.
