# Hermes Sandbox Type — How It Was Wired Up

How `hermes` was added as a first-class sandbox type in the agentserver fork, running `nousresearch/hermes-agent` on dev EKS with Bedrock (primary) + Z.AI GLM-5.1 (fallback).

---

## Architecture

```
agentserver (Go)
   └── creates Sandbox CRD (type=hermes) in agent-ws-<id> namespace
         └── auto-creates ServiceAccount "hermes" annotated with IRSA role ARN
         └── replicates ConfigMap "agentserver-hermes-config" into the ws ns
         └── Pod (agent-sandbox-<id>)
               ├── serviceAccountName: hermes  ──► IRSA ──► Bedrock InvokeModel
               ├── image: docker.io/nousresearch/hermes-agent:main
               ├── command: <image default /init>
               ├── args: ["gateway", "run"]      (skips first-run wizard)
               ├── env:
               │     GLM_API_KEY=<Z.AI token>   ──► fallback provider zai/glm-5.1
               │     AWS_REGION=us-east-1
               │     AWS_ROLE_ARN, AWS_WEB_IDENTITY_TOKEN_FILE  (auto by EKS)
               ├── volumes:
               │     session-data PVC  → /opt/data        (hermes data dir)
               │     hermes-config CM  → /opt/data/config.yaml (subPath, RO)
               └── containerPort: 9119  (Hermes Dashboard / Web UI)
```

Sandbox subdomain (`hermes-<short>.agentserver.analytics.vtex.com`) routes through sandboxproxy to the pod port 9119.

---

## Files touched

### Go backend

- **`internal/sandbox/config.go`** — added fields:
  - `HermesImage` ← env `HERMES_IMAGE`
  - `HermesPort` = 9119 (Hermes Dashboard)
  - `HermesRuntimeClassName` ← env `HERMES_RUNTIME_CLASS`
  - `HermesConfigMapName` ← env `HERMES_CONFIGMAP_NAME`
  - `HermesGLMAPIKey` ← env `HERMES_GLM_API_KEY`
  - `HermesServiceAccountRoleArn` ← env `HERMES_SA_ROLE_ARN`

