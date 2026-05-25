# Sandboxproxy — Wildcard Subdomain Routing

How the `sandboxproxy` pod routes wildcard-subdomain traffic to sandbox pods.

---

## Where it fits

```
Browser
  │  https://<prefix>-<short_id>.<baseDomain>/...
  ▼
ALB ingress (wildcard rule for *.<baseDomain>)
  │
  ▼
agentserver-sandboxproxy pod   (deployment in `agentserver` ns)
  │
  ├── Match Host header → identify prefix (code-, claw-, claude-, jupyter-, hermes-)
  ├── Strip prefix → sandboxID
  ├── Lookup sandbox in DB → workspace + status + PodIP
  ├── Cookie auth + workspace-member check
  └── httputil.ReverseProxy → PodIP:<typePort>
                                       │
                                       ▼
                          agent-sandbox-<short>  (in agent-ws-<ws> ns)
```

The `agentserver` pod itself only handles the main UI/API at the bare `<baseDomain>`. Every other host that matches `*.<baseDomain>` is consumed by `sandboxproxy`.

---

## Routing table

| Prefix | Type | Handler | Pod port (target) | Cookie name |
|---|---|---|---|---|
| `code-` | opencode | `handleSubdomainProxy` | 4096 | `opencode-token` |
| `claw-` | openclaw | `handleOpenclawSubdomainProxy` | 18789 | `openclaw-token` |
| `claude-` | claudecode | `handleClaudeCodeSubdomainProxy` | 7681 (ttyd) | `claudecode-token` |
| `jupyter-` | jupyter | `handleJupyterSubdomainProxy` | 8888 | `jupyter-token` |
| `hermes-` | hermes | `handleHermesSubdomainProxy` | 9119 (dashboard) | `hermes-token` |

Each handler enforces:
1. **/auth?token=… (GET)** — validate the agentserver session token, set a *per-subdomain* cookie (no `Domain=` attr → scoped to that exact subdomain only), 302 to `/` (or `/lab` for Jupyter).
2. **All other paths** — read the cookie, validate, confirm workspace membership, reverse-proxy to `<PodIP>:<typePort>`.

---

## Why per-subdomain cookies (no `Domain=` attr)

If the cookie were set with `Domain=.<baseDomain>`, every sandbox subdomain would share the same cookie. A malicious sandbox could read another workspace's session via JS. By scoping to the exact host (no `Domain` attr), each sandbox can only see its own auth cookie.

This is also why `/auth?token=…` is a **handshake** — the main-site session token (from the user's `agentserver-token` cookie) is exchanged for a per-subdomain cookie that the sandbox subdomain can read.

---

## Adding a new sandbox type to the proxy

When you add a new type (e.g. `hermes`), three places change:

1. **`internal/sandboxproxy/<type>_proxy.go`** — new handler file. Cookie constant, port constant, two handler funcs (`handle<Type>SubdomainProxy` and `exchange<Type>Token`). Copy the Jupyter or OpenClaw template.

2. **`internal/sandboxproxy/server.go`** — wire the prefix into the host-match chain:
   ```go
   <type>Prefix := s.<Type>SubdomainPrefix + "-"
   …
   if strings.HasPrefix(sub, <type>Prefix) {
       sandboxID := sub[len(<type>Prefix):]
       s.handle<Type>SubdomainProxy(w, r, sandboxID)
       return
   }
   ```
   Plus the `<Type>SubdomainPrefix string` field on the `Server` struct.

3. **`internal/sandboxproxy/config.go`** — env var → struct field plumbing:
   ```go
   <Type>SubdomainPrefix: os.Getenv("<TYPE>_SUBDOMAIN_PREFIX"),
   …
   if cfg.<Type>SubdomainPrefix == "" {
       cfg.<Type>SubdomainPrefix = "<default>"
   }
   ```

Then **Helm chart**:

- `deploy/helm/agentserver/templates/sandboxproxy.yaml` — add `<TYPE>_SUBDOMAIN_PREFIX` env var on the container.

Then **rebuild + push + helm upgrade**. The sandboxproxy is a separate binary/image from the main `agentserver` pod — agentserver changes alone don't update the routing.

---

## Image build

The chart now consumes `.Values.sandboxProxy.image.repository` / `.tag` / `.pullPolicy`. For dev EKS we point at ECR:

```yaml
sandboxProxy:
  image:
    repository: 344729309528.dkr.ecr.us-east-1.amazonaws.com/agentserver-sandboxproxy
    tag: hermes
    pullPolicy: Always
```

`Dockerfile.sandboxproxy` was switched to cross-compile (`--platform=$BUILDPLATFORM` on Go + frontend builder stages, `GOOS=linux GOARCH=$TARGETARCH go build`). The Go toolchain segfaults under Rosetta when building `linux/amd64` natively on Apple Silicon — cross-compile from `linux/arm64` builder side-steps it.

---

## How to verify routing changes landed

```bash
CTX="arn:aws:eks:us-east-1:344729309528:cluster/dev-ti-eks-analytics-platform"

# Pod is running the new image
kubectl --context $CTX get deployment agentserver-sandboxproxy -n agentserver \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
# → 344729309528.dkr.ecr.us-east-1.amazonaws.com/agentserver-sandboxproxy:hermes

# All prefixes loaded
kubectl --context $CTX exec -n agentserver deployment/agentserver-sandboxproxy -- env | grep SUBDOMAIN
# OPENCODE_SUBDOMAIN_PREFIX=code
# OPENCLAW_SUBDOMAIN_PREFIX=claw
# CLAUDECODE_SUBDOMAIN_PREFIX=claude
# JUPYTER_SUBDOMAIN_PREFIX=jupyter
# HERMES_SUBDOMAIN_PREFIX=hermes

# Hit the subdomain (returns 302 after /auth, then the dashboard HTML)
curl -sSI "https://hermes-XXXX.agentserver.analytics.vtex.com/auth?token=<session>"

# Tail proxy logs while you hit it
kubectl --context $CTX logs deployment/agentserver-sandboxproxy -n agentserver -f | grep -v healthz
```

If you see `404 page not found` for a known prefix, three likely causes:
1. The pod is running the upstream image, not the fork — check the image ref.
2. The env var isn't set — check the sandboxproxy.yaml template.
3. Sandbox is paused or pod has no IP yet — `kubectl get sandbox` in the workspace ns.
