# Cobrança wedge — operator setup guide

Step-by-step guide for platform operators provisioning the **cobrança** pilot workspace (sister / debt-collection wedge). This is **not** the end-user product guide — see [cobranca-wedge eng spec](../specs/cobrana-wedge-eng-spec.md) for deploy, playground, and fork flows.

**Related docs**

| Doc | Purpose |
|-----|---------|
| [Runbook](runbook.md) | Dev cluster deploy, seed job, smoke tests |
| [Eng spec](../specs/cobrana-wedge-eng-spec.md) | Product scope, manual checklist, acceptance |
| [Product design](../product-design-cobrana-wedge.md) | Wedge context and personas |
| [WhatsApp integration](../whatsapp-cloud-integration.md) | Meta webhook and BSP details |
| [IM channels API](../api/reference/im-channels.md) | IM API reference |

---

## Decisions Locked

Operator and product decisions for the cobrança wedge pilot. Engineering detail: [cobranca wedge eng spec](../specs/cobrana-wedge-eng-spec.md).

| ID | Question | Decision |
|----|----------|----------|
| D1 | Deploy idempotency (sister re-deploy) | **B** — delete existing sandboxes before production deploy (avoids stale OpenClaw config) |
| D2 | Fork authentication | **A** — sister forks herself when logged in; no shared fork identity |
| D3 | Playground editor for sisters | **A** — `isDevMode=false` hides dev-only controls (PR [#81](https://github.com/CarlosSalvador-vtex/agentserver/pull/81), [#82](https://github.com/CarlosSalvador-vtex/agentserver/pull/82)) |
| D4 | Fork display name | **A** — strip `-fork` suffix in UI after fork |
| D5 | Automated test coverage | **A** — vitest deploy orchestration + component tests where added |
| D6 | Quota errors in UI | **B** — operator checklist for `maxSandboxes`; API returns `quota_exceeded` with PT-BR message |
| D7 | Admin provisioning steps | Documented in this guide (workspace, quota, invite, WhatsApp configure, sandbox bind) — not in application code |

---

## Prerequisites

| Requirement | Notes |
|-------------|--------|
| **Platform admin account** | User with `role = admin` in `users` (grants `/admin` in the web UI). |
| **agentserver API access** | Session cookie or API key; base URL is your cluster ingress (e.g. `https://agent.example.com`). |
| **WhatsApp Cloud API** | Meta Business account, **Phone number ID**, long-lived **System User access token** with `whatsapp_business_messaging` (and related) permissions. |
| **imbridge reachable from Meta** | Inbound webhooks must hit the public imbridge URL (see [Step 4](#step-4-whatsapp-bsp-configuration)). |
| **Database access** (optional) | Only needed to run [seed templates](../../scripts/seed-cobrana-templates.py) or inspect workspace IDs; not required for API-only setup. |

**Defaults (if you skip quota override)**

| Setting | Default |
|---------|---------|
| Max sandboxes per workspace | **20** (`quota_max_sandboxes_per_workspace`) |
| Max workspaces per user | **10** |

For cobrança pilots, set workspace `max_sandboxes` to at least **1** (Step 2).

---

## Step 1 — Create the workspace

The **Admin panel** (`/admin`) lists users, workspaces, sandboxes, and quotas — it does **not** create workspaces.

Create the pilot workspace from the main app while logged in as an admin (or any user with quota headroom):

1. Open the agentserver web UI (root `/`).
2. Use the workspace switcher → **Create workspace**.
3. Choose a **name** (e.g. `Cobrança Piloto`) and **slug** (e.g. `cobranca-piloto`). The slug is used in URLs and invite links.

**API equivalent** (same as the UI):

```bash
curl -sS -X POST "$BASE_URL/api/workspaces" \
  -H "Content-Type: application/json" \
  -H "Cookie: agentserver-token=$SESSION_TOKEN" \
  -d '{"name":"Cobrança Piloto","slug":"cobranca-piloto"}'
```

Response includes `id` (workspace UUID) and `slug`. Save **`WORKSPACE_ID`** and **`WORKSPACE_SLUG`** for later steps.

**Verification:** `GET /api/workspaces` lists the new workspace, or Admin → **Workspaces** tab shows it.

---

## Step 2 — Set workspace sandbox quota (`maxSandboxes ≥ 1`)

Each workspace is limited by **max sandboxes** (default **20**). Pilots need at least one sandbox for deploy and WhatsApp binding.

### Option A — Admin panel (recommended)

1. Go to **`/admin/workspaces`**.
2. Open the workspace row → **Quota** (workspace quota modal).
3. Set **Max sandboxes** to `1` or higher → **Save**.

### Option B — API

```bash
curl -sS -X PUT "$BASE_URL/api/admin/workspaces/$WORKSPACE_ID/quota" \
  -H "Content-Type: application/json" \
  -H "Cookie: agentserver-token=$ADMIN_SESSION_TOKEN" \
  -d '{"max_sandboxes":5}'
```

Use `0` only if your platform policy treats that as unlimited (same as user quota UI). For a minimal pilot, **`1`** is enough.

**Verification:** Create a sandbox in the workspace UI without `quota exceeded`, or:

```bash
curl -sS "$BASE_URL/api/admin/workspaces/$WORKSPACE_ID/quota" \
  -H "Cookie: agentserver-token=$ADMIN_SESSION_TOKEN"
```

Expect `max_sandboxes` ≥ 1 in the JSON.

---

## Step 3 — Invite the workspace owner (email)

Invites require **owner** or **maintainer** on the workspace. After Step 1, the creating admin is usually already **owner** of the new workspace.

### UI

1. Open the workspace → **Settings** → **Members**.
2. **Invite by email** → enter email, role **`owner`** (recommended for the sister account).
3. Copy the **`invite_url`** from the modal (shown once). The API also emails the link if outbound mail is configured.

### API

```bash
curl -sS -X POST "$BASE_URL/api/workspaces/$WORKSPACE_ID/invites" \
  -H "Content-Type: application/json" \
  -H "Cookie: agentserver-token=$SESSION_TOKEN" \
  -d '{"email":"sister@example.com","role":"owner"}'
```

| Field | Value |
|-------|--------|
| `email` | Invitee address (required) |
| `role` | `owner`, `maintainer`, `developer`, or `guest` (default `developer` if omitted) |

**Response (201):** includes `invite_url` — store it securely; the plaintext token is not retrievable later.

**Verification:** Invitee opens `invite_url`, signs up or logs in, accepts → appears in Members. Pending invites: `GET /api/workspaces/$WORKSPACE_ID/invites`.

---

## Step 4 — WhatsApp BSP configuration

Configure Meta **WhatsApp Cloud API** credentials for the workspace. This creates or updates the workspace WhatsApp channel and returns webhook details for Meta App setup.

### UI

1. Workspace → **Settings** → **Channels**.
2. **WhatsApp** → enter **Phone number ID** and **Access token** → save.
3. Note the displayed **webhook URL** and **verify token** (if shown).

### API

```bash
curl -sS -X POST "$BASE_URL/api/workspaces/$WORKSPACE_ID/im/whatsapp/configure" \
  -H "Content-Type: application/json" \
  -H "Cookie: agentserver-token=$SESSION_TOKEN" \
  -d '{
    "phone_number_id": "YOUR_PHONE_NUMBER_ID",
    "access_token": "YOUR_LONG_LIVED_ACCESS_TOKEN",
    "base_url": "https://graph.facebook.com/v18.0"
  }'
```

| Field | Required | Description |
|-------|----------|-------------|
| `phone_number_id` | Yes | From Meta App → WhatsApp → API setup |
| `access_token` | Yes | System user token with messaging permissions |
| `base_url` | No | Default `https://graph.facebook.com/v18.0` |

**Response:** `channel_id` (UUID) — save as **`CHANNEL_ID`**. Optional fields: `webhook_url`, `verify_token` for Meta dashboard configuration.

### Meta webhook (imbridge)

Point the Meta app **Callback URL** at your **imbridge** ingress (HTTPS required):

| Mode | Callback URL pattern |
|------|-------------------|
| Global verify token | `https://<imbridge-host>/webhook/whatsapp` |
| Per-workspace (returned by configure) | `https://<imbridge-host>/webhook/whatsapp/<WORKSPACE_ID>` |

Set **Verify token** to the value returned by configure (`verify_token`) or the cluster secret `WHATSAPP_WEBHOOK_VERIFY_TOKEN` for the global route.

Confirm subscription fields include at least **`messages`**.

Inbound traffic: Meta → imbridge → agentserver internal API → bound sandbox (Step 5).

**Verification:**

```bash
curl -sS "$BASE_URL/api/workspaces/$WORKSPACE_ID/im/channels" \
  -H "Cookie: agentserver-token=$SESSION_TOKEN"
```

Expect a WhatsApp channel with `connected: true`.

---

## Step 5 — Bind sandbox to WhatsApp channel

Link the sister’s **sandbox** (agent runtime) to the WhatsApp **channel** so inbound messages reach that agent.

Prerequisites:

- Sandbox exists (sister creates via **Deploy** / playground after quota and templates are ready).
- WhatsApp channel configured (Step 4).

### UI

When only one IM channel exists, the product may offer bind in channel settings. Otherwise use the API below.

### API

```bash
# Replace SANDBOX_ID from GET /api/workspaces/$WORKSPACE_ID/sandboxes or sandbox list UI
curl -sS -X POST "$BASE_URL/api/sandboxes/$SANDBOX_ID/im/bind" \
  -H "Content-Type: application/json" \
  -H "Cookie: agentserver-token=$SESSION_TOKEN" \
  -d "{\"channel_id\":\"$CHANNEL_ID\"}"
```

**Verification:**

```bash
curl -sS "$BASE_URL/api/sandboxes/$SANDBOX_ID/im/bindings" \
  -H "Cookie: agentserver-token=$SESSION_TOKEN"
```

Expect the WhatsApp channel in the binding list. Send a test WhatsApp message → agent receives it (see eng spec smoke checklist).

---

## Seed cobrança marketplace templates (recommended)

Before the sister forks a template, seed the shared **Cobrança** skill/soul and references:

```bash
# From repo root, with DATABASE_URL pointing at the cluster DB
pip install psycopg2-binary   # if needed
DATABASE_URL="postgres://..." python3 scripts/seed-cobrana-templates.py
```

Or use the in-cluster Job pattern in [runbook](runbook.md) (`scripts/seed-cobrana-job.yaml`). The script is idempotent.

Sister then: Marketplace → fork **Agente de Cobrança** (or the seeded skill name) → tune if needed → **Deploy** sandbox.

---

## End-to-end verification checklist

Aligned with [eng spec § Manual setup checklist](../specs/cobrana-wedge-eng-spec.md):

| # | Check |
|---|--------|
| 1 | Workspace exists; slug resolves in UI |
| 2 | `max_sandboxes` ≥ 1 (admin quota) |
| 3 | Owner invite accepted; member visible |
| 4 | Seed script run; template visible in Marketplace |
| 5 | Sister deploys sandbox from template; pod running |
| 6 | WhatsApp configured; Meta webhook verified |
| 7 | Sandbox bound to WhatsApp channel |
| 8 | WhatsApp message → agent responds with LGPD-safe test data |

---

## Troubleshooting

| Symptom | Likely cause |
|---------|----------------|
| `quota exceeded` on create sandbox | Step 2 not applied or value too low |
| `insufficient permissions` on invite | Caller is not owner/maintainer on that workspace |
| `invite already pending` | Revoke pending invite or wait for expiry (7 days) |
| WhatsApp webhook verify fails | Mismatch verify token URL (global vs per-workspace) or wrong `WHATSAPP_WEBHOOK_VERIFY_TOKEN |
| Inbound WA messages, no agent | Step 5 bind missing; sandbox not running; imbridge routing |
| Template missing in Marketplace | Seed script not run or wrong workspace visibility |

For cluster deploy issues, see [runbook](runbook.md).
