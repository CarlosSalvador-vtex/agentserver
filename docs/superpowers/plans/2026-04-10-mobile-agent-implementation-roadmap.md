# Mobile Agent — Unified Implementation Roadmap

**Source Spec:** `docs/superpowers/specs/2026-04-10-mobile-agent-design.md`
**Created:** 2026-04-12
**Status:** Draft — pending review

---

## Phase Definitions (Canonical)

These phase numbers supersede any numbering in the individual plan files.

| Phase | Name | Repository | Depends On | Est. Effort |
|-------|------|-----------|------------|-------------|
| **P0** | Modelserver Device Flow | modelserver | — | 2–3 days |
| **P1** | Agentserver Mobile Client + `mobile` Sandbox Type | agentserver | — | 1–2 days |
| **P2** | Mobile Tunnel & Connectivity | agentserver | P1 | 2–3 days |
| **P3** | Flutter Core Auth & Agent Loop | flutter app | P0, P1, P2 | 2–3 weeks |
| **P4** | Push Notifications | agentserver + flutter | P3 | 1 week |

### Dependency Graph

```
P0 (modelserver) ──────────────────────────┐
                                            │
P1 (agentserver mobile client) ──┐          │
                                 │          │
                                 ▼          │
                     P2 (tunnel & conn) ────┤
                                            │
                                            ▼
                              P3 (Flutter core)
                                            │
                                            ▼
                              P4 (push notifications)
```

**P0 and P1 can run in parallel** — different repos, no code coupling.
**P2 depends on P1** — needs `type=mobile` to exist before testing mobile tunnels.
**P3 depends on all three** — Flutter needs all server endpoints ready.
**P4 is independent** of P0–P2 but depends on P3 (needs the Flutter app to test).

---

## Pre-Flight: Spec Fixes (Before Starting P0/P1)

These items should be fixed in the spec before starting implementation:

- [ ] **Fix scope inconsistency**: Change spec line 308 from `project:inference` to `user:inference` (matches P0 plan and existing Claude Code client)
- [ ] **Add unified phase numbering**: Insert a "Phase Breakdown" section matching this roadmap
- [ ] **Document LLM usage tracking gap**: Add to "Open Questions" — direct modelserver calls bypass agentserver usage tracking
- [ ] **Add security section**: Lost/stolen device revocation flow, proxy_token rotation consideration
- [ ] **Clarify tunnel protocol decision**: Document choice (JSON framing vs yamux reuse) with rationale
- [ ] **Verify consent UI on mobile viewport**: Test `/oauth2/consent`, `/oauth2/login`, `/oauth2/device/verify` at 375px width — blocker if broken

---

## P0: Modelserver Device Flow

**Plan file:** `docs/superpowers/plans/2026-04-10-mobile-agent-p0-modelserver.md`
**Repository:** `/root/coding/modelserver`

### Summary

Add RFC 8628 device authorization grant to modelserver by registering a new public OAuth client and reverse-proxying Hydra's device auth/token endpoints through the modelserver admin HTTP server.

### Tasks (from plan, with corrections)

| Task | Description | Key Fix vs. Original Plan |
|------|-------------|---------------------------|
| 0 | Scope check — confirm Hydra v26.2 supports device flow | No change |
| 1 | Add `Auth.OAuth.Hydra.PublicURL` config field | No change |
| 2 | Device flow reverse proxy handlers | Refactor: use `forward(path)` pattern instead of separate Handle methods |
| 3 | Wire into admin router | No change |
| 4 | Client spec + registration script | Fix: use `user:inference` (not `project:inference`) |
| 5 | Docker-compose auto-registration | No change |
| 6 | E2E integration test | Fix: add `project_id` assertion in Step 3 (not optional) |
| 7 | Verify `/v1/messages` accepts tokens | **Fix: expand placeholder test code** (Step 2 has `// ...follow the fixture pattern...` — must be real code) |
| 8 | Env var docs | No change |

### P0 Corrections Detail

**Task 2 — Refactored proxy pattern:**

