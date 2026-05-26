# Release — Multi-Channel Routing + WhatsApp Cloud (2026-05-26)

Single-session delivery of the multi-tenant B2B routing foundation in the agentserver fork (`CarlosSalvador-vtex/agentserver`). 12 PRs (9 features, 3 fixes) plus a deploy-state catch-up commit, all merged to `main` and rolled to dev EKS revision 28 (`routing-v4`).

**Headline goal:** unblock N:M sandbox ↔ IM-channel binding, surface it in the UI, and ship the first push-based provider (WhatsApp Cloud) end-to-end through the same dispatch pipeline.

---

## PRs (in merge order)

| # | Type | Title | Notes |
|---|------|-------|-------|
| [#3](https://github.com/CarlosSalvador-vtex/agentserver/pull/3) | feat | N:M sandbox↔channel bindings + workspace routing strategy | Schema fundament |
| [#4](https://github.com/CarlosSalvador-vtex/agentserver/pull/4) | feat | channel auto-bind + `provisionSandbox` extraction | Backend |
| [#5](https://github.com/CarlosSalvador-vtex/agentserver/pull/5) | feat | routing strategy dropdown + auto-bind action | Frontend |
| [#6](https://github.com/CarlosSalvador-vtex/agentserver/pull/6) | fix | migration 031 `updated_at` → `created_at` | Hot-fix from dev rollout |
| [#7](https://github.com/CarlosSalvador-vtex/agentserver/pull/7) | feat | WhatsApp Cloud (Meta) provider — webhook-driven | New provider |
| [#8](https://github.com/CarlosSalvador-vtex/agentserver/pull/8) | fix | webhook nil-proxy + dev-eks imbridge image override | Hot-fix from dev rollout |
| [#9](https://github.com/CarlosSalvador-vtex/agentserver/pull/9) | feat | WhatsApp Cloud configure modal | Frontend |
| [#10](https://github.com/CarlosSalvador-vtex/agentserver/pull/10) | feat | verify `X-Hub-Signature-256` against Meta app secret | Security |
| [#11](https://github.com/CarlosSalvador-vtex/agentserver/pull/11) | fix | hermes / jupyter / nano sandbox labels | UI |
| [#12](https://github.com/CarlosSalvador-vtex/agentserver/pull/12) | fix | scan nullable `nanoclaw_bridge_secret` without panicking | Surfaced by smoke |
| `fe1e544` | chore | bump dev-eks tags to `routing-v4` | Deploy catch-up |

---

## What you get

### 1. N:M sandbox ↔ channel routing (PR #3)

**Schema (migration 031):**

```sql
ALTER TABLE workspaces
  ADD COLUMN channel_routing_strategy TEXT NOT NULL DEFAULT 'shared';
-- Valid values: 'shared' | 'per_agent' | 'hybrid'

CREATE TABLE sandbox_channel_bindings (
    sandbox_id TEXT NOT NULL REFERENCES sandboxes(id)             ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES workspace_im_channels(id) ON DELETE CASCADE,
    bound_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sandbox_id, channel_id)
);
-- Backfilled from sandboxes.im_channel_id; legacy FK kept for compat.
```

**Strategies:**

| Strategy | Behaviour |
|---|---|
| `shared` (default) | All channels in workspace converge on one sandbox |
| `per_agent` | One sandbox per channel |
| `hybrid` | Manual binding only (auto-bind refuses) |

**Code:** new helpers in `internal/db/sandbox_channel_bindings.go`; junction-first reads with FK fallback in `internal/db/im_channels.go`; dual-write on every Bind/Unbind. Workspace struct gains `ChannelRoutingStrategy`.

### 2. Auto-provision + bind (PR #4)

```
POST /api/workspaces/{id}/im/channels/{channelId}/auto-bind
```

Reads the workspace strategy, then either reuses an existing shared sandbox (via `GetSharedSandbox`) or provisions a fresh one via the refactored `provisionSandbox(ctx, wsID, in)` helper. Returns:

```json
{ "sandbox_id": "...", "channel_id": "...", "strategy": "shared", "reused": true|false }
```

`handleCreateSandbox` is rebuilt on top of the same helper — typed `*provisionError` values map cleanly to HTTP status codes via `writeProvisionError`.

### 3. Frontend (PRs #5, #9, #11)

- **Settings tab** → Channel Routing panel with strategy dropdown + per-option description.
- **IM Channels** → Auto-bind action per channel row (Plus icon next to Trash). Disabled in hybrid mode; reports `reused` vs `provisioned` inline.
- **WhatsApp Cloud modal** mirrors Telegram: collects `phone_number_id` + access token, then shows webhook URL with one-click copy.
- **Sandbox badges** now render correctly for hermes (amber), jupyter (pink), nanoclaw / nano (fuchsia), claudecode (orange), openclaw / claw (purple), opencode / code (blue) — fixes the bug where every non-opencode/openclaw/claudecode type fell through to "code".

### 4. WhatsApp Cloud provider (PRs #7, #8, #10)

A 4th IM provider following the existing `Provider` interface, but webhook-driven instead of poll-driven.

| Layer | What |
|---|---|
| `internal/imbridge/whatsapp_provider.go` | `Provider` + `ConfigurableProvider`. `Poll()` no-op with 5-min backoff. `Send()` → Graph API `POST /{phone_number_id}/messages`. |
| `internal/imbridge/bridge.go` | New exported `Bridge.DispatchInbound(ctx, channelID, msg)` so push providers feed messages into the same `forwardMessage` pipeline as polling ones. `BridgeDB` interface gains `DispatchInboundChannel`. |
| `internal/db/im_channels.go` | `FindIMChannelByProviderBot(provider, botID)` for webhook routing by `phone_number_id`; `DispatchInboundChannel` for credential reconstruction. |
| `internal/imbridgesvc/handlers.go` | `handleWorkspaceWhatsAppConfigure` (authenticated); `handleWhatsAppWebhookVerify` (hub.challenge); `handleWhatsAppWebhookInbound` (parses Meta payload, dispatches each text message). |
| `internal/server/server.go` + `im_routes.go` | Reverse-proxies the new routes through `imBridgeProxy` so split deployments work. **PR #8** caught a nil-proxy panic when the webhook routes were registered before `imBridgeProxy` was constructed. |
| `cmd/imbridge/main.go` | `&WhatsAppProvider{}` registered. |
| Helm | `whatsapp.webhookVerifyToken` + `whatsapp.appSecret` → env on the imbridge pod. |

**Security (PR #10):** every webhook delivery now passes through HMAC verification — `verifyWhatsAppSignature` decodes `X-Hub-Signature-256: sha256=<hex>`, recomputes HMAC-SHA256 of the raw body with the app secret, and constant-time compares (`hmac.Equal`). Returns 401 on mismatch so monitoring can surface spoofing attempts.

### 5. Data robustness (PR #12)

`sandboxes.nanoclaw_bridge_secret` is nullable (only populated for nanoclaw sandboxes). The junction-first read query was scanning that column into a plain `string`, which fails with `converting NULL to string is unsupported` for any non-nanoclaw sandbox bound to a channel. Both the new and legacy paths now scan into `sql.NullString` and copy out only when `.Valid`. The misleading "no running sandbox bound to channel" error in `forwardToNanoClaw` is replaced with `%w`-wrapped diagnostics.

---

## New API surface

### Routing strategy
```
GET  /api/workspaces/{id}/routing-strategy            → {"strategy": "shared"}
PUT  /api/workspaces/{id}/routing-strategy            body {"strategy": "..."}
```

### Channel binding (N:M aware)
```
POST /api/sandboxes/{id}/im/bind-multi                body {"channel_ids": [...]}
POST /api/workspaces/{id}/im/channels/{cid}/auto-bind body {"sandbox_type": "openclaw"}
```

### WhatsApp Cloud
```
POST /api/workspaces/{id}/im/whatsapp/configure       body {"phone_number_id": "...", "access_token": "...", "base_url": "..."}
GET  /webhook/whatsapp                                Meta hub.challenge handshake
POST /webhook/whatsapp                                Meta inbound delivery
```

---

## Configuration

### values.yaml additions
```yaml
whatsapp:
  webhookVerifyToken: ""   # required for production; matches Meta Webhook → Verify Token
  appSecret: ""            # required for production; enables X-Hub-Signature-256 check

# Already in values; mention here for completeness:
imbridge:
  image:
    repository: 344729309528.dkr.ecr.us-east-1.amazonaws.com/agentserver-imbridge
    tag: routing-v4
```

### Env vars introduced
- `WHATSAPP_WEBHOOK_VERIFY_TOKEN` (imbridge pod) — empty disables the GET handshake check (dev only).
- `WHATSAPP_APP_SECRET` (imbridge pod) — empty disables HMAC body signature check (dev only).

---

## Smoke verified on dev EKS

```bash
# GET handshake
curl -i "https://agentserver.analytics.vtex.com/webhook/whatsapp?hub.mode=subscribe&hub.verify_token=$TOKEN&hub.challenge=42"
# → 200 + body "42"

# Strategy + auto-bind + bind-multi (via cookie auth in browser)
PUT  /api/workspaces/W/routing-strategy            {"strategy":"per_agent"} → 200
POST /api/workspaces/W/im/whatsapp/configure       {"phone_number_id":"...","access_token":"..."} → 200 channel created
POST /api/sandboxes/S/im/bind-multi                {"channel_ids":["C"]} → 200
POST /webhook/whatsapp                             <Meta payload> → 200 + dispatch logged on imbridge
```

**Outcome:** webhook → `Bridge.DispatchInbound` → channel lookup → `forwardToNanoClaw` → HTTP POST to pod IP `:3002`. The forward step succeeds for nanoclaw sandboxes; for openclaw/hermes it returns `connection refused` because those images don't yet listen on `:3002` — a sandbox-image-layer gap, not a routing-platform gap.

---

## Docs added

- [`docs/multi-channel-routing.md`](docs/multi-channel-routing.md) — schema, dual-write, fallback strategy, auto-bind flow, decision matrix, smoke recipe, roadmap.
- [`docs/whatsapp-cloud-integration.md`](docs/whatsapp-cloud-integration.md) — Meta credential mapping, outbound `Send()`, inbound webhook + HMAC verification, multi-tenant routing, dev EKS smoke, limitations table.

---

## What we deliberately cut (next-PR candidates)

| Topic | Why deferred | Where to add |
|---|---|---|
| Openclaw / Hermes IM inbound endpoint | Image-layer change, not routing-platform | extend each agent image with `POST /message` handler, or wire `Bridge.forwardToOpenclaw` |
| Media messages (image, voice, video, document) for WhatsApp | Each type needs `GET /<media_id>` round-trip + provider-side upload | extend `handleWhatsAppWebhookInbound` switch + `WhatsAppProvider.SendImage` |
| Webhook status events (delivered/read/failed) | Useful for monitoring, no agent action depends | parse `entry[].changes[].value.statuses` into channel meta |
| Zero-race auto-bind inside configure handlers | Optional. UI calls configure → auto-bind sequentially today; race window is small | extend each provider configure handler to call `provisionSandbox` + binding helpers |
| Frontend display of bound sandbox(es) per channel | Requires either new endpoint or extending `ListIMChannels` response | extend `GET /api/workspaces/{id}/im/channels` |
| Drop legacy FK `sandboxes.im_channel_id` | Wait for junction to be in use for N weeks in production | future cleanup migration |
| Per-workspace app secret / verify token | Today both are agentserver-level env vars | move to `workspace_im_channel_meta` rows |

---

## Deploy state

- **Cluster:** `arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform`
- **Helm release:** `agentserver` ns `agentserver`, revision **28**
- **Images:**
  - `agentserver:routing-v4` (sha256:b1b169f5...)
  - `agentserver-imbridge:routing-v4` (sha256:c021879c...)
- **Migration applied:** `031_multi_channel_routing.sql`
- **Postgres schema:** `sandbox_channel_bindings` table + `workspaces.channel_routing_strategy` column present and backfilled.

---

## Tier 1 playground hardening (2026-05-26)

| Area | Change |
|------|--------|
| Constants | `internal/sandbox/types.go` — `SandboxType`, `RefKind`, `ProviderKind` |
| Tests | `composition_integration_test.go`, `playground_provision_integration_test.go` (need `TEST_DATABASE_URL`) |
| Rate limits | Per-user limits on `POST .../dry-run` (~10/min) and `.../test-sandbox` (~3/min) |
| UI | Composition picker in Create Sandbox modal (draft soul + skills) |
| OpenClaw soul | Env `OPENCLAW_SOUL_FILE` / `AGENTSERVER_SOUL_BODY` + prompt preamble on draft skills |

Deploy tag suggestion: `tier1-final` — bump `values-dev-eks.yaml` before `helm upgrade`.

---

## Acknowledgements

Built with [Claude Code](https://claude.com/claude-code) in a single session, end-to-end: research, planning, implementation, deploy, browser-driven smoke tests, and bug surfacing + hot-fixes.
