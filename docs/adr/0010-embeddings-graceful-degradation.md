# 0010. Embeddings via OpenAI with graceful degradation

- Status: Accepted
- Date: 2026-10-01

## Context

The semantic cache needs embeddings. A local model avoids external dependencies but costs RAM on a small VPS and adds a sidecar. Using OpenAI means the semantic cache depends on the same vendor that may be down.

## Decision

Use OpenAI `text-embedding-3-small` (1536 dimensions) through a dedicated `Embedder` client, separate from the chat provider client and not subject to chaos rules. The embedder has its own circuit breaker, so a sustained outage stops paying the 500ms timeout on every request.

When embedding fails or exceeds its time budget, the semantic layer reports `unavailable`. The request proceeds as a miss, and the live panel shows the semantic cache as degraded.

## Consequences

- Embedding cost is negligible at demo scale.
- A real OpenAI outage degrades the semantic cache while Anthropic keeps serving chat. This is the intended behaviour and can be explained in the demo.
- Switching to a local model later only requires another `Embedder` implementation and a dimension change.

## Alternatives considered

- Local embedding model (for example `bge-small` via ONNX or Ollama): no vendor dependency, but more memory and operational surface.
- Applying chaos to embeddings: muddies the failover demo, which is about chat providers.
