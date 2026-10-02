# 0004. OpenAI-compatible API surface, text only

- Status: Accepted
- Date: 2026-10-01

## Context

Existing clients and SDKs should work against the gateway by changing only the base URL and the key. Matching the full OpenAI API would make provider translation the bulk of the project.

## Decision

Support:

- `POST /v1/chat/completions` with text messages, `stream` true or false, `temperature`, `top_p`, `max_tokens`, `stop`, `user`
- `GET /v1/models`, listing aliases and concrete models

Reject tools, function calling, image or audio content parts, `n > 1`, `logprobs` and `response_format` with a `400` in the OpenAI error envelope.

Gateway behaviour is reported through response headers (`X-Gateway-Provider`, `X-Gateway-Model`, `X-Gateway-Cache`, `X-Gateway-Attempts`, `X-Gateway-Similarity`, `X-Gateway-Request-Id`) so that the response body stays OpenAI-identical.

## Consequences

- Official OpenAI SDKs work unchanged for text chat.
- Translation to Anthropic covers system prompts, roles, stop sequences, usage and streaming events only.
- Tool use and multimodal content are explicit future extensions.

## Alternatives considered

- Full parity including tools: high translation cost with no added value for the resilience and caching story.
