# 0013. Public demo safety

- Status: Accepted
- Date: 2026-10-01

## Context

Anonymous visitors spend real provider credits and share a single global gateway state.

## Decision

- The playground page never holds an API key. It calls `/demo/*` endpoints, which invoke the gateway service in process using a server-side demo key.
- Requests from the playground use the `fast` alias, with `max_tokens` capped at 512 and the conversation trimmed to the last 10 messages.
- Each IP is limited to 20 messages per hour, enforced before the gateway pipeline and in addition to the demo key's token bucket.
- The global daily budget is US$1. When it is exhausted, playground traffic is answered from cache or by the in-process mock provider, and the panel shows the budget as exhausted. The budget is computed from the ledger and reset at 00:00 UTC.
- The public chaos button can only set `down` for 30s on one provider, with a 60s cooldown per provider. Chaos is global, so all visitors see the outage, and it is restored automatically.
- Live panel events never include message content.

## Consequences

- Worst-case spend is bounded and known.
- One visitor can briefly affect others, which is part of the demonstration and is time-bounded.

## Alternatives considered

- Per-session chaos and breakers: isolates visitors, but breakers are inherently shared in production, so the demo would misrepresent reality.
