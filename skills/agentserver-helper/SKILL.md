---
name: agentserver-helper
description: Navigate + operate the agentserver fork (OpenClaw + Hermes): build images, deploy dev EKS, run playground, debug skills, route IM channels, WhatsApp.
---

# Agentserver Helper

## When to use

Invoke this skill when working on the `agentserver-study` fork: editing Go services, building/pushing container images, deploying to the `dev-ti-eks-analytics-platform` EKS cluster, iterating on playground soul/skill drafts, wiring multi-channel IM routing (WeChat / Telegram / Matrix / WhatsApp), or debugging sandbox pod boot + OpenClaw skill mounting. It indexes the repo layout, common workflows, and the sharp edges we already paid for.

## Repo layout

Top-level Go service (`main.go` + `cmd/`) plus a React frontend in `web/` and a Helm chart in `deploy/helm/agentserver/`.

- `cmd/` — Go binaries. `cmd/serve.go` is the main agentserver entrypoint. Also `cmd/imbridge`, `cmd/llmproxy`, `cmd/sandboxproxy`, `cmd/credentialproxy`, `cmd/agentserver-agent`.
- `internal/server/` — REST handlers, auth, request plumbing.
- `internal/sandbox/` — Pod lifecycle: `manager.go`, `composition.go`, ephemeral ConfigMap mounting for playground drafts.
- `internal/imbridge/` — IM channel providers (WeChat, Telegram, Matrix, WhatsApp). `whatsapp_provider.go` is the WhatsApp Cloud impl.
- `internal/imbridgesvc/` — HTTP service wrapping `imbridge`; runs as the `imbridge` container.
- `internal/db/` — Postgres migrations + DB layer. Migration 031 = sandboxes timestamp hotfix, 032 = playground tables, 033+ = channel routing.
- `internal/llmproxy/` — LiteLLM / Anthropic proxy. Ships as the `llmproxy` image.
- `internal/sandboxproxy/`, `internal/sbxstore/`, `internal/tunnel/`, `internal/wsbridge/` — sandbox traffic + storage plumbing.
- `internal/crypto/`, `internal/weixin/` — matrix crypto (libolm) and WeChat helpers; require build tag `goolm`.
- `web/src/` — React frontend (TypeScript + Tailwind, no Monaco). Playground routes at `/playground/*`.
- `web/` — uses pnpm. `pnpm openapi:gen` regenerates types from `docs/api/openapi.yaml`.
- `deploy/helm/agentserver/` — Helm chart (`templates/`, `values.yaml`, `skills/`, `souls/`, `iam/`).
- `values-dev-eks.yaml` (repo root) — overlay for the dev EKS cluster. Bump image tags here.
- `docs/` — design docs. Critical reads: `playground-design.md`, `multi-channel-routing.md`, `whatsapp-cloud-integration.md`, `openclaw-skill-slash-research.md`, `dev-eks-deploy.md`, `sandbox-architecture.md`.
- `scripts/build/` — Docker build + push to ECR: `login.sh`, `build-one.sh`, `build-all.sh` (keys in `scripts/build/README.md`).
- `Dockerfile.*` — one Dockerfile per service at repo root.

## Common workflows

### Local build + lint (Go)

```bash
go build -tags goolm ./...
go vet  -tags goolm ./...
```

The `goolm` tag is REQUIRED. Without it the build fails on Apple Silicon with libolm errors (matrix crypto package).

### Frontend build

```bash
cd web
pnpm openapi:gen    # regenerate TS types from docs/api/openapi.yaml; run when API changes
pnpm build          # tsc + vite, must be clean before PR
```

### Build + push a single image

```bash
bash scripts/build/login.sh                          # once per session (12h ECR token)
bash scripts/build/build-one.sh <key> [tag]          # tag defaults to "dev"
```

Image keys (see `scripts/build/README.md`): `agentserver`, `imbridge`, `llmproxy`, `sandboxproxy`, `credentialproxy`, `openclaw`. Registry: `344729309528.dkr.ecr.us-east-1.amazonaws.com`.

### Build + push all images

```bash
bash scripts/build/build-all.sh [tag]
```

### Dev EKS deploy

```bash
export CTX=arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform

# 1) bump image tag(s) in values-dev-eks.yaml first
# 2) then upgrade
helm upgrade agentserver deploy/helm/agentserver \
  -n agentserver --kube-context "$CTX" \
  -f values-dev-eks.yaml
```

Never `helm upgrade` without bumping the tag — Helm sees no diff and silently rolls back to the previous revision.

### Verify a sandbox pod boots

```bash
kubectl --context "$CTX" get pods -n agent-ws-<workspace-prefix>
kubectl --context "$CTX" exec -n agent-ws-<workspace-prefix> <pod> -- \
  cat /home/agent/.openclaw/openclaw.json
```

If the file is missing or the pod crashloops, check the openclaw container logs and re-read the gotchas below.

## Playground (soul + skill drafts)