- **`internal/sandbox/manager.go`**:
  - `case "hermes"` in container build switch: sets image, args=`["gateway","run"]`, env (AWS_REGION, GLM_API_KEY, HERMES_DASHBOARD=1).
  - `containerArgs` field passed through to pod spec as `Args` (preserves the image's `/init` entrypoint for s6-overlay supervisor).
  - Session PVC mount path overridden to `/opt/data` for hermes (`~/.hermes` equivalent inside the upstream image).
  - ConfigMap mount overlays `/opt/data/config.yaml` (subPath, ReadOnly).
  - Sets `ServiceAccountName: hermes` on the pod spec when role ARN is configured.
  - `ensureHermesServiceAccount()` — creates the SA with IRSA annotation in the workspace ns (idempotent).
  - `replicateHermesConfigMap()` — copies the cluster-wide hermes config map from the agentserver release ns into the workspace ns (ConfigMaps are namespace-scoped).

- **`internal/server/server.go`**:
  - Added `hermes` to the valid sandbox types check.
  - Added `HermesURL` field in `sandboxResponse`.
  - URL builder: `https://<HermesSubdomainPrefix>-<short_id>.<baseDomain>/auth?token=<authToken>` (prefix default `hermes`).

### Frontend (Web UI)

- **`web/src/components/CreateSandboxModal.tsx`** — Hermes button wired to set `sandboxType='hermes'` (was placeholder disabled before).
- Type union `'opencode' | 'openclaw' | 'nanoclaw' | 'claudecode' | 'jupyter' | 'hermes'` extended in `CreateSandboxModal.tsx`, `SandboxList.tsx`, and `lib/api.ts`.

### Helm chart

- **`deploy/helm/agentserver/values.yaml`** — added `sandbox.hermes` block with `image`, `subdomainPrefix`, `serviceAccountRoleArn`, `glmApiKey`, `config`.
- **`deploy/helm/agentserver/templates/deployment.yaml`** — env vars `HERMES_IMAGE`, `HERMES_SUBDOMAIN_PREFIX`, `HERMES_CONFIGMAP_NAME`, `HERMES_GLM_API_KEY`, `HERMES_SA_ROLE_ARN`.
- **`deploy/helm/agentserver/templates/hermes-config.yaml`** (new) — ConfigMap rendered from `.Values.sandbox.hermes.config` (raw YAML string).
- **`deploy/helm/agentserver/templates/rbac.yaml`** — added `serviceaccounts` and `configmaps` to the cluster role rules so agentserver can create them in workspace namespaces.

### IAM (out-of-band, AWS)

- **`deploy/helm/agentserver/iam/hermes/`** — committed reference files:
  - `trust-policy.json` — Federated IRSA trust with `StringLike` wildcard on `sub` for `system:serviceaccount:agent-ws-*:hermes` so every workspace ns is covered without per-workspace IAM changes.
  - `policy.json` — `bedrock:InvokeModel` + `InvokeModelWithResponseStream` + `Converse` + `ConverseStream` on the Sonnet 4.6 inference profile ARN and foundation models in us-east-1/2/west-2.
  - `README.md` — create commands.

Created in AWS:
- Role: `arn:aws:iam::344729309528:role/dev-ti-eks-analytics-platform-hermes-bedrock-role`
- Inline policy: `bedrock-invoke-hermes`

### dev EKS values

`values-dev-eks.yaml`:
```yaml
sandbox:
  hermes:
    image: "docker.io/nousresearch/hermes-agent:main"
    subdomainPrefix: "hermes"
    serviceAccountRoleArn: "arn:aws:iam::344729309528:role/dev-ti-eks-analytics-platform-hermes-bedrock-role"
    glmApiKey: "<Z.AI token>"
    config: |
      model:
        default: arn:aws:bedrock:us-east-1:344729309528:application-inference-profile/62r7btpf0s40
        provider: bedrock
      providers: {}
      fallback_providers:
        - model: zai/glm-5.1
          provider: zai
      agent:
        max_turns: 80
```

---

## Sequence on first hermes sandbox create

1. **UI** POSTs `/api/workspaces/<wid>/sandboxes` with `{"type":"hermes",...}`.
2. **agentserver** validates type, creates DB row, generates short ID + auth token.
3. Switch hits `case "hermes"` — image, args, env, mount paths set.
4. `ensureHermesServiceAccount(ctx, ns)` — idempotent create of SA `hermes` in `agent-ws-<id>` with `eks.amazonaws.com/role-arn` annotation. EKS Pod Identity webhook auto-injects `AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE` into pods using this SA.
5. `replicateHermesConfigMap(ctx, ns)` — copy `agentserver-hermes-config` from `agentserver` ns into `agent-ws-<id>` ns (ConfigMaps are namespace-scoped, can't be read across).
6. Create Sandbox CRD. `agent-sandbox-controller` reconciles and creates the pod with the podTemplate.
7. Pod starts. `/init` (s6-overlay) sets up services, then runs `hermes gateway run`.
8. Hermes reads `/opt/data/config.yaml`, detects `provider: bedrock` + `fallback_providers: [zai/glm-5.1]`.
9. Bedrock calls go via boto3 which auto-uses IRSA creds from the env vars + token file.
10. Web UI listens on `0.0.0.0:9119`. Sandbox subdomain (`hermes-<short>.<baseDomain>`) routes here via ALB ingress wildcard + sandboxproxy.

---

## Gotchas hit during integration

| Issue | Cause | Fix |
|---|---|---|
| `Duplicate value: {"name":"ANTHROPIC_BASE_URL"}` CRD validation error | Manually added env vars already injected by the shared LLM-proxy block | Removed duplicates; rely on shared block |
| `ImagePullBackOff 403` on `ghcr.io/nousresearch/hermes-agent:latest` | GHCR repo is gated, no anon pull | Switched to `docker.io/nousresearch/hermes-agent:main` (public, 1.5M pulls) |
| `It looks like Hermes isn't configured yet — no API keys or providers found` | Mounted config.yaml at `/home/agent/.hermes/config.yaml`; image expects `/opt/data/config.yaml` | Mount session PVC at `/opt/data` and overlay ConfigMap at `/opt/data/config.yaml` (subPath) |
| `exec: "gateway": executable file not found in $PATH` | Override of pod `Command` bypassed the s6 `/init` entrypoint that puts `hermes` on `$PATH` | Use pod `Args` (not `Command`) — `["gateway","run"]` becomes CMD passed to `/init` |
| `serviceaccounts is forbidden` for agentserver SA | ClusterRole `agentserver-sandbox` only granted namespaces/networkpolicies/sandboxes/pods/PVCs | Added `serviceaccounts` and `configmaps` to the role's rules |
| ConfigMap not visible in workspace ns | ConfigMaps are namespace-scoped | `replicateHermesConfigMap()` copies it from `agentserver` ns into each `agent-ws-*` ns |
| First Hermes container exited cleanly but with no provider | Container ran default `setup` interactive wizard, no TTY | Args `["gateway","run"]` bypasses the wizard when `/opt/data/config.yaml` is present |

---

## How to verify it's working

```bash
CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"

# 1. Sandbox is Running, single container, no restarts
kubectl --context $CTX get pod -l managed-by=agentserver -n agent-ws-<short> 

# 2. SA + ConfigMap auto-created
kubectl --context $CTX get sa,configmap -n agent-ws-<short>
#   serviceaccount/hermes  (annotated)
#   configmap/agentserver-hermes-config

# 3. Pod logs end with: "Hermes Web UI → http://0.0.0.0:9119"
kubectl --context $CTX logs agent-sandbox-<short> -n agent-ws-<short> -c agent | tail -20

# 4. IRSA wired
kubectl --context $CTX exec agent-sandbox-<short> -n agent-ws-<short> -c agent -- env | grep AWS_ROLE_ARN
#   AWS_ROLE_ARN=arn:aws:iam::344729309528:role/dev-ti-eks-analytics-platform-hermes-bedrock-role
#   AWS_WEB_IDENTITY_TOKEN_FILE=/var/run/secrets/eks.amazonaws.com/serviceaccount/token

# 5. Dashboard responds via port-forward
kubectl --context $CTX port-forward pod/agent-sandbox-<short> 9119:9119 -n agent-ws-<short> &
curl -sS http://localhost:9119/ | grep -i "Hermes Agent - Dashboard"

# 6. Via the agentserver UI: open the sandbox → "Open" button → chat with model=bedrock/Sonnet-4.6
```

---

## Next steps (out of scope of this doc)

- **Pause/resume**: session-data PVC at `/opt/data` survives pod restart, but Hermes runtime DB initialization may require deeper handling.
- **Auto-cleanup of SA / ConfigMap on workspace deletion**: currently lifecycle-bound to the namespace (cascade delete). No explicit cleanup needed.
- **Per-workspace token isolation**: today, every hermes sandbox in a workspace shares the same IRSA role + GLM key from the chart values. For multi-tenant production, scope keys per workspace via External Secrets.
