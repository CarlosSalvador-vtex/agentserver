# OpenAPI Phase 1.b — IM Channels tag Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development.

**Goal:** Annotate 9 IM Channels REST endpoints. All endpoints currently use a single shared `imbridgeProxy` handler that reverse-proxies to a separate `imbridge` service. Per-route swag annotations require per-route wrappers, so introduce thin wrappers in a new file.

**Architecture:** Same swaggo pipeline. New file `internal/server/im_routes.go` holds wrappers like `handleIMChannelList(w, r) { s.imbridgeProxy(w, r) }`. Each wrapper has its own annotation block. Router (`server.go`) is updated to point at the wrappers.

**Prereq:** Stacked on `feat/openapi-phase-1b-sandboxes` (PR #154).

---

## Endpoints (9)

| Method | Path | Wrapper handler (new) | Auth |
|---|---|---|---|
| GET | `/api/workspaces/{id}/im/channels` | `handleIMChannelList` | cookie + member |
| PATCH | `/api/workspaces/{id}/im/channels/{channelId}` | `handleIMChannelPatch` | cookie + member |
| DELETE | `/api/workspaces/{id}/im/channels/{channelId}` | `handleIMChannelDelete` | cookie + member |
| POST | `/api/workspaces/{id}/im/weixin/qr-start` | `handleIMWeixinQRStart` | cookie + member |
| POST | `/api/workspaces/{id}/im/weixin/qr-wait` | `handleIMWeixinQRWait` | cookie + member |
| POST | `/api/workspaces/{id}/im/telegram/configure` | `handleIMTelegramConfigure` | cookie + member |
| POST | `/api/workspaces/{id}/im/matrix/configure` | `handleIMMatrixConfigure` | cookie + member |
| POST | `/api/sandboxes/{id}/im/bind` | `handleIMSandboxBind` | cookie + member |
| DELETE | `/api/sandboxes/{id}/im/bind` | `handleIMSandboxUnbind` | cookie + member |

All routes are registered only when `s.IMBridgeURL != ""`.

---

## File Structure

**Create:** `internal/server/im_routes.go` — 9 wrapper handlers with swag annotation blocks
**Modify:** `internal/server/api_types.go` — append IM Channels DTOs
**Modify:** `internal/server/server.go` — switch router from `s.imbridgeProxy` direct registration to the new wrappers
**Modify:** `docs/api/openapi.{yaml,json}` (regenerated)
**Modify:** `web/src/lib/api.ts` — migrate IM helpers if any exist

---

### Task 1: IM Channels DTOs in api_types.go

**Files:** modify `internal/server/api_types.go`

- [ ] **Step 1: Verify which shapes are actually in use**

Read the imbridge handlers (`internal/imbridgesvc/handlers.go` and friends) to confirm exact request/response shapes. The shapes proxy-through, so getting them wrong means lying in the spec. Search:

```bash
grep -nE "json\\.NewEncoder|json\\.NewDecoder|http\\.Error" /root/agentserver/internal/imbridgesvc/*.go | head -40
```

For each endpoint listed in the plan, identify:
- Request body fields (if any) and types
- Success response body fields and types
- Status code on success

Cross-check by reading what the frontend (`web/src/lib/api.ts`) currently expects:

```bash
grep -nE "im/channels|im/weixin|im/telegram|im/matrix|sandboxes/.*im" /root/agentserver/web/src/lib/api.ts
```

- [ ] **Step 2: Append to api_types.go**

After the existing `// --- Sandboxes ---` block, add:

```go
// --- IM Channels ---

// IMChannelResponse mirrors workspace_im_channels rows surfaced via the
// imbridge service. Fields not all required — bot_token is masked or
// omitted by the server side.
type IMChannelResponse struct {
	ID             string `json:"id" validate:"required"`
	WorkspaceID    string `json:"workspace_id" validate:"required"`
	Provider       string `json:"provider" validate:"required" example:"weixin"`
	BotID          string `json:"bot_id" validate:"required"`
	UserID         string `json:"user_id" validate:"required"`
	RequireMention bool   `json:"require_mention"`
	RoutingMode    string `json:"routing_mode" validate:"required" example:"codex"`
	BoundAt        string `json:"bound_at" validate:"required"`
} // @name IMChannel

// IMChannelListResponse is the {"channels": [...]} envelope.
type IMChannelListResponse struct {
	Channels []IMChannelResponse `json:"channels" validate:"required"`
} // @name IMChannelListResponse

// IMChannelPatchRequest is the body for PATCH /api/workspaces/{id}/im/channels/{channelId}.
// Both fields optional — only the supplied keys are applied.
type IMChannelPatchRequest struct {
	RequireMention *bool   `json:"require_mention" extensions:"x-nullable=true"`
	RoutingMode    *string `json:"routing_mode" extensions:"x-nullable=true" example:"codex"`
} // @name IMChannelPatchRequest

// IMWeixinQRStartResponse is returned by POST .../im/weixin/qr-start.
type IMWeixinQRStartResponse struct {
	QRCodeURL string `json:"qrcode_url" validate:"required"`
	Message   string `json:"message"`
} // @name IMWeixinQRStartResponse

// IMWeixinQRWaitResponse is returned once the QR code is scanned and the
// poller binds the channel.
type IMWeixinQRWaitResponse struct {
	ChannelID string `json:"channel_id" validate:"required"`
	BotID     string `json:"bot_id" validate:"required"`
} // @name IMWeixinQRWaitResponse

// IMTelegramConfigureRequest is the body for POST .../im/telegram/configure.
type IMTelegramConfigureRequest struct {
	BotToken string `json:"bot_token" validate:"required" example:"123456:ABC-DEF..."`
} // @name IMTelegramConfigureRequest

// IMTelegramConfigureResponse is the body returned by the configure endpoint.
type IMTelegramConfigureResponse struct {
	Connected bool   `json:"connected" validate:"required"`
	BotID     string `json:"bot_id" validate:"required"`
} // @name IMTelegramConfigureResponse

// IMMatrixConfigureRequest is the body for POST .../im/matrix/configure.
type IMMatrixConfigureRequest struct {
	HomeserverURL string `json:"homeserver_url" validate:"required" example:"https://matrix.example.com"`
	AccessToken   string `json:"access_token" validate:"required"`
	RecoveryKey   string `json:"recovery_key"` // optional, for E2EE
} // @name IMMatrixConfigureRequest

// IMMatrixConfigureResponse is the body returned by the matrix configure endpoint.
type IMMatrixConfigureResponse struct {
	Connected bool   `json:"connected" validate:"required"`
	BotID     string `json:"bot_id" validate:"required"`
} // @name IMMatrixConfigureResponse

// IMSandboxBindRequest is the body for POST /api/sandboxes/{id}/im/bind.
type IMSandboxBindRequest struct {
	ChannelID string `json:"channel_id" validate:"required"`
} // @name IMSandboxBindRequest
```

ADJUST field sets after Step 1's recon — these are best-effort guesses based on the Explore agent's report.

- [ ] **Step 3: Build + commit**

```bash
cd /root/agentserver
go build ./...
git add internal/server/api_types.go
git commit -m "feat(openapi): IM Channels DTOs in api_types.go"
```

---

### Task 2: Create the wrapper handlers + rewire router

**Files:**
- Create: `internal/server/im_routes.go`
- Modify: `internal/server/server.go` (router section + maybe drop direct `s.imbridgeProxy` registrations)

- [ ] **Step 1: Recon current router registrations**

```bash
grep -n "imbridgeProxy\|/im/" /root/agentserver/internal/server/server.go | head -20
```

Identify the exact lines where `s.imbridgeProxy` is registered as the handler for each IM route. List them.

- [ ] **Step 2: Create `internal/server/im_routes.go`**

```go
package server

import "net/http"

// This file holds thin per-route wrappers around s.imbridgeProxy so each
// wrapper can carry its own swag annotation block. The wrappers are wired
// up in server.go's router section when s.IMBridgeURL != "".
//
// The actual request handling all happens upstream in the imbridge service
// (see internal/imbridgesvc/); these wrappers exist only for OpenAPI
// documentation. Do NOT add per-route logic here — push it upstream.

// handleIMChannelList lists IM channels bound to a workspace.
//
//	@Summary   List IM channels in a workspace
//	@Tags      IM Channels
//	@Produce   json
//	@Param     id  path  string  true  "Workspace id"
//	@Success   200  {object}  IMChannelListResponse
//	@Failure   403  {string}  string  "not a member"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/im/channels [get]
func (s *Server) handleIMChannelList(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMChannelPatch updates channel settings.
//
//	@Summary   Update IM channel settings
//	@Tags      IM Channels
//	@Accept    json
//	@Param     id          path  string                 true  "Workspace id"
//	@Param     channelId   path  string                 true  "Channel id"
//	@Param     body        body  IMChannelPatchRequest  true  "Settings patch"
//	@Success   204
//	@Failure   400  {string}  string  "bad request"
//	@Failure   403  {string}  string  "not a member"
//	@Failure   404  {string}  string  "channel not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/im/channels/{channelId} [patch]
func (s *Server) handleIMChannelPatch(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMChannelDelete removes a channel binding.
//
//	@Summary   Delete an IM channel
//	@Tags      IM Channels
//	@Param     id         path  string  true  "Workspace id"
//	@Param     channelId  path  string  true  "Channel id"
//	@Success   204
//	@Failure   403  {string}  string  "not a member"
//	@Failure   404  {string}  string  "channel not found"
//	@Failure   500  {string}  string  "internal error"
//	@Security  CookieAuth
//	@Router    /api/workspaces/{id}/im/channels/{channelId} [delete]
func (s *Server) handleIMChannelDelete(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMWeixinQRStart starts the WeChat/Weixin QR-code login flow.
//
//	@Summary     Start WeChat QR-code bind
//	@Description Returns a QR code URL the user scans in WeChat. Client should then long-poll qr-wait until a channel is bound.
//	@Tags        IM Channels
//	@Produce     json
//	@Param       id  path  string  true  "Workspace id"
//	@Success     200  {object}  IMWeixinQRStartResponse
//	@Failure     403  {string}  string  "not a member"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/weixin/qr-start [post]
func (s *Server) handleIMWeixinQRStart(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMWeixinQRWait long-polls for QR-code scan completion.
//
//	@Summary     Long-poll WeChat QR-code scan
//	@Description Blocks until the user scans the QR code or the poll expires. On success returns the bound channel id.
//	@Tags        IM Channels
//	@Produce     json
//	@Param       id  path  string  true  "Workspace id"
//	@Success     200  {object}  IMWeixinQRWaitResponse
//	@Failure     400  {string}  string  "poll expired / timeout"
//	@Failure     403  {string}  string  "not a member"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/weixin/qr-wait [post]
func (s *Server) handleIMWeixinQRWait(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMTelegramConfigure validates a Telegram bot token and binds a channel.
//
//	@Summary     Bind a Telegram bot
//	@Tags        IM Channels
//	@Accept      json
//	@Produce     json
//	@Param       id    path  string                      true  "Workspace id"
//	@Param       body  body  IMTelegramConfigureRequest  true  "Bot token"
//	@Success     200   {object}  IMTelegramConfigureResponse
//	@Failure     400   {string}  string  "invalid bot token"
//	@Failure     403   {string}  string  "not a member"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/telegram/configure [post]
func (s *Server) handleIMTelegramConfigure(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMMatrixConfigure validates Matrix credentials and binds a channel.
//
//	@Summary     Bind a Matrix account
//	@Tags        IM Channels
//	@Accept      json
//	@Produce     json
//	@Param       id    path  string                    true  "Workspace id"
//	@Param       body  body  IMMatrixConfigureRequest  true  "Matrix credentials"
//	@Success     200   {object}  IMMatrixConfigureResponse
//	@Failure     400   {string}  string  "invalid credentials"
//	@Failure     403   {string}  string  "not a member"
//	@Failure     500   {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/workspaces/{id}/im/matrix/configure [post]
func (s *Server) handleIMMatrixConfigure(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMSandboxBind binds a sandbox to an IM channel.
//
//	@Summary     Bind a sandbox to an IM channel
//	@Tags        IM Channels
//	@Accept      json
//	@Param       id    path  string                true  "Sandbox id"
//	@Param       body  body  IMSandboxBindRequest  true  "Channel id"
//	@Success     204
//	@Failure     400  {string}  string  "bad request"
//	@Failure     403  {string}  string  "not a member"
//	@Failure     404  {string}  string  "sandbox not found"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/sandboxes/{id}/im/bind [post]
func (s *Server) handleIMSandboxBind(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}

// handleIMSandboxUnbind removes a sandbox's IM channel binding.
//
//	@Summary     Unbind a sandbox from its IM channel
//	@Tags        IM Channels
//	@Param       id  path  string  true  "Sandbox id"
//	@Success     204
//	@Failure     403  {string}  string  "not a member"
//	@Failure     404  {string}  string  "sandbox not found"
//	@Failure     500  {string}  string  "internal error"
//	@Security    CookieAuth
//	@Router      /api/sandboxes/{id}/im/bind [delete]
func (s *Server) handleIMSandboxUnbind(w http.ResponseWriter, r *http.Request) {
	s.imbridgeProxy(w, r)
}
```

- [ ] **Step 3: Update router in `internal/server/server.go`**

Find the block (in the IM section guarded by `if s.IMBridgeURL != ""`) where each route is currently `r.<METHOD>(<path>, s.imbridgeProxy)`. Change each to use the new wrapper:

```go
r.Get("/api/workspaces/{id}/im/channels", s.handleIMChannelList)
r.Patch("/api/workspaces/{id}/im/channels/{channelId}", s.handleIMChannelPatch)
r.Delete("/api/workspaces/{id}/im/channels/{channelId}", s.handleIMChannelDelete)
r.Post("/api/workspaces/{id}/im/weixin/qr-start", s.handleIMWeixinQRStart)
r.Post("/api/workspaces/{id}/im/weixin/qr-wait", s.handleIMWeixinQRWait)
r.Post("/api/workspaces/{id}/im/telegram/configure", s.handleIMTelegramConfigure)
r.Post("/api/workspaces/{id}/im/matrix/configure", s.handleIMMatrixConfigure)
r.Post("/api/sandboxes/{id}/im/bind", s.handleIMSandboxBind)
r.Delete("/api/sandboxes/{id}/im/bind", s.handleIMSandboxUnbind)
```

Adjust method names (`r.Get`/`r.Patch`/etc.) to match chi's idioms in the file. Verify routes by reading the existing block first.

If any IM route uses a wildcard/regex (`r.HandleFunc` etc.), keep the wildcard pattern but point at the new wrapper.

- [ ] **Step 4: Build + tests + regenerate spec**

```bash
cd /root/agentserver
go build ./...
go test ./internal/server/ -count=1 -timeout 120s
make openapi
make openapi-check
```

Verify the new schemas + paths landed:

```bash
grep -E "^  /api/(workspaces/\{id\}/im|sandboxes/\{id\}/im)" docs/api/openapi.yaml | sort -u
grep -E "^    (IMChannel|IMChannelList|IMChannelPatchRequest|IMWeixinQRStartResponse|IMWeixinQRWaitResponse|IMTelegramConfigure|IMMatrixConfigure|IMSandboxBindRequest):" docs/api/openapi.yaml | sort
grep "server\\." docs/api/openapi.yaml | grep -v "^.*Public REST API for agentserver"  # should be empty
```

- [ ] **Step 5: Commit**

```bash
git add internal/server/im_routes.go internal/server/server.go docs/api/openapi.yaml docs/api/openapi.json
git commit -m "feat(openapi): annotate IM Channels handlers (9 endpoints via wrappers)"
```

---

### Task 3: Migrate IM helpers in `web/src/lib/api.ts`

**Files:** modify `web/src/lib/api.ts`

- [ ] **Step 1: Regenerate types + find helpers**

```bash
cd /root/agentserver/web
pnpm openapi:gen
grep -nE "export async function .*(IMChannel|Weixin|Telegram|Matrix|bind.*[Ss]andbox|unbind.*[Ss]andbox|listIM|im/channels|im/weixin)" /root/agentserver/web/src/lib/api.ts
```

Some helpers may have non-obvious names — also check:

```bash
grep -nE "/api/workspaces/.*/(im|members)|api/sandboxes/.*/im" /root/agentserver/web/src/lib/api.ts
```

- [ ] **Step 2: Migrate present helpers**

For each helper, rewrite the body to use `apiFetch` + the corresponding generated type. Keep signatures stable.

Add type aliases:

```typescript
export type IMChannel = components['schemas']['IMChannel']
export type IMChannelListResponse = components['schemas']['IMChannelListResponse']
export type IMChannelPatchRequest = components['schemas']['IMChannelPatchRequest']
// ... etc
```

Drop duplicate local types.

- [ ] **Step 3: Verify**

```bash
cd /root/agentserver/web
pnpm tsc --noEmit
pnpm lint
pnpm build
```

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver
git checkout -- web/dist/ 2>/dev/null || true
git add web/src/lib/api.ts
git commit -m "refactor(web): migrate IM Channels helpers to apiClient + generated types"
```

---

### Task 4: Final verify + PR

- [ ] **Step 1: Gauntlet**

```bash
make openapi-check
go test ./internal/server/ -count=1 -timeout 120s
cd web && pnpm openapi:gen && pnpm build && cd ..
git checkout -- web/dist/ 2>/dev/null || true
git status --short
```

- [ ] **Step 2: Push + open PR (stacked on #154)**

```bash
git push -u github feat/openapi-phase-1b-im-channels
gh pr create --base main --title "feat(openapi): Phase 1.b — IM Channels tag (9 endpoints)" --body "..."
```

PR body should explain:
- Wrapper-pattern (per-route wrappers around shared `imbridgeProxy`) was introduced specifically for swag annotation; wrappers carry no logic
- Stacked on PR #154

---

## Done when

- 9 endpoints under tag `IM Channels` in `docs/api/openapi.yaml`
- All 9 wrappers in `internal/server/im_routes.go` annotated
- Router updated to use wrappers
- Frontend builds; existing IM UI works
- PR open against main
