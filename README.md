<h1 align="center">agentserver</h1>

<p align="center">
  <strong>Personally command and deploy AI agents across every location and device — from one place.</strong>
</p>

<p align="center">
  <a href="https://platform.agentserver.dev"><img src="https://img.shields.io/badge/Try%20Now-platform.agentserver.dev-blue?style=for-the-badge" alt="Try Now"></a>
</p>

<p align="center">
  <a href="https://github.com/agentserver/agentserver/actions"><img src="https://github.com/agentserver/agentserver/actions/workflows/build.yml/badge.svg" alt="Build"></a>
  <a href="https://github.com/agentserver/agentserver/blob/main/LICENSE"><img src="https://img.shields.io/github/license/agentserver/agentserver" alt="License"></a>
  <a href="https://github.com/agentserver/agentserver/releases"><img src="https://img.shields.io/github/v/release/agentserver/agentserver" alt="Release"></a>
</p>

---

<p align="center">
  <img src="assets/screenshot-1.png" alt="agentserver Web UI" width="800">
</p>
<p align="center">
  <img src="assets/screenshot-2.png" alt="agentserver Coding Agent" width="800">
</p>

> 📖 Read the full vision: [Overview of agentserver](Overview%20of%20agentserver.pdf) (slide deck, Apr 2026)

agentserver is a self-hosted platform for **operating a fleet of coding agents from one console** — cloud sandboxes, your laptop, your desktop, even your phone, all reachable through the same Web UI or an IM channel (WeChat / Weixin, Telegram, …).

It is the answer to a question Addy Osmani frames as the path from L1 (no AI) to L8 (build your own orchestrator)*: once you are juggling 10+ agents across machines, you stop being a *conductor* and become an *orchestrator*. agentserver is the orchestration layer.

<sub>* Addy Osmani, Director, Google · Gemini & Cloud AI — <a href="https://talks.addy.ie/oreilly-codecon-march-2026">talks.addy.ie/oreilly-codecon-march-2026</a></sub>

### How it differs from what already exists

| Tool | Local agents | Cloud sandboxes | Cross-device peering |
|------|:---:|:---:|:---:|
| OpenClaw / Claude Code Remote | one at a time | — | — |
| Claude Code on the web | — | ✅ | — |
| Claude Code Agent Teams | — | ✅ (subagents) | — |
| **agentserver** | **✅ many** | **✅** | **✅** |

## Why agentserver?