```go
// Instead of separate HandleDeviceAuth/HandleToken methods with duplicated Clone logic:
func (h *DeviceFlowProxy) forward(upstreamPath string) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        r2 := r.Clone(r.Context())
        r2.URL.Path = upstreamPath
        r2.Host = h.hydraPublic.Host
        h.proxy.ServeHTTP(w, r2)
    }
}

// Usage in routes.go:
r.Post("/oauth/device/auth", dfp.forward("/oauth2/device/auth"))
r.Post("/oauth/token", dfp.forward("/oauth2/token"))
```

**Task 6 Step 3 — Add mandatory assertion:**

```go
// After obtaining access_token, introspect it and assert project_id exists.
// This MUST be an assertion, not just a log line.
if result.Ext.ProjectID == "" {
    t.Fatal("introspection missing project_id in ext claims — consent handler did not stamp it")
}
```

**Task 7 Step 2 — Expand the placeholder:**

The plan currently has:
```go
// ... follow the fixture pattern already in auth_middleware_test.go ...
```

This must be replaced with a concrete test that:
1. Creates a fake Hydra introspection server returning `active: true, client_id: "agentserver-mobile-client", scope: "user:inference offline_access", ext: {project_id, user_id}`
2. Builds a real AuthMiddleware with that introspector
3. Sends a request with `Authorization: Bearer fake-token`
4. Asserts: status != 401, context carries expected project

Additionally, add a **negative test**: same setup but `scope: "offline_access"` only (no `user:inference`) → assert 403 or equivalent.

### P0 Done Criteria

1. `go test ./...` green
2. `go test -tags=e2e ./internal/admin/ -run TestDeviceFlow_EndToEnd` completes with a valid access token (manual browser approval)
3. `docker compose up` on a clean checkout registers the mobile client automatically
4. `curl -X POST http://localhost:8080/v1/messages -H "Authorization: Bearer <device-flow-token>"` returns inference response (not 401)

---

## P1: Agentserver Mobile Client + `mobile` Sandbox Type

**Plan file:** `docs/superpowers/plans/2026-04-10-mobile-agent-p1-agentserver-mobile-client.md`
**Repository:** `/root/agentserver`

### Summary

Register `agentserver-agent-mobile` Hydra client and accept `type="mobile"` in `handleAgentRegister`.

### Tasks (from plan, with corrections)

