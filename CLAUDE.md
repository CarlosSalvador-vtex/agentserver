# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

**agentserver** — control plane for a Personal Compute Network. Enrolls laptops, cloud sandboxes, and IM bots into one workspace. Users drive them via a React web UI, `codex --remote`, Jupyter, or WeChat/Telegram/Matrix. Go 1.26 backend, React 19 + Vite frontend, PostgreSQL, Kubernetes sandboxes.

## Commands

### Backend
```bash
go run . serve --db-url "postgres://agentserver:agentserver@localhost:5432/agentserver?sslmode=disable"
go build -o bin/agentserver .          # main server
go build -o bin/llmproxy ./cmd/llmproxy
go build -o bin/credentialproxy ./cmd/credentialproxy
go test ./...                          # all tests
go test ./internal/server/... -run TestName  # single test
go vet ./...
make test                              # go vet + go test -count=1 (CI parity)
make agent                             # cmd/agentserver-agent (CGO_ENABLED=0)
make astool                            # cmd/astool admin CLI
```

**`goolm` build tag**: `internal/crypto/` (Matrix libolm) and `internal/weixin/` only compile under `-tags goolm`. To build/vet the whole tree including those packages:
```bash
go build -tags goolm ./...
go vet   -tags goolm ./...
```

### Frontend
```bash
cd web && pnpm install && pnpm dev     # dev server (Vite, hot reload)
cd web && pnpm build                   # production build → web/dist/
cd web && pnpm lint                    # ESLint
cd web && pnpm test                    # vitest
cd web && pnpm openapi:gen             # regenerate src/lib/api-generated/schema.d.ts from OpenAPI spec
```

### Full build
```bash
make build    # frontend + backend
make clean    # rm -rf bin/ web/dist/
```

### OpenAPI / docs
```bash
make openapi       # regenerate docs/api/openapi.{yaml,json} from swaggo annotations
make openapi-check # CI drift check (must match committed spec)
make api-docs      # regenerate docs/api/reference/ markdown from openapi.yaml
make api-docs-check
```

### Docker / local dev stack
```bash
docker-compose up         # postgres + server + llmproxy + credentialproxy
```

### Python SDK tests
```bash
cd sdk/python && .venv/bin/pytest -v
cd sdk/python && .venv/bin/ruff check .
```

## Architecture

```
cmd/                          # one subdir per binary; root.go + serve.go are the main agentserver CLI
  serve.go                    # cobra CLI — wires all env vars into server.Server{}
  agentserver-agent/          # connector agent binary (cross-compiled, see `make agent-all`)
  astool/                     # admin/ops CLI against the DB + API
  llmproxy/                   # standalone LLM proxy binary
  credentialproxy/            # credential injection binary
  codex-app-gateway/          # per-workspace codex app-server subprocess + ws bridge
  codex-exec-gateway/         # rendezvous for codex exec-server --remote connectors
  imbridge/                   # IM channel bridge binary (WeChat/Telegram/Matrix/WhatsApp)
  sandboxproxy/               # subdomain → sandbox service routing
  ilink-debug/                # connector link debug tool

internal/
  server/        # HTTP router (chi), all REST handlers, swagger annotations
  auth/          # session cookies, bcrypt login, OIDC/GitHub OAuth, Hydra device flow
  codexauth/     # self-hosted codex 0.132+ auth shim — PKCE, device flow, JWKS, token validation
  db/            # raw SQL via lib/pq, schema migrations in db/migrations/ (SQL files, numbered)
  sandbox/       # Kubernetes sandbox pod lifecycle (create/pause/resume/delete)
  sbxstore/      # in-memory sandbox state cache
  tunnel/, wsbridge/ # yamux multiplexed tunnel + ws bridging for connector ↔ server
  imbridge/      # IM message routing logic (WeChat weixin, Telegram, Matrix mautrix, WhatsApp)
  imbridgesvc/   # HTTP service wrapping imbridge (runs as the imbridge container)
  llmproxy/      # RPD quota enforcement + key injection
  credentialproxy/ # AES-256 encrypted credential bindings for sandboxes
  namespace/     # K8s namespace-per-workspace management
  codexexecgateway/, codexappgateway/ # codex remote rendezvous + app-server plumbing
  crypto/, weixin/ # Matrix libolm + WeChat helpers — require `-tags goolm`
  audit/         # session/audit event logging      notif/   # email + notification dispatch
  mcpbridge/     # MCP server bridging                secrets/ # secret storage
  storage/, container/, process/, namespace/ # sandbox + workspace infra plumbing

web/src/
  components/    # React page components (one file per panel/modal)
  lib/
    api.ts               # typed wrappers over fetch, exports domain types from openapi schema
    apiClient.ts         # base apiFetch() + ApiError
    api-generated/       # auto-generated from openapi.yaml via `pnpm openapi:gen` — DO NOT hand-edit
```