- **One console, every device** — Operate cloud sandboxes, local laptops/desktops, and IM-bound agents from the same workspace.
- **Local tunneling, zero public IP** — A local opencode/Claude Code/Codex instance dials home over WebSocket and appears as a sandbox in the UI.
- **Sandboxes** — Per-task containers with pause/resume and idle auto-pause; Docker (single node) or Kubernetes with [Agent Sandbox](https://github.com/kubernetes-sigs/agent-sandbox) + gVisor.
- **Multi-tenancy by workspace** — Cloud and local agents register into the *same* workspace registry; role-based access (owner / maintainer / developer / guest).
- **Credential & LLM proxy** — Sandboxes never see real provider keys; per-workspace RPD quotas and usage tracking enforced server-side.
- **IM bridge (WIP)** — Drive agents from WeChat / Weixin or Telegram via `imbridge`; no terminal required.
- **SSO ready** — GitHub OAuth and generic OIDC (Keycloak, Authentik, …).
- **Deploy anywhere** — Pre-built binaries, Homebrew, Docker Compose, or Helm for Kubernetes.

## Roadmap: Three Stages

agentserver is being built in three stages. The diagrams and full reasoning live in [Overview of agentserver.pdf](Overview%20of%20agentserver.pdf).

| Stage | Theme | Status | What lands |
|-------|-------|:---:|------------|
| **1** | `code-server` for coding agents | ✅ shipping | Sandbox provisioner, agent registry, credential / LLM proxy, agent-proxy ingress, Web Console |
| **2** | The emergence of OpenClaw | 🚧 in progress | NanoClaw (sandboxed Claude Code), `imbridge` (WeChat / Telegram), agent message bus |
| **3** | Centralized agent-loop | 🔭 designing | Stateless `cc` worker pool, `cc-broker` provisioner, tool router, durable memory / context store, agent mailboxes |

### Core insights driving Stage 3

- **Stateless harness** — Decouple the *brain* (Claude + harness) from the *hands* (sandboxes and tools). Sessions are append-only event logs that live outside the context window. Workers are *cattle, not pets* — a worker that dies mid-turn loses nothing.
- **Hybrid cloud-local mesh** — Cloud and local agents share one workspace registry. Discovery happens through agent cards; the LLM picks a tool and a tool router decides where the call goes. *Agent discovery, not network mesh.*
- **Async collaboration via mailboxes** — Agents hand off work through inboxes in durable storage. The receiver does not need to be alive when the message is sent. The mailbox is the source of truth.

## Architecture

Today's deployment (Stage 1, with Stage 2 services landing):

```
                  World (Anthropic, GitHub, …)
                          ▲
                          │ egress
              ┌───────────┴────────────┐
              │  credentialproxy /     │
              │  llmproxy (:8081)      │
              │  • key injection       │
              │  • RPD quota / usage   │
              └───────────┬────────────┘
                          │
WeChat / Telegram ──▶ imbridge ──▶ ┐
Browser ───────────▶ agentserver  ─┤    ┌──────────────────┐
                     (:8080)       │    │ sandbox pod /    │
                     • REST API    ├───▶│ container        │
                     • admin UI    │    │ └─ opencode /    │
                     • registry    │    │    nanoclaw      │
                     • tunnels     │    └──────────────────┘
                          │        │
                          │        └──▶ local laptop / desktop / phone
                          │              └─ agentserver-agent (WS tunnel)
                          ▼
                     PostgreSQL
                  (users, workspaces,
                   sandboxes, quotas,
                   sessions, mailboxes)

Browser ──▶ sandboxproxy (:8082) ─▶ subdomain routing to sandbox services
Browser ──▶ cc-broker          ───▶ stateless cc worker pool (Stage 3)
Sandbox ──▶ executor-registry  ───▶ tool-call dispatch / executor lookup
```

| Service | Default Port | Role |
|---------|-------------|------|
| **agentserver** | `:8080` | Main API, Web UI, agent registry, tunnel endpoints |
| **llmproxy** | `:8081` | LLM API proxy with per-workspace rate limiting and usage tracking |
| **sandboxproxy** | `:8082` | Subdomain-based routing to sandbox services |
| **credentialproxy** | — | Server-side injection of provider credentials |
| **imbridge** | — | IM channel bridge (WeChat / Weixin, Telegram) |
| **cc-broker** | — | Stateless Claude Code / Codex worker pool (Stage 3) |
| **executor-registry** | — | Tool-call dispatch / executor discovery (Stage 3) |

## Code of Conduct

agentserver follows four house rules that shape every change:

- ❌ **No human-authored code.** All production code is generated by AI agents.
- ✅ **Open source from day 1.** The repository is public from inception; no closed-source phase.
- ✅ **Fully automated DevOps.** Build, test, release, and deployment are end-to-end automated.
- ✅ **Dogfooding & bootstrapping.** agentserver is built (partially) *with* agentserver — every feature is used by our own agents before it ships.

## Quick Start

### Docker Compose (recommended for local use)

```bash
git clone https://github.com/agentserver/agentserver.git && cd agentserver
docker build -f Dockerfile.opencode -t agentserver-agent:latest .
export ANTHROPIC_API_KEY="sk-ant-..."
docker compose up -d
```

Open `http://localhost:8080` in your browser.

### Helm (Kubernetes)

```bash
helm install agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --namespace agentserver --create-namespace \
  --set database.url="postgres://user:pass@postgres:5432/agentserver?sslmode=disable" \
  --set anthropicApiKey="sk-ant-..." \
  --set ingress.enabled=true \
  --set ingress.host="cli.example.com" \
  --set baseDomain="cli.example.com"
```

### Pre-built Binaries

Download from [GitHub Releases](https://github.com/agentserver/agentserver/releases), or install via Homebrew:

```bash
brew install agentserver/tap/agentserver
```

## Local Agent Tunneling

Connect a locally-running opencode instance to agentserver — no public IP or third-party tunnel needed.

1. In the Web UI, click the laptop icon next to "Sandboxes" to generate a registration code.

2. On your local machine:

```bash
# First time — register with the server
agentserver connect \
  --server https://cli.example.com \
  --code <registration-code> \
  --name "My MacBook"

# Subsequent runs — auto-reconnect using saved credentials
agentserver connect
```

3. A **local** sandbox appears in the Web UI — click "Open" to access your local opencode through the browser.

### Multi-agent support

Register multiple agents on the same machine, each targeting a different directory and workspace:

```bash
# List all registered agents
agentserver list

# Remove a registration
agentserver remove --workspace <workspace-id>
```

Agent credentials are stored in `~/.agentserver/registry.json`.

**Tunnel features:** zero-config networking, auto-reconnect with backoff, binary WebSocket protocol (no base64 overhead), real-time SSE streaming, offline detection with auto-recovery.

## Configuration

See the [API reference](docs/api-reference.md) for full endpoint documentation.

<details>
<summary><strong>Helm Values</strong></summary>

| Parameter | Description | Default |
|-----------|-------------|---------|
| `image.repository` | Server image | `ghcr.io/agentserver/agentserver` |
| `image.tag` | Server image tag | `latest` |
| `opencode.image` | Opencode agent image for sandbox pods | `ghcr.io/agentserver/opencode-agent:latest` |
| `opencode.runtimeClassName` | RuntimeClass for sandbox pods (e.g. `gvisor`) | `""` |
| `openclaw.image` | OpenClaw gateway image | `""` |
| `openclaw.port` | OpenClaw gateway port | `18789` |
| `database.url` | PostgreSQL connection string | (required) |
| `anthropicApiKey` | Anthropic API key | (required) |
| `anthropicBaseUrl` | Custom Anthropic API base URL | `""` |
| `anthropicAuthToken` | Anthropic auth token (alternative to API key) | `""` |
| `backend` | Sandbox backend: `docker` or `k8s` | `docker` |
| `baseDomain` | Base domain for subdomain routing | `""` |
| `baseScheme` | URL scheme for generated URLs | `https` |
| `idleTimeout` | Auto-pause idle sandboxes after | `30m` |
| `persistence.sessionStorageSize` | Per-sandbox ephemeral storage | `5Gi` |
| `persistence.userDriveSize` | Per-workspace shared disk size | `10Gi` |
| `persistence.storageClassName` | Storage class for PVCs | `""` (cluster default) |
| `workspace.resources` | Resource limits/requests for sandbox pods | `1Gi/1cpu` limits |
| `agentSandbox.install` | Install Agent Sandbox controller | `true` |
| `ingress.enabled` | Enable Nginx Ingress | `false` |
| `ingress.host` | Ingress hostname | `agentserver.example.com` |
| `ingress.tls` | Enable TLS (cert-manager) | `false` |
| `gateway.enabled` | Enable Gateway API HTTPRoute | `false` |

</details>

<details>
<summary><strong>Environment Variables (Main Server)</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | (required) |
| `ANTHROPIC_API_KEY` | Anthropic API key | (required) |
| `ANTHROPIC_BASE_URL` | Custom API base URL | `https://api.anthropic.com` |
| `ANTHROPIC_AUTH_TOKEN` | Anthropic auth token (alternative to API key) | - |
| `OPENCODE_CONFIG_CONTENT` | JSON opencode config for sandbox pods | - |
| `BASE_DOMAIN` | Base domain for subdomain routing | - |
| `BASE_SCHEME` | URL scheme (`http` or `https`) | `https` |
| `IDLE_TIMEOUT` | Auto-pause timeout (e.g. `30m`) | `30m` |
| `AGENT_IMAGE` | Container image for sandbox agents | `ghcr.io/agentserver/opencode-agent:latest` |
| `LLMPROXY_URL` | Base URL of the LLM proxy service | - |
| `PASSWORD_AUTH_ENABLED` | Enable password-based auth | `true` |
| `OIDC_REDIRECT_BASE_URL` | External URL for OIDC callbacks | - |
| `GITHUB_CLIENT_ID` | GitHub OAuth client ID | - |
| `GITHUB_CLIENT_SECRET` | GitHub OAuth client secret | - |
| `OIDC_ISSUER_URL` | Generic OIDC issuer URL | - |
| `OIDC_CLIENT_ID` | Generic OIDC client ID | - |
| `OIDC_CLIENT_SECRET` | Generic OIDC client secret | - |
| `SANDBOX_NAMESPACE_PREFIX` | K8s namespace prefix | `agent-ws` |
| `NETWORKPOLICY_ENABLED` | Enable K8s NetworkPolicy isolation | `false` |
| `NETWORKPOLICY_DENY_CIDRS` | CIDRs to deny in network policies | - |
| `AGENTSERVER_NAMESPACE` | agentserver's own K8s namespace | - |
| `STORAGE_CLASS` | K8s storage class for PVCs | (cluster default) |
| `USER_DRIVE_SIZE` | Per-workspace storage size | `10Gi` |
| `USER_DRIVE_STORAGE_CLASS` | Storage class for workspace drives | inherits `STORAGE_CLASS` |
| `CC_BROKER_URL` | URL of the cc-broker service (required for TUI flow) | - |
| `EXECUTOR_REGISTRY_URL` | URL of the executor-registry service (required for TUI flow) | - |
| `INTERNAL_API_SECRET` | Shared secret for internal endpoints (recommended) | - |

</details>

<details>
<summary><strong>Environment Variables (LLM Proxy)</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `LLMPROXY_LISTEN_ADDR` | HTTP listen address | `:8081` |
| `LLMPROXY_DATABASE_URL` | Proxy's own PostgreSQL connection URL | - |
| `LLMPROXY_AGENTSERVER_URL` | agentserver internal API URL for token validation | (required) |
| `ANTHROPIC_API_KEY` | Anthropic API key | (required*) |
| `ANTHROPIC_AUTH_TOKEN` | Anthropic auth token (alternative to API key) | (required*) |
| `ANTHROPIC_BASE_URL` | Upstream Anthropic API URL | `https://api.anthropic.com` |
| `LLMPROXY_DEFAULT_MAX_RPD` | Default max requests per day per workspace (0 = unlimited) | `0` |

</details>

<details>
<summary><strong>Environment Variables (Sandbox Proxy)</strong></summary>

| Variable | Description | Default |
|----------|-------------|---------|
| `DATABASE_URL` | PostgreSQL connection string | (required) |
| `LISTEN_ADDR` | HTTP listen address | `:8082` |
| `BASE_DOMAIN` | Base domain for subdomain routing | (required) |
| `OPENCODE_SUBDOMAIN_PREFIX` | Subdomain prefix for opencode sandboxes | `code` |
| `OPENCLAW_SUBDOMAIN_PREFIX` | Subdomain prefix for openclaw sandboxes | `claw` |
| `OPENCODE_ASSET_DOMAIN` | Domain for opencode static assets | `opencodeapp.{BASE_DOMAIN}` |

</details>

<details>
<summary><strong>OIDC Authentication</strong></summary>

**GitHub OAuth:**

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set oidc.redirectBaseUrl="https://cli.example.com" \
  --set oidc.github.enabled=true \
  --set oidc.github.clientId="your-client-id" \
  --set oidc.github.clientSecret="your-client-secret"
```

Callback URL: `https://cli.example.com/api/auth/oidc/github/callback`

**Generic OIDC (Keycloak, Authentik, etc.):**

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set oidc.redirectBaseUrl="https://cli.example.com" \
  --set oidc.generic.enabled=true \
  --set oidc.generic.issuerUrl="https://idp.example.com/realms/main" \
  --set oidc.generic.clientId="agentserver" \
  --set oidc.generic.clientSecret="your-secret"
```

</details>

<details>
<summary><strong>Kubernetes Backend</strong></summary>

For production multi-tenant deployments with gVisor isolation:

```bash
helm upgrade agentserver oci://ghcr.io/agentserver/charts/agentserver \
  --reuse-values \
  --set backend=k8s \
  --set opencode.runtimeClassName=gvisor \
  --set sandbox.namespace=agentserver
```

</details>

## Building from Source

```bash
# Prerequisites: Go 1.26, Node.js, pnpm, bun

# Build everything (frontend + backend)
make build

# Build individual components
make backend          # Go binary → bin/agentserver
make frontend         # React frontend → web/dist/
make agent            # Local agent binary → bin/agentserver-agent
make agent-all        # Agent for all platforms (linux/darwin/windows, amd64/arm64)
make llmproxy         # LLM proxy binary → bin/llmproxy

# Docker images
make docker           # Main server image
make docker-agent     # Agent container image
make docker-llmproxy  # LLM proxy image
make docker-all       # All images
```

## Contributing

```bash
# Terminal 1: Start backend
go run . serve --db-url "postgres://..." --backend docker

# Terminal 2: Start frontend dev server
cd web && pnpm install && pnpm dev
```

Per the [Code of Conduct](#code-of-conduct), production code is AI-generated. Pull requests authored by an agent (with a human reviewer) are welcome; the repo is dogfooded against itself.

## License

[MIT](LICENSE)