- DB-backed drafts in tables `skill_drafts` and `soul_drafts` (added in migration 032).
- Promote endpoint opens a PR against the soul/skill repo via the GitHub Contents API.
- Composition `{soul, skills[], config}` is accepted at sandbox create; the boot path mounts ephemeral ConfigMaps under `/home/agent/.openclaw/extensions/<skill>/`.
- HTTP routes (under `internal/server/`):
  - `GET/POST/PUT /api/playground/skills`
  - `GET/POST/PUT /api/playground/souls`
  - `POST /api/playground/skills/{id}/dry-run`
  - `POST /api/playground/skills/{id}/promote`
  - `POST /api/playground/skills/{id}/test-sandbox`
- Frontend: `/playground` catalog, editors at `/playground/skills/:id` and `/playground/souls/:id`.
- Reference: `docs/playground-design.md` (878 lines incl. Appendix A alternatives).

## Multi-channel routing

- Each workspace has a `channel_routing_strategy`: `shared` | `per_agent` | `hybrid`.
- Junction table `sandbox_channel_bindings` (N:M sandbox ↔ channel).
- Endpoints:
  - `GET/PUT /api/workspaces/{id}/routing-strategy`
  - `POST   /api/sandboxes/{id}/im/bind-multi`
  - `POST   /api/workspaces/{id}/im/channels/{channelId}/auto-bind`
- WhatsApp Cloud provider in `internal/imbridge/whatsapp_provider.go`; webhook at `/webhook/whatsapp`. Verification via `WHATSAPP_WEBHOOK_VERIFY_TOKEN` env; payload HMAC via `WHATSAPP_APP_SECRET`.
- Reference: `docs/multi-channel-routing.md`, `docs/whatsapp-cloud-integration.md`.

## Conventions + gotchas

- **Image platform**: always build `--platform linux/amd64`. Developer is on Apple Silicon; EKS nodes are AMD64. `build-one.sh` already does this — don't override it.
- **Go build tag**: `-tags goolm` everywhere (build, vet, test). Skipping it breaks libolm-using packages (matrix provider, crypto).
- **OpenClaw manifest filename**: `openclaw.plugin.json`, NOT `plugin.json`. Must declare `id` and `configSchema`. Background in `docs/openclaw-skill-slash-research.md`.
- **OpenClaw runs as user `agent`** → skills mount at `/home/agent/.openclaw/extensions/<name>/`. Older code mounted at `/home/node/` which was wrong (fixed in PR #13).
- **Skill loader rejects stale entries**: `plugins.installs.<name>` with `source: "path"` is dropped as "stale". Rely on auto-discovery from the extensions dir (PR #13).
- **`register()` must be synchronous**: an async `register()` is silently ignored by the loader. Use `fs.readFileSync` at module init for any setup that needs file I/O.
- **OpenClaw root config schema is strict**: unknown keys are rejected. We hit this with `agent.systemPrompt` (PR #25). Stick to `gateway`, `models`, `plugins`, `channels` until the correct field is identified.
- **`sandboxes.nanoclaw_bridge_secret` is nullable**: scan into `sql.NullString` (PR #12). A plain `string` will fail on null rows.
- **`sandboxes.updated_at` does NOT exist** — only `created_at` and `last_activity_at`. Caught during migration 031 hotfix (PR #6).
- **API surface changes** → regenerate frontend types: `pnpm openapi:gen` in `web/`.
- **values-dev-eks.yaml** is the canonical dev overlay. There is a separate `values-litellm-dev-eks.yaml` for the litellm sub-deploy.

## Useful commits to grep

Use `gh pr view <N> --repo CarlosSalvador-vtex/agentserver` for details.

- **PR #3** — routing strategy schema: adds `channel_routing_strategy` + `sandbox_channel_bindings`.
- **PR #4** — auto-bind endpoint: `POST /api/workspaces/{id}/im/channels/{channelId}/auto-bind`.
- **PR #6** — migration 031 hotfix for `sandboxes.updated_at` (column never existed).
- **PR #7** — WhatsApp Cloud provider in `internal/imbridge/whatsapp_provider.go` + webhook wiring.
- **PR #12** — null-safe scan of `nanoclaw_bridge_secret` (`sql.NullString`).
- **PR #13** — skill discoverability fix: drop stale `plugins.installs.*`, mount under `/home/agent/`, sync `register()`.
- **PR #15** — playground schema (migration 032): `skill_drafts`, `soul_drafts`.
- **PR #25** — OpenClaw root config strict-mode regression (`agent.systemPrompt` rejected).

## Output policy

- Before committing backend changes: `go build -tags goolm ./... && go vet -tags goolm ./...` — both must pass.
- Before committing frontend changes: `cd web && pnpm build` — tsc + vite must be clean. Re-run `pnpm openapi:gen` if the OpenAPI doc changed.
- For deploys: bump the image tag in `values-dev-eks.yaml` THEN `helm upgrade`. Never the other way around.
- PRs: concise title (imperative, < 70 chars) and a `## Test plan` section listing what you verified (build, vet, helm template, pod boot, endpoint hit).
- Do not edit `vendor/`, generated OpenAPI types under `web/src/api/`, or `go.sum` by hand.
