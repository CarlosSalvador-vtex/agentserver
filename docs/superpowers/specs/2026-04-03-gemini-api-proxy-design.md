# Gemini API Proxy Support

## Overview

Add Google Gemini API proxy support to the LLM proxy service, following the existing Anthropic proxy pattern. Uses raw HTTP reverse proxy with usage interception — no SDK dependency.

## Scope

- **Backend**: Gemini API only (`generativelanguage.googleapis.com`), API key auth
- **Operations**: `generateContent` (non-streaming) and `streamGenerateContent` (streaming)
- **Proxy style**: Raw HTTP reverse proxy (same as Anthropic)

## Architecture Context

The agentserver has three LLM routing modes, in priority order:

1. **Modelserver** — workspace has an OAuth connection to a modelserver; sandbox routes through LLM proxy, which forwards to the modelserver URL with a Bearer token
2. **BYOK** — workspace has custom LLM provider config; sandbox connects **directly** to the provider (bypasses LLM proxy entirely)
3. **Platform default** — sandbox routes through LLM proxy, which forwards to the platform's API (Anthropic/Gemini) with the real API key

This design adds Gemini support for modes **1 (modelserver)** and **3 (platform default)**. BYOK (mode 2) requires no LLM proxy changes since it bypasses the proxy.

## Routing

Path-based auto-detection within the existing server:

| Path pattern | Provider | Handler |
|---|---|---|
| `/v1/*` | Anthropic | `handleAnthropicProxy()` (existing) |
| `/v1beta/*` | Gemini | `handleGeminiProxy()` (new) |

Both live under the same service — no separate port or prefix needed. The Anthropic and Gemini APIs have non-overlapping path structures, so auto-detection is unambiguous.

## New Files

| File | Purpose |
|---|---|
| `internal/llmproxy/gemini.go` | `handleGeminiProxy()` — request validation, reverse proxy setup, usage interception |
| `internal/llmproxy/gemini_parser.go` | Gemini response parsing: non-streaming JSON and SSE event extraction |
| `internal/llmproxy/gemini_stream.go` | `geminiStreamInterceptor` — SSE passthrough with usage tracking |

## Modified Files

| File | Change |
|---|---|
| `internal/llmproxy/server.go` | Add `/v1beta/*` route to `handleGeminiProxy` |
| `internal/llmproxy/config.go` | Add `GeminiBaseURL` and `GeminiAPIKey` fields |
| `cmd/llmproxy/main.go` | Relax startup validation (require at least one provider, not just Anthropic) |
| `internal/llmproxy/trace.go` | Add `GenerateGeminiTraceID()` and `GenerateGeminiRequestID()` with `gt-`/`gr-` prefixes |

## Configuration

New environment variables:

| Variable | Default | Description |
|---|---|---|
| `GEMINI_BASE_URL` | `https://generativelanguage.googleapis.com` | Upstream Gemini API URL |
| `GEMINI_API_KEY` | (none) | Real Google API key for Gemini |

Startup validation: at least one of `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` or `GEMINI_API_KEY` must be set.

## Request Flow (gemini.go)

Mirrors the Anthropic handler:

1. **Auth**: Extract `x-api-key` header (proxy token), validate via agentserver `/internal/validate-proxy-token`
2. **Sandbox check**: Must be "running" or "creating"
3. **Upstream target resolution**:
   - If `sbx.ModelserverUpstreamURL` is set → use modelserver (same as Anthropic)
   - Otherwise → use `GeminiBaseURL`
   - If neither modelserver nor `GeminiAPIKey` is configured → return 503 "gemini not configured"
4. **RPD quota check**: For `:generateContent` and `:streamGenerateContent` endpoints. Skipped for modelserver (same as Anthropic).
5. **Stream detection**: URL path contains `:streamGenerateContent`
6. **Read body**: Up to 10MB, for trace extraction
7. **Trace ID**: Same `ExtractTraceID()` logic, but generate new trace IDs with `gt-` prefix and request IDs with `gr-` prefix
8. **Trace persistence**: Upsert trace for generate endpoints
9. **Reverse proxy**:
   - Director: set upstream host, inject auth credentials, strip proxy `x-api-key`
   - ModifyResponse: intercept for usage tracking
   - FlushInterval: -1 for SSE streaming
10. **Usage recording**: Parse response, store with `provider = "gemini"`

### Auth Injection (Director function)