| Task | Description | Key Fix vs. Original Plan |
|------|-------------|---------------------------|
| 0 | Sanity check — no CHECK constraint on sandboxes.type | No change (verified: correct) |
| 1 | Introduce `normalizeSandboxType` helper + test | No change |
| 2 | Wire into `handleAgentRegister` | Fix: remove the "If go vet complains" fallback paragraph (it won't trigger) |
| 3 | Verify opencodePassword skip for mobile | No change (verification only) |
| 4 | Register mobile client in Helm Hydra Job | Fix: wrap each client in a shell function with individual logging |
| 5 | Manual E2E verification | Fix: add explicit check for `refresh_token` presence in Step 3 response |
| 6 | Update spec documentation | Path corrected: file exists at `docs/superpowers/specs/2026-04-10-mobile-agent-design.md` |

### P1 Corrections Detail

**Task 4 — Improved Helm Job idempotency:**

```yaml
args:
  - |
    ENDPOINT="http://{{ .Release.Name }}-hydra-admin:{{ .Values.hydra.adminPort }}"

    register_client() {
      local ID="$1"
      shift
      if hydra update oauth2-client "$ID" --endpoint "$ENDPOINT" "$@" 2>/dev/null; then
        echo "updated client: $ID"
      else
        hydra create oauth2-client --endpoint "$ENDPOINT" --id "$ID" "$@"
        echo "created client: $ID"
      fi
    }

    # --- CLI agent (existing) ---
    register_client agentserver-agent-cli \
      --name Agentserver_Agent_CLI \
      --grant-type urn:ietf:params:oauth:grant-type:device_code \
      --grant-type refresh_token \
      --response-type code \
      --scope openid --scope profile --scope agent:register \
      --token-endpoint-auth-method none \
      --audience "https://{{ .Values.platform.domain }}"

    # --- Mobile agent (new) ---
    register_client agentserver-agent-mobile \
      --name Agentserver_Agent_Mobile \
      --grant-type urn:ietf:params:oauth:grant-type:device_code \
      --grant-type refresh_token \
      --response-type code \
      --scope openid --scope profile --scope agent:register --scope offline_access \
      --token-endpoint-auth-method none \
      --audience "https://{{ .Values.platform.domain }}"
```

**Task 5 Step 3 — Assert refresh token:**

```bash
# After obtaining tokens, verify refresh_token is present.
# This confirms offline_access scope was actually granted.
curl -sS -X POST https://<host>/api/oauth2/token \
  -d 'client_id=agentserver-agent-mobile' \
  -d 'device_code=<device_code>' \
  -d 'grant_type=urn:ietf:params:oauth:grant-type:device_code' | jq '.refresh_token'
# Expected: non-null string. If null, offline_access was not granted.
```

### P1 Done Criteria

1. `go test ./internal/server/... -run TestNormalize -v` passes
2. `go vet ./internal/server/...` clean
3. `go build ./...` at repo root succeeds
4. `grep -c 'agentserver-agent-mobile' deploy/helm/agentserver/templates/hydra.yaml` returns 2
5. Manual E2E: device flow → register → sandbox created with `type=mobile`

---

## P2: Mobile Tunnel & Connectivity

**Repository:** `/root/agentserver`
**Depends on:** P1

### Decision Point: Tunnel Protocol

The spec proposes a new JSON-framed tunnel endpoint. The P1 plan assumes reuse of existing yamux tunnel.

**Recommended:** Start with yamux reuse (P1's approach). If the Flutter team finds yamux in Dart prohibitively complex, switch to JSON framing. This decision should be made during P3 prototyping.

### Tasks

- [ ] **Task 0: Verify existing tunnel works for mobile connect/disconnect cycling**
  - Simulate rapid reconnects (mobile backgrounding) against existing `/api/sandboxes/{id}/tunnel`
  - Verify `status` transitions: running → offline → running work correctly
  - Verify no resource leaks in yamux session cleanup

- [ ] **Task 1: Add `llm_proxy_url` to registration response** (IF Approach A is adopted later)
  - Skip if using Approach B (direct modelserver). The spec currently uses Approach B.
  - If needed: add `LLMProxyURL string` to server config, return in registration JSON

- [ ] **Task 2: Configurable idle timeout per sandbox type**
  - Add `idle_timeout_override` per sandbox type in config (or per sandbox on creation)
  - Default for mobile: 30 minutes (vs. existing 5 minutes for CLI)
  - This prevents mobile sandboxes from being auto-paused when the app is briefly backgrounded

- [ ] **Task 3: Structured logging for mobile events**
  - Add `sandbox_type` field to structured log entries for: tunnel connect/disconnect, agent register, heartbeat timeout
  - No new logger infra, just additional context fields

- [ ] **Task 4: (Conditional) JSON-framed tunnel endpoint**
  - Only if yamux proves impractical for Flutter
  - New endpoint: `WS /api/tunnel/mobile/{sandboxId}?token={tunnelToken}`
  - JSON message protocol as described in spec (lines 491-501)
  - Falls back to same auth (GetSandboxByTunnelToken) and same status management

### P2 Done Criteria

1. Existing tunnel unit tests still pass
2. Rapid connect/disconnect test (10 reconnects in 30s) shows no resource leaks
3. Mobile sandboxes don't auto-pause within the configured timeout
4. Log entries for mobile events contain `sandbox_type=mobile`

---

## P3: Flutter Core Auth & Agent Loop

**Repository:** New Flutter repository
**Depends on:** P0, P1, P2

This is the largest phase. It implements the full Flutter mobile app as described in the spec.

### Milestone Breakdown

| Milestone | Description | Duration |
|-----------|-------------|----------|
| P3.1 | Auth & Registration | 3–4 days |
| P3.2 | LLM Client & Basic Chat | 3–4 days |
| P3.3 | Agent Loop & Tools | 4–5 days |
| P3.4 | Tunnel & Multi-Agent | 3–4 days |

#### P3.1: Auth & Registration

- [ ] Flutter project scaffold (pubspec.yaml, directory structure per spec)
- [ ] `DeviceFlowClient` — RFC 8628 implementation in Dart
- [ ] Agentserver device flow UI (open browser, polling, waiting screen)
- [ ] Modelserver device flow UI (same pattern, separate server)
- [ ] Agent registration (`POST /api/agent/register` with `type=mobile`)
- [ ] Token storage via `flutter_secure_storage`
- [ ] Token refresh (proactive, 60s before expiry)
- [ ] Session persistence to SQLite

#### P3.2: LLM Client & Basic Chat

- [ ] Modelserver client (`POST /v1/messages` with streaming SSE)
- [ ] SSE parser for `content_block_delta` events
- [ ] Chat UI: streaming text, message bubbles, input box
- [ ] Conversation persistence to SQLite
- [ ] Context management (token tracking, oldest-first pruning)
- [ ] Error handling: 401 → refresh → retry; 429 → backoff; 500 → retry

#### P3.3: Agent Loop & Tools

- [ ] `ToolRegistry` + `Tool` base class
- [ ] Local tools: Read, Write, Edit, Glob, Grep, WebFetch, WebSearch, AskUser
- [ ] Agent discovery tools: discover_agents, delegate_task, check_task, send_message, read_inbox
- [ ] Agent loop engine: messages → API call → parse tool_use → execute → tool_result → repeat
- [ ] Tool call visualization in chat UI (collapsible cards)
- [ ] Max turns guard (default 50)
- [ ] MCP client (HTTP/SSE transport — no stdio on mobile)

#### P3.4: Tunnel & Multi-Agent

- [ ] WebSocket tunnel client (yamux or JSON-framed, per P2 decision)
- [ ] Connection manager (foreground/background state machine)
- [ ] Agent card registration on connect
- [ ] Inbound request handling via tunnel (other agents calling mobile's tools)
- [ ] Background polling for tasks and mailbox
- [ ] Device capability tools: camera, location, clipboard, notifications

### P3 Done Criteria

1. Manual checklist from spec (lines 815-826) — all items green
2. Unit tests for agent loop, each tool, auth flows
3. Integration test against real agentserver + modelserver
4. Cross-agent delegation test: CLI agent ↔ mobile agent

---

## P4: Push Notifications (Future)

**Repository:** agentserver + Flutter app
**Depends on:** P3

### Tasks

- [ ] Database migration: `agent_push_tokens` table (spec lines 507-517)
- [ ] Push registration endpoint: `POST /api/agent/push/register`
- [ ] FCM dispatcher (`internal/push/fcm.go`)
- [ ] APNs dispatcher (`internal/push/apns.go`)
- [ ] Hook into task creation (send push when target is offline mobile agent)
- [ ] Hook into mailbox send (same)
- [ ] Flutter: FCM/APNs integration, background wake-up
- [ ] Flutter: notification tap → navigate to relevant screen

### P4 Done Criteria

1. Mobile agent receives push within 10s of task delegation (when not connected via tunnel)
2. Tapping notification opens the correct task/conversation
3. Android foreground service works during active background tasks
4. iOS BGProcessingTask + auto-delegation works when loop exceeds 10 min

---

## Execution Order Summary

```
Week 1:        Spec fixes + P0 (start) + P1 (start)
                P0 and P1 run in parallel

Week 1-2:      P0 (complete) + P1 (complete)

Week 2:        P2 (start, depends on P1)

Week 2-3:      P2 (complete) + P3.1 (start, Flutter auth)

Week 3-4:      P3.2 (LLM client + chat)

Week 4-5:      P3.3 (agent loop + tools)

Week 5-6:      P3.4 (tunnel + multi-agent)

Week 7+:       P4 (push notifications)
```

---

## Risk Register

| Risk | Impact | Likelihood | Mitigation |
|------|--------|-----------|------------|
| Modelserver consent flow doesn't stamp `project_id` for device flow tokens | P0 blocked | Low | Task 6 e2e test catches this early |
| Consent UI not mobile-responsive | P3 blocked | Medium | Pre-flight audit in Week 1 |
| Dart yamux library not production-ready | P3 delayed | Medium | JSON-framed tunnel as fallback (P2 Task 4) |
| iOS background execution kills agent mid-task | Poor UX | High | Auto-delegate to remote agent (spec design) |
| Direct modelserver calls lose usage tracking | Operational gap | Medium | Accept for v1; add client-side reporting in v2 |
| Hydra `offline_access` scope not properly granted | Auth broken | Low | P1 Task 5 explicit refresh_token check |