### Key data flows

**Connector enrollment**: `codex exec-server --remote` → codex-exec-gateway (`:6060`) → yamux tunnel registered in `tunnel.Registry` → visible in UI as an executor.

**Browser session**: `codex --remote` → codex-app-gateway (`:8086`) → per-workspace codex app-server subprocess → routes to Connector.

**LLM calls from sandboxes**: sandbox → credentialproxy (injects real API keys) → llmproxy (quota check, usage track) → upstream provider (Anthropic/OpenAI).

**IM inbound**: WeChat/Telegram/Matrix → imbridge → agentserver REST → dispatches to bound sandbox/connector.

### Auth layers

- **Session cookie** (`agentserver-token`): password login, 7-day TTL, bcrypt hash.
- **OIDC**: GitHub OAuth or generic OIDC (Keycloak/Authentik). Managed by `auth.OIDCManager`.
- **Hydra device flow**: `internal/auth/hydra.go` — for agent device authorization.
- **codexauth** (`internal/codexauth/`): self-hosted OAuth2/OIDC shim for `codex` 0.132+ clients — PKCE, JWKS, token signing with per-instance RSA key.
- **Workspace API keys**: scoped tokens for programmatic access; stored hashed in DB.
- **Proxy tokens**: short-lived tokens for internal service-to-service calls.

### Database

Migrations live in `internal/db/migrations/` as numbered SQL files (e.g. `038_workspace_verify_token.sql`). Applied at startup. Add new migrations by incrementing the number.

Frontend types are generated from `docs/api/openapi.yaml` — after changing handler swagger annotations, run `make openapi` then `pnpm openapi:gen` in `web/`.

## Environment variables

| Variable | Required | Notes |
|---|---|---|
| `DATABASE_URL` | Yes | PostgreSQL DSN |
| `PASSWORD_AUTH_ENABLED` | No | Default `true`; set `false` to disable password login |
| `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET` | No | GitHub OAuth |
| `OIDC_ISSUER_URL` / `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | No | Generic OIDC |
| `LLMPROXY_URL` | No | e.g. `http://llmproxy:8081` |
| `BASE_DOMAIN` | No | Subdomain routing base (e.g. `agent.cs.ac.cn`) |
| `INTERNAL_API_SECRET` | Recommended | Shared secret for internal endpoints |
| `AGENTSERVER_COOKIE_DOMAIN` | No | Set for cross-subdomain SSO (e.g. `.agent.cs.ac.cn`) |

## Contribution workflow (this fork)

This is a fork tracked via numbered backlog activities. A Cursor agent rule (`.cursor/rules/context-guard.mdc`) drives a strict per-activity workflow — Claude Code orchestrates merges. When making changes, follow the same conventions:

- **One branch + one PR per activity** — no batching.
- **Branch naming**: `docs/<activity-id>-<slug>` (e.g. `docs/b09-choose-workspace-apex`).
- **PR title**: `docs(<id>): <short description>`.
- **Never push to `main`, never force-push.** Open the PR as soon as the branch has a commit.
- `CURSOR_CONTEXT.md` (repo root) is the live task briefing/handoff state; activity specs live in `docs/cursor-handoffs/`.

## Project skill & deploy

- **`skills/agentserver-helper/SKILL.md`** — invoke for fork-specific workflows: building/pushing container images (`scripts/build/`), deploying to the `dev-ti-eks-analytics-platform` EKS cluster, playground soul/skill drafts, IM channel routing, sandbox boot debugging. Indexes critical design docs under `docs/`.
- **Deploy**: Helm chart in `deploy/helm/agentserver/`; env overlays are repo-root `values-dev-eks.yaml` / `values-staging-eks.yaml` (bump image tags there). One `Dockerfile.<service>` per binary at repo root.