**Platform default** (no modelserver):
- Remove the proxy `x-api-key` header
- Set `x-goog-api-key: {GeminiAPIKey}` (Gemini's native API key header)

**Modelserver forwarding**:
- Remove the proxy `x-api-key` header
- Pre-fetch modelserver token via `fetchModelserverToken()` (same cached flow as Anthropic)
- Set `Authorization: Bearer {modelserverToken}`

This mirrors the Anthropic handler's dual-path auth exactly.

## Response Parsing

### Non-Streaming (gemini_parser.go)

Gemini `generateContent` returns:

```json
{
  "candidates": [{"content": {...}, "finishReason": "STOP"}],
  "usageMetadata": {
    "promptTokenCount": 100,
    "candidatesTokenCount": 50,
    "cachedContentTokenCount": 0,
    "totalTokenCount": 150,
    "thoughtsTokenCount": 0
  },
  "modelVersion": "gemini-2.5-flash"
}
```

Extraction:
- `model` ← `modelVersion`
- `inputTokens` ← `promptTokenCount`
- `outputTokens` ← `candidatesTokenCount`
- `cacheReadInputTokens` ← `cachedContentTokenCount`
- `cacheCreationInputTokens` ← 0 (not applicable for Gemini)
- `messageID` ← empty (Gemini doesn't return a message ID)

### Streaming (gemini_stream.go)

Gemini `streamGenerateContent?alt=sse` returns SSE:

```
data: {"candidates":[...],"usageMetadata":{...},"modelVersion":"gemini-2.5-flash"}

data: {"candidates":[...],"usageMetadata":{...}}

...
```

Each SSE chunk is a complete `GenerateContentResponse`. Key differences from Anthropic SSE:
- No `event:` type lines — just `data:` lines
- Each chunk may include `usageMetadata` — use the **last** chunk's values as final
- TTFT: first chunk with non-empty `candidates[0].content.parts`

The `geminiStreamInterceptor` wraps `io.ReadCloser`, passes through all bytes, parses SSE `data:` lines, and tracks:
- Model version (from any chunk's `modelVersion`)
- Usage metadata (overwritten on each chunk, so last one wins)
- TTFT (time to first content part)

## Usage Storage Mapping

The existing `usage` table already has a `provider` column. Mapping:

| usage column | Gemini source |
|---|---|
| `provider` | `"gemini"` |
| `model` | `modelVersion` from response |
| `message_id` | `""` (Gemini has no message ID) |
| `input_tokens` | `promptTokenCount` |
| `output_tokens` | `candidatesTokenCount` |
| `cache_read_input_tokens` | `cachedContentTokenCount` |
| `cache_creation_input_tokens` | 0 |
| `streaming` | true/false based on endpoint |
| `duration` | wall clock ms |
| `ttft` | time to first content chunk (ms) |

## RPD Quota

Same mechanism as Anthropic — counts all requests in the `usage` table for the workspace today (UTC boundary). Gemini requests count toward the same pool as Anthropic requests. RPD check is skipped for modelserver-routed requests (consistent with Anthropic behavior).

## Error Handling

- No `GEMINI_API_KEY` and no modelserver: requests to `/v1beta/*` return 503 "gemini not configured"
- Upstream errors: pass through as-is (same as Anthropic)
- Parse failures in usage interception: log warning, don't block the response

## Trace ID Prefixes

- Gemini trace IDs: `"gt-" + UUID` (gemini trace)
- Gemini request IDs: `"gr-" + UUID` (gemini request)

This distinguishes Gemini traces from Anthropic ones (`at-`/`ar-`) in the database.

## Out of Scope

These items are NOT part of this change:

- **Sandbox-side Gemini configuration**: The sandbox types (opencode, openclaw, nanoclaw) currently only configure Anthropic provider env vars (`ANTHROPIC_BASE_URL`, `ANTHROPIC_API_KEY`, `provider.anthropic.options`). Adding Gemini-aware sandbox config (e.g. injecting `GEMINI_API_KEY` env var or a `provider.google` block) is a separate change that depends on how the sandbox agents (opencode, openclaw, nanoclaw) consume Gemini.
- **BYOK for Gemini**: The BYOK workspace config (`workspace_llm_config` table) currently stores a single `base_url`/`api_key` pair without a provider type field. Supporting Gemini BYOK would require extending this schema. Out of scope.
- **Gemini-specific rate limiting**: Using separate RPD pools per provider. Currently all providers share one pool per workspace.
