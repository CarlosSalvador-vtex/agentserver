# Jupyter Sandbox Type Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the supervisor-managed workspace notebook with a `jupyter` sandbox type alongside `opencode`/`claudecode`/`openclaw`/`nanoclaw`.

**Architecture:** New `case "jupyter"` in `internal/sandbox/manager.go` (pod with `agentserver-jupyter` image, `session-data` mount, port 8888) + new `internal/sandboxproxy/jupyter_proxy.go` (mirroring `claudecode_proxy.go` minus the tunnel/ttyd branches) + chart values for the image/runtimeClass/subdomainPrefix. Existing `*.agent.cs.ac.cn → sandboxproxy` HTTPRoute already covers `jupyter-*` as a subset; no Pulumi networking changes.

**Tech Stack:** Go (chi, k8s client-go), React/TypeScript (frontend), Helm chart, Pulumi (TypeScript).

**Source spec:** `docs/superpowers/specs/2026-05-19-jupyter-sandbox-type-design.md` (commit `bcbcc60`).

**Repo layout:** Phase A and B touch `/root/agentserver`. Phase C touches `/root/k8s`. Phase D is a manual cluster cleanup snippet (no code, no commit).

---

## Phase A — Add `jupyter` sandbox type (additive; old supervisor still works)

### Task A1: Add `Dockerfile.jupyter` + CI `build-jupyter` job

**Files:**
- Create: `Dockerfile.jupyter`
- Modify: `.github/workflows/build.yml`

- [ ] **Step 1: Create the Dockerfile**

Create `Dockerfile.jupyter`:
```dockerfile
# syntax=docker/dockerfile:1
FROM python:3.12-slim

ENV PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tini \
 && rm -rf /var/lib/apt/lists/*

RUN pip install --no-cache-dir \
      jupyter-server~=2.14 \
      jupyterlab~=4.2 \
      ipykernel~=6.29

COPY sdk/python /tmp/sdk
RUN pip install --no-cache-dir /tmp/sdk && rm -rf /tmp/sdk

COPY notebook/agentserver_jupyter_ext /tmp/agentserver_jupyter_ext
RUN pip install --no-cache-dir /tmp/agentserver_jupyter_ext && rm -rf /tmp/agentserver_jupyter_ext

COPY notebook/jupyter_server_config.py /etc/jupyter/

RUN mkdir -p /root/.ipython/profile_default/startup
COPY notebook/ipython_startup/00-ctx.py /root/.ipython/profile_default/startup/00-ctx.py

RUN useradd -m -u 1000 agent && mkdir -p /home/agent && chown -R agent:agent /home/agent
WORKDIR /home/agent

ENV JUPYTER_CONFIG_DIR=/etc/jupyter

EXPOSE 8888
ENTRYPOINT ["tini", "--", "jupyter", "server", "--config=/etc/jupyter/jupyter_server_config.py"]
```

(Differences vs `Dockerfile.notebook`: `useradd agent`, `WORKDIR /home/agent` instead of `/workspace`. The image will read `JUPYTER_TOKEN` and `NOTEBOOK_BASE_URL` from env — `jupyter_server_config.py` already supports both.)

- [ ] **Step 2: Add `build-jupyter` job in `.github/workflows/build.yml`**

Copy the entire `build-notebook:` job block (lines around 470–510) and rename in place. Paste it right after `build-notebook:` so both exist temporarily. Change:
- Job key: `build-jupyter:`
- `images: ${{ env.REGISTRY }}/agentserver/agentserver-jupyter`
- `file: ./Dockerfile.jupyter`

Also append `build-jupyter` to the `publish-helm.needs` list (currently 13 entries) — comma-add after `build-notebook`.

- [ ] **Step 3: Local Dockerfile sanity check**

Run: `docker build -f Dockerfile.jupyter -t agentserver-jupyter:test .`
Expected: image builds, no errors. (Skip if docker not available locally — CI will catch it.)

- [ ] **Step 4: Commit**

```bash
git add Dockerfile.jupyter .github/workflows/build.yml
git commit -m "feat(jupyter): add Dockerfile.jupyter + build-jupyter CI job"
```

---

### Task A2: Add `JupyterImage`/`JupyterPort`/`JupyterRuntimeClassName` to `sandbox.Config`

**Files:**
- Modify: `internal/sandbox/config.go`
- Modify: `internal/sandbox/config_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/sandbox/config_test.go`:
```go
func TestDefaultConfig_JupyterEnvWiring(t *testing.T) {
    t.Setenv("JUPYTER_IMAGE", "img:tag")
    t.Setenv("JUPYTER_RUNTIME_CLASS", "gvisor")
    c := DefaultConfig()
    if c.JupyterImage != "img:tag" {
        t.Errorf("JupyterImage=%q", c.JupyterImage)
    }
    if c.JupyterRuntimeClassName != "gvisor" {
        t.Errorf("JupyterRuntimeClassName=%q", c.JupyterRuntimeClassName)
    }
    if c.JupyterPort != 8888 {
        t.Errorf("JupyterPort=%d, want 8888", c.JupyterPort)
    }
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandbox/ -run TestDefaultConfig_JupyterEnvWiring -v`
Expected: compile error (`c.JupyterImage undefined`) or FAIL.

- [ ] **Step 3: Add the fields and defaults**

Edit `internal/sandbox/config.go`. In the `Config` struct (after the `ClaudeCode*` fields, around line 33):
```go
JupyterImage            string
JupyterPort             int
JupyterRuntimeClassName string
```

In `DefaultConfig()` (after the `ClaudeCode*` lines):
```go
JupyterImage:            os.Getenv("JUPYTER_IMAGE"),
JupyterPort:             8888,
JupyterRuntimeClassName: os.Getenv("JUPYTER_RUNTIME_CLASS"),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/sandbox/ -run TestDefaultConfig_JupyterEnvWiring -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/sandbox/config.go internal/sandbox/config_test.go
git commit -m "feat(jupyter): add JupyterImage/Port/RuntimeClass to sandbox.Config"
```

---

### Task A3: Add `case "jupyter"` to `sandbox.Manager`

**Files:**
- Modify: `internal/sandbox/manager.go` (two switches: container spec ~line 403 and runtimeClass ~line 908)
- Create: `internal/sandbox/manager_jupyter_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/sandbox/manager_jupyter_test.go`:
```go
package sandbox

import (
    "strings"
    "testing"
)

// TestBuildContainerEnv_JupyterShape is a smoke check that the new
// "jupyter" branch produces a container spec with the expected image,
// port, and required env. It exercises the unexported helper used by
// the manager when SandboxType=="jupyter".
//
// NOTE: This is a placeholder test until we expose a unit-testable
// builder. For now, the live integration coverage lives in
// internal/sandboxproxy/jupyter_proxy_test.go (Task A5) plus the
// chart-template smoke check (Task A6). The check below asserts that
// the Config plumbing compiles and that JUPYTER_IMAGE wired through.
func TestJupyterConfig_ImageRequired(t *testing.T) {
    c := Config{JupyterImage: "", JupyterPort: 8888}
    if c.JupyterImage != "" {
        t.Errorf("default JupyterImage should be empty until env set")
    }
    if c.JupyterPort != 8888 {
        t.Errorf("JupyterPort=%d", c.JupyterPort)
    }
    // Reachable string from the manager's error message.
    want := "JUPYTER_IMAGE"
    if !strings.Contains(want, "JUPYTER") {
        t.Fatal("sanity")
    }
}
```

(This is intentionally light — manager.go's case statement isn't pure-function unit-testable without a fake k8s client, and that scaffolding is overkill for a single switch arm. The real proof lands in Task A5 + Task A6.)

- [ ] **Step 2: Run test to verify it fails or compiles**

Run: `go test ./internal/sandbox/ -run TestJupyterConfig_ImageRequired -v`
Expected: PASS (it only asserts config wiring already added in A2).

- [ ] **Step 3: Add the `case "jupyter"` block in manager.go**

In `internal/sandbox/manager.go`, locate the container-spec switch (the `case "claudecode":` block around line 403). Add **before** the `default:` line:
```go
    case "jupyter":
        if m.cfg.JupyterImage == "" {
            return "", fmt.Errorf("JUPYTER_IMAGE not configured: set the environment variable to the jupyter container image (build with Dockerfile.jupyter)")
        }
        sandboxImage = m.cfg.JupyterImage
        containerPort = m.cfg.JupyterPort

        // Jupyter Server picks up JUPYTER_TOKEN as the built-in token
        // (defense in depth — actual access control is the sandboxproxy
        // jupyter-token cookie). NOTEBOOK_BASE_URL=/ keeps absolute
        // URLs rooted at the subdomain (matches the vhost convention).
        containerEnv = append(containerEnv,
            corev1.EnvVar{Name: "JUPYTER_TOKEN", Value: opts.ProxyToken},
            corev1.EnvVar{Name: "NOTEBOOK_BASE_URL", Value: "/"},
        )
```

- [ ] **Step 4: Add the runtimeClass case**

In the same file, locate the runtimeClass switch (the `case "claudecode":` block around line 908). Add before the closing brace:
```go
    case "jupyter":
        if m.cfg.JupyterRuntimeClassName != "" {
            return strPtr(m.cfg.JupyterRuntimeClassName)
        }
```

- [ ] **Step 5: Build check**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 6: Run the sandbox tests**

Run: `go test ./internal/sandbox/ -count=1`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/sandbox/manager.go internal/sandbox/manager_jupyter_test.go
git commit -m "feat(jupyter): add jupyter case to sandbox.Manager container spec"
```

---

### Task A4: Allow `jupyter` in the sandbox-create type validators

**Files:**
- Modify: `internal/server/server.go` (line ~1477)
- Modify: `internal/server/agent_register.go` (line ~80)

- [ ] **Step 1: Update `server.go` type allow-list**

Find this line in `internal/server/server.go`:
```go
if sandboxType != "opencode" && sandboxType != "openclaw" && sandboxType != "nanoclaw" && sandboxType != "claudecode" {
    http.Error(w, "invalid sandbox type: must be opencode, openclaw, nanoclaw, or claudecode", http.StatusBadRequest)
```
Change to:
```go
if sandboxType != "opencode" && sandboxType != "openclaw" && sandboxType != "nanoclaw" && sandboxType != "claudecode" && sandboxType != "jupyter" {
    http.Error(w, "invalid sandbox type: must be opencode, openclaw, nanoclaw, claudecode, or jupyter", http.StatusBadRequest)
```

- [ ] **Step 2: Update `agent_register.go` type allow-list**

Find this line in `internal/server/agent_register.go`:
```go
if sandboxType != "opencode" && sandboxType != "claudecode" && sandboxType != "custom" {
    http.Error(w, "invalid type: must be opencode, claudecode, or custom", http.StatusBadRequest)
```
Leave as-is. Agent self-registration is for *external* agents that announce themselves; jupyter pods never call this endpoint. Add a one-line comment above the check:
```go
// jupyter sandboxes are created via POST /api/workspaces/{wid}/sandboxes only;
// they don't self-register through this endpoint.
```

- [ ] **Step 3: Build**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 4: Commit**

```bash
git add internal/server/server.go internal/server/agent_register.go
git commit -m "feat(jupyter): allow jupyter as sandbox type in create endpoint"
```

---

### Task A5: `sandboxproxy/jupyter_proxy.go` + dispatcher wiring

**Files:**
- Create: `internal/sandboxproxy/jupyter_proxy.go`
- Create: `internal/sandboxproxy/jupyter_proxy_test.go`
- Modify: `internal/sandboxproxy/server.go` (add field + dispatch branch)
- Modify: `internal/sandboxproxy/config.go` (add env-loaded prefix)

- [ ] **Step 1: Write the failing test**

Create `internal/sandboxproxy/jupyter_proxy_test.go`:
```go
package sandboxproxy

import (
    "net/http"
    "net/http/httptest"
    "net/url"
    "testing"

    "github.com/agentserver/agentserver/internal/sbxstore"
)

// stubAuth + stubDB + stubSandboxes are minimal fakes; the real Auth/DB
// types are interfaces in the parent packages. If they aren't yet, this
// test will need adapters — guard by writing the test, running it,
// reading the compile error, and adding minimal interfaces.
//
// For simplicity in the first iteration this test asserts the handler
// surface: missing token → 400, missing cookie → 302 to base domain,
// /auth with valid token sets cookie + 302 /lab.

func TestJupyterProxy_MissingTokenRejected(t *testing.T) {
    s := &Server{BaseDomains: []string{"agent.test"}}
    req := httptest.NewRequest(http.MethodGet, "/auth", nil)
    rr := httptest.NewRecorder()
    s.handleJupyterSubdomainProxy(rr, req, "sbx-1")
    if rr.Code != http.StatusBadRequest {
        t.Errorf("missing token: status=%d, want 400", rr.Code)
    }
}

func TestJupyterProxy_MissingCookieRedirectsToLogin(t *testing.T) {
    s := &Server{BaseDomains: []string{"agent.test"}}
    req := httptest.NewRequest(http.MethodGet, "/lab", nil)
    rr := httptest.NewRecorder()
    s.handleJupyterSubdomainProxy(rr, req, "sbx-1")
    if rr.Code != http.StatusFound {
        t.Errorf("missing cookie: status=%d, want 302", rr.Code)
    }
    if loc := rr.Header().Get("Location"); loc != "https://agent.test/" {
        t.Errorf("Location=%q", loc)
    }
}

// More elaborate tests (auth exchange happy path, membership check,
// proxy success) need fakes for Auth/DB/Sandboxes. Add them only if
// the simple two above don't catch regressions in CI — keep this
// suite small and focused.
var _ = sbxstore.Sandbox{}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/sandboxproxy/ -run TestJupyterProxy -v`
Expected: compile error (`s.handleJupyterSubdomainProxy undefined`).

- [ ] **Step 3: Create `jupyter_proxy.go`**

Create `internal/sandboxproxy/jupyter_proxy.go`:
```go
package sandboxproxy

import (
    "encoding/base64"
    "log"
    "net/http"
    "net/http/httputil"
    "net/url"
    "time"
)

const (
    jupyterCookieKey    = "jupyter-token"
    jupyterPort         = "8888"
    jupyterCookieMaxTTL = 7 * 24 * time.Hour
)

// handleJupyterSubdomainProxy handles all requests on
// jupyter-{sandboxID}.{baseDomain}.
//
// Auth flow mirrors opencode/claudecode:
//  1. GET /auth?token=<main-site session>: validate, set per-subdomain
//     cookie (no Domain attr — scoped to this subdomain only), 302 /lab
//  2. All other requests: read cookie, validate, workspace membership,
//     reverse-proxy to the in-cluster Jupyter Server on the pod IP.
//
// Path is forwarded as-is. Jupyter runs with base_url=/ so absolute
// URLs in its HTML/JS work without any rewriting in this proxy.
func (s *Server) handleJupyterSubdomainProxy(w http.ResponseWriter, r *http.Request, sandboxID string) {
    if r.URL.Path == "/auth" && r.Method == http.MethodGet {
        s.exchangeJupyterToken(w, r, sandboxID)
        return
    }

    cookie, err := r.Cookie(jupyterCookieKey)
    if err != nil {
        http.Redirect(w, r, "https://"+s.matchedBaseDomain(r)+"/", http.StatusFound)
        return
    }
    userID, ok := s.Auth.ValidateToken(cookie.Value)
    if !ok {
        http.Redirect(w, r, "https://"+s.matchedBaseDomain(r)+"/", http.StatusFound)
        return
    }

    sbx, found := s.Sandboxes.Resolve(sandboxID)
    if !found || sbx.Type != "jupyter" {
        writeErrorPage(w, errPageSandboxNotFound)
        return
    }
    isMember, err := s.DB.IsWorkspaceMember(sbx.WorkspaceID, userID)
    if err != nil || !isMember {
        writeErrorPage(w, errPageSandboxNotFound)
        return
    }
    if sbx.Status != "running" {
        writeErrorPage(w, errPageSandboxNotRunning)
        return
    }
    if sbx.PodIP == "" {
        writeErrorPage(w, errPagePodNotReady)
        return
    }

    // Defense-in-depth: inject Jupyter's own token as Basic Auth so a
    // request that somehow reaches the pod without going through us
    // still gets bounced by Jupyter.
    if sbx.OpencodeToken != "" {
        cred := base64.StdEncoding.EncodeToString([]byte("jupyter:" + sbx.OpencodeToken))
        r.Header.Set("Authorization", "Basic "+cred)
    }

    s.throttledActivity(sandboxID)

    target := &url.URL{Scheme: "http", Host: sbx.PodIP + ":" + jupyterPort}
    proxy := httputil.NewSingleHostReverseProxy(target)
    proxy.FlushInterval = -1 // SSE + WebSocket streaming
    proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
        log.Printf("jupyter proxy error for sandbox %s: %v", sandboxID, err)
        http.Error(w, "proxy error", http.StatusBadGateway)
    }
    proxy.ServeHTTP(w, r)
}

func (s *Server) exchangeJupyterToken(w http.ResponseWriter, r *http.Request, sandboxID string) {
    tok := r.URL.Query().Get("token")
    if tok == "" {
        http.Error(w, "missing token", http.StatusBadRequest)
        return
    }
    userID, ok := s.Auth.ValidateToken(tok)
    if !ok {
        http.Error(w, "invalid token", http.StatusUnauthorized)
        return
    }
    sbx, found := s.Sandboxes.Resolve(sandboxID)
    if !found || sbx.Type != "jupyter" {
        writeErrorPage(w, errPageSandboxNotFound)
        return
    }
    isMember, err := s.DB.IsWorkspaceMember(sbx.WorkspaceID, userID)
    if err != nil || !isMember {
        writeErrorPage(w, errPageSandboxNotFound)
        return
    }
    http.SetCookie(w, &http.Cookie{
        Name:     jupyterCookieKey,
        Value:    tok,
        Path:     "/",
        HttpOnly: true,
        Secure:   true,
        SameSite: http.SameSiteLaxMode,
        MaxAge:   int(jupyterCookieMaxTTL.Seconds()),
    })
    http.Redirect(w, r, "/lab", http.StatusFound)
}
```

- [ ] **Step 4: Add `JupyterSubdomainPrefix` to `Server` struct**

In `internal/sandboxproxy/server.go`, add to the `Server` struct field block (alongside `ClaudeCodeSubdomainPrefix`):
```go
JupyterSubdomainPrefix string
```

In `New(...)` constructor, copy from config:
```go
JupyterSubdomainPrefix: cfg.JupyterSubdomainPrefix,
```

- [ ] **Step 5: Wire dispatch in `Server.Router()`**

In the same file, in the subdomain middleware loop (where `opcodePrefix`, `clawPrefix`, `claudePrefix` are declared and matched), add:

```go
jupyterPrefix := s.JupyterSubdomainPrefix + "-"
```

And in the per-entry match loop, after the `claudePrefix` `if` block, add:
```go
if strings.HasPrefix(sub, jupyterPrefix) {
    sandboxID := sub[len(jupyterPrefix):]
    s.handleJupyterSubdomainProxy(w, r, sandboxID)
    return
}
```

- [ ] **Step 6: Add `JupyterSubdomainPrefix` to `sandboxproxy.Config`**

In `internal/sandboxproxy/config.go`, append to the `Config` struct:
```go
JupyterSubdomainPrefix string
```

In `LoadConfigFromEnv()`:
```go
cfg.JupyterSubdomainPrefix = os.Getenv("JUPYTER_SUBDOMAIN_PREFIX")
// ... lower in the defaults block:
if cfg.JupyterSubdomainPrefix == "" {
    cfg.JupyterSubdomainPrefix = "jupyter"
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./internal/sandboxproxy/ -count=1`
Expected: PASS (the two `TestJupyterProxy_*` tests + existing ones).

- [ ] **Step 8: Commit**

```bash
git add internal/sandboxproxy/jupyter_proxy.go \
        internal/sandboxproxy/jupyter_proxy_test.go \
        internal/sandboxproxy/server.go \
        internal/sandboxproxy/config.go
git commit -m "feat(jupyter): sandboxproxy handler for jupyter-* subdomains"
```

---

### Task A6: Chart values + deployment env for jupyter

**Files:**
- Modify: `deploy/helm/agentserver/values.yaml`
- Modify: `deploy/helm/agentserver/templates/deployment.yaml`

- [ ] **Step 1: Add `sandbox.jupyter` values**

In `deploy/helm/agentserver/values.yaml`, after the `claudecode:` block (around line 108), add:
```yaml
  jupyter:
    image: ""                # e.g. ghcr.io/agentserver/agentserver-jupyter:main
    runtimeClassName: ""
    subdomainPrefix: "jupyter" # subdomain: {prefix}-{sandboxID}.{baseDomain}
```

- [ ] **Step 2: Pipe `JUPYTER_IMAGE`/`JUPYTER_RUNTIME_CLASS` into the agentserver container**

In `deploy/helm/agentserver/templates/deployment.yaml`, locate the `sandbox.claudecode.image` block (around lines 147–157). Right after it, add:
```yaml
            {{- if .Values.sandbox.jupyter.image }}
            - name: JUPYTER_IMAGE
              value: {{ .Values.sandbox.jupyter.image | quote }}
            {{- end }}
            {{- if .Values.sandbox.jupyter.runtimeClassName }}
            - name: JUPYTER_RUNTIME_CLASS
              value: {{ .Values.sandbox.jupyter.runtimeClassName | quote }}
            {{- end }}
```

- [ ] **Step 3: Pipe `JUPYTER_SUBDOMAIN_PREFIX` into the sandboxproxy container**

In the same file, find the `claudecode.subdomainPrefix` env line (around line 207). Right after it, add:
```yaml
            - name: JUPYTER_SUBDOMAIN_PREFIX
              value: {{ .Values.sandbox.jupyter.subdomainPrefix | default "jupyter" | quote }}
```

- [ ] **Step 4: Verify the rendered chart**

Run: `cd deploy/helm/agentserver && helm template . --set sandbox.jupyter.image=foo:bar --set sandbox.jupyter.runtimeClassName=gvisor | grep -A1 "JUPYTER_"`
Expected output contains:
```
- name: JUPYTER_IMAGE
  value: "foo:bar"
- name: JUPYTER_RUNTIME_CLASS
  value: "gvisor"
- name: JUPYTER_SUBDOMAIN_PREFIX
  value: "jupyter"
```

- [ ] **Step 5: Commit**

```bash
git add deploy/helm/agentserver/values.yaml deploy/helm/agentserver/templates/deployment.yaml
git commit -m "feat(jupyter): chart wires sandbox.jupyter.{image,runtimeClass,prefix}"
```

---

### Task A7: Frontend — add `jupyter` to the sandbox-create modal

**Files:**
- Modify: `web/src/components/CreateSandboxModal.tsx`

- [ ] **Step 1: Widen the type union and state**

In `web/src/components/CreateSandboxModal.tsx`, change line 8:
```ts
onCreate: (name: string, type: 'opencode' | 'nanoclaw' | 'claudecode' | 'jupyter', cpu?: number, memory?: number, idleTimeout?: number, metadata?: Record<string, unknown>) => void
```
And line 14:
```ts
const [sandboxType, setSandboxType] = useState<'opencode' | 'nanoclaw' | 'claudecode' | 'jupyter'>('opencode')
```

- [ ] **Step 2: Add the jupyter selector button**

After the `claudecode` button (around line 154 in the rendered JSX), add a new button mirroring the existing one but with `'jupyter'` and a Jupyter label. Add a short helper note rendered when `sandboxType === 'jupyter'`:
```tsx
{sandboxType === 'jupyter' && (
  <p className="mt-2 text-xs text-[var(--muted-foreground)]">
    Pausing this sandbox stops the Jupyter server — kernel state and unsaved variables are lost. Notebook files (.ipynb) on the session volume are preserved.
  </p>
)}
```

- [ ] **Step 3: Build the frontend**

Run: `cd web && pnpm build`
Expected: build succeeds.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/CreateSandboxModal.tsx web/dist
git commit -m "feat(jupyter): CreateSandboxModal lets users pick jupyter type"
```

(Include `web/dist` so the embedded build matches the source — agentserver Docker image embeds it.)

---

## Phase B — Remove the supervisor / vhost / JWT path

### Task B1: Frontend — drop `NotebooksPanel` and `createNotebookSession`

**Files:**
- Modify: `web/src/components/WorkspaceDetail.tsx` (import + usage)
- Delete: `web/src/components/NotebooksPanel.tsx`
- Modify: `web/src/lib/api.ts` (remove `createNotebookSession` + `NotebookSession`)

- [ ] **Step 1: Remove the panel from WorkspaceDetail**

In `web/src/components/WorkspaceDetail.tsx`:
- Delete the import on line 67: `import NotebooksPanel from './NotebooksPanel'`
- Delete the JSX usage on line 329: `<NotebooksPanel workspaceId={workspaceId} />`

- [ ] **Step 2: Delete the NotebooksPanel file**

```bash
rm web/src/components/NotebooksPanel.tsx
```

- [ ] **Step 3: Delete `createNotebookSession` and `NotebookSession`**

In `web/src/lib/api.ts`, delete lines ~1078-1106 (the entire `// === Notebook (Plan 3c) ===` block including the `NotebookSession` interface and `createNotebookSession` function).

- [ ] **Step 4: Rebuild the frontend**

Run: `cd web && pnpm build`
Expected: build succeeds, no unused-import errors.

- [ ] **Step 5: Commit**

```bash
git add web/src/components/WorkspaceDetail.tsx web/src/lib/api.ts web/dist
git rm web/src/components/NotebooksPanel.tsx
git commit -m "refactor(jupyter): remove NotebooksPanel + createNotebookSession (sandbox replaces it)"
```

---

### Task B2: Strip notebook routes / fields / middleware from `server.go`

**Files:**
- Modify: `internal/server/server.go`

- [ ] **Step 1: Delete the Router vhost middleware**

In `internal/server/server.go`, find the block beginning with `// Per-workspace notebook subdomain vhost` (inside `Router()`, near the top). Delete the entire `if s.NotebookHostBaseDomain != "" { ... }` block including the closing brace.

- [ ] **Step 2: Delete the notebook routes**

Same file, search for `s.postNotebookSession` and `s.notebookProxy`. Delete both `r.Post(...)` and `r.HandleFunc(...)` lines plus their leading comment block.

- [ ] **Step 3: Delete the notebook fields from `Server` struct**

Delete these fields:
- `NotebookSupervisor *notebooksupervisor.Supervisor`
- `NotebookJWTSecret []byte`
- `NotebookHostBaseDomain string`
- `NotebookSubdomainPrefix string`
- `testNotebookUpstream func(wsID string) (string, error)`

Also remove the `internal/notebooksupervisor` import line at the top of the file.

- [ ] **Step 4: Build check**

Run: `go build ./internal/server/...`
Expected: errors about undefined `postNotebookSession` / `notebookProxy` / `notebookVhost` — those handlers live in soon-to-be-deleted files. Defer to Task B4 to actually run a clean build.

- [ ] **Step 5: Commit (build will be red until Task B4)**

```bash
git add internal/server/server.go
git commit -m "refactor(jupyter): drop notebook routes + Server fields"
```

---

### Task B3: Strip the notebook supervisor init from `cmd/serve.go`

**Files:**
- Modify: `cmd/serve.go`

- [ ] **Step 1: Delete the supervisor init block**

In `cmd/serve.go`, delete the entire block starting at the comment `// Notebook supervisor (Plan 3a)` (around line 303) through the closing brace at line 347, including:
- the `if k8sClient != nil { ... }` block
- the `notebooksupervisor.New(...)` call + reaper goroutine
- the 11 `NOTEBOOK_*` env reads
- the `srv.NotebookHostBaseDomain = ...` + `srv.NotebookSubdomainPrefix = ...` lines

Remove the `internal/notebooksupervisor` import if it remains.

- [ ] **Step 2: Build check**

Run: `go build ./cmd/...`
Expected: still red because supervisor package referenced elsewhere — Task B4 fixes.

- [ ] **Step 3: Commit**

```bash
git add cmd/serve.go
git commit -m "refactor(jupyter): drop notebook supervisor init from serve.go"
```

---

### Task B4: Delete the notebook packages and files

**Files:**
- Delete: `internal/notebooksupervisor/` (whole directory)
- Delete: `internal/notebookjwt/` (whole directory)
- Delete: `internal/server/notebook_session.go` + `_test.go`
- Delete: `internal/server/notebook_proxy.go` + `_test.go`
- Delete: `internal/server/notebook_vhost.go` + `_test.go`
- Delete: `Dockerfile.notebook`

- [ ] **Step 1: rm the files**

```bash
rm -rf internal/notebooksupervisor internal/notebookjwt
rm internal/server/notebook_session.go internal/server/notebook_session_test.go
rm internal/server/notebook_proxy.go internal/server/notebook_proxy_test.go
rm internal/server/notebook_vhost.go internal/server/notebook_vhost_test.go
rm Dockerfile.notebook
```

- [ ] **Step 2: Find any leftover imports**

Run: `grep -rn "notebooksupervisor\|notebookjwt\|notebookProxy\|notebookVhost\|postNotebookSession" --include="*.go" .`
Expected: no matches. If matches exist, delete the referencing code (likely small leftovers from B2/B3).

- [ ] **Step 3: Full build + tests**

Run: `go build ./... && go test ./... -count=1`
Expected: all green.

- [ ] **Step 4: Commit**

```bash
git rm -r internal/notebooksupervisor internal/notebookjwt
git rm internal/server/notebook_session.go internal/server/notebook_session_test.go \
       internal/server/notebook_proxy.go internal/server/notebook_proxy_test.go \
       internal/server/notebook_vhost.go internal/server/notebook_vhost_test.go \
       Dockerfile.notebook
git commit -m "refactor(jupyter): delete notebooksupervisor + notebookjwt packages + Dockerfile.notebook"
```

---

### Task B5: Chart cleanup + 0.60.0 bump

**Files:**
- Modify: `deploy/helm/agentserver/values.yaml`
- Modify: `deploy/helm/agentserver/templates/deployment.yaml`
- Modify: `deploy/helm/agentserver/templates/httproute.yaml`
- Modify: `deploy/helm/agentserver/Chart.yaml`
- Modify: `.github/workflows/build.yml` (drop `build-notebook`)

- [ ] **Step 1: Delete the `notebook:` block in `values.yaml`**

Delete the entire `notebook:` top-level block (lines ~387–425, the whole multi-line block including `image:`, `resources:`, `idleTTLSeconds:`, etc.).

- [ ] **Step 2: Delete `NOTEBOOK_*` env lines in `deployment.yaml`**

In `deploy/helm/agentserver/templates/deployment.yaml`, delete every `- name: NOTEBOOK_*` env entry (there are ~12 of them) plus their surrounding `{{- if .Values.notebook.* }}` gates.

- [ ] **Step 3: Delete the notebook-vhost HTTPRoute**

In `deploy/helm/agentserver/templates/httproute.yaml`, delete the entire `{{- if .Values.notebook.host.baseDomain }}` block including its `---` separator (the block starts with the `# Per-workspace notebook vhost.` comment).

- [ ] **Step 4: Drop `build-notebook` from CI**

In `.github/workflows/build.yml`, delete the entire `build-notebook:` job block. Also remove `build-notebook` from the `publish-helm.needs` list (Task A1 added `build-jupyter` next to it).

- [ ] **Step 5: Bump Chart.yaml**

In `deploy/helm/agentserver/Chart.yaml`:
```yaml
version: 0.60.0
appVersion: "0.60.0"
```

- [ ] **Step 6: Render check**

Run: `cd deploy/helm/agentserver && helm template . --set sandbox.jupyter.image=foo:bar > /tmp/render.yaml`
Expected: no errors. Verify no `NOTEBOOK_` strings remain:
```
grep NOTEBOOK_ /tmp/render.yaml
```
Expected: no matches.

- [ ] **Step 7: Commit**

```bash
git add deploy/helm/agentserver/values.yaml \
        deploy/helm/agentserver/templates/deployment.yaml \
        deploy/helm/agentserver/templates/httproute.yaml \
        deploy/helm/agentserver/Chart.yaml \
        .github/workflows/build.yml
git commit -m "chore(jupyter): chart 0.60.0 — drop notebook values/env/HTTPRoute/CI job

BREAKING CHANGE: the /api/notebooks/* endpoints and the per-workspace
nb-* vhost are removed. Use the jupyter sandbox type instead.
"
```

---

## Phase C — Pulumi (`/root/k8s`)

### Task C1: Pulumi stack — drop notebook config, add jupyter image, bump chart

**Files:**
- Modify: `/root/k8s/stacks/agentserver.ts`

- [ ] **Step 1: Inspect current diffs first (memory rule)**

```bash
cd /root/k8s && git diff stacks/agentserver.ts
```
Read whatever's already there before staging — there may be WIP from another session.

- [ ] **Step 2: Remove `notebookJwtSecret` RandomPassword**

Delete the `const notebookJwtSecret = new random.RandomPassword(...)` block (around line 37–42).

- [ ] **Step 3: Remove `notebook:` from helm values**

In the `values: { ... }` block passed to `helm.v3.Release`, delete the entire `notebook: { jwtSecret, image, host, ... }` block.

- [ ] **Step 4: Add `sandbox.jupyter.image`**

In the `sandbox: { ... }` values, alongside `opencode`/`openclaw`/`claudecode`/`nanoclaw`, add:
```ts
jupyter: {
    image: "registry.nj.cs.ac.cn/ghcr/agentserver/agentserver-jupyter:main",
},
```

- [ ] **Step 5: Remove `agentserver-notebook-vhost-cn` HTTPRoute**

Delete the entire `const notebookVhostRouteCN = new k8s.apiextensions.CustomResource(...)` block (the 6b block).

- [ ] **Step 6: Bump chart version**

Change:
```ts
version: "0.59.x",  // whatever it currently is
```
to:
```ts
version: "0.60.0",
```

- [ ] **Step 7: Type-check**

```bash
cd /root/k8s && npx tsc --noEmit
```
Expected: no errors.

- [ ] **Step 8: Commit**

```bash
cd /root/k8s && git add stacks/agentserver.ts && git commit -m "chore(agentserver): chart 0.60.0 — switch notebook from supervisor to jupyter sandbox

Drops notebookJwtSecret, notebook.* values, and the nb-* HTTPRoute.
Adds sandbox.jupyter.image so the agentserver pod can spawn jupyter
sandbox pods. The wildcard *.agent.cs.ac.cn → sandboxproxy route
already covers jupyter-* subdomains as a subset.
"
```

---

## Phase D — Post-deploy cluster cleanup (manual, no commit)

These commands run **once** after `pulumi up` brings chart 0.60.0 online. They are not part of any commit; treat as runbook.

- [ ] **Step 1: Verify chart 0.60.0 is live**

```bash
helm -n agentserver list -o json | jq -r '.[] | "\(.name) \(.chart)"'
```
Expected output: `agentserver agentserver-0.60.0`.

- [ ] **Step 2: Delete supervisor-created notebook resources across all workspace namespaces**

```bash
kubectl get deploy -A -l managed-by=agentserver -o json \
  | jq -r '.items[] | select(.metadata.name | startswith("notebook-")) | "\(.metadata.namespace) \(.metadata.name)"' \
  | while read ns name; do
      kubectl -n "$ns" delete deploy "$name" --ignore-not-found
      kubectl -n "$ns" delete svc "$name" --ignore-not-found
    done
```

- [ ] **Step 3: Drop the dead HTTPRoute if Pulumi didn't garbage-collect it**

```bash
kubectl -n agentserver delete httproute agentserver-notebook-vhost-cn --ignore-not-found
```

- [ ] **Step 4: Spot-check a workspace**

Browse to a workspace, click "Create sandbox", select "Jupyter", give it a name, create. Open the resulting sandbox URL `https://jupyter-<sandboxID>.agent.cs.ac.cn/auth?token=<sess>` and verify JupyterLab loads.

---

## Self-review pass

| Spec requirement | Implemented in |
|---|---|
| New `jupyter` sandbox type, parallel to claudecode | Tasks A2, A3, A4 |
| `Dockerfile.jupyter` with `WORKDIR /home/agent` | Task A1 |
| `agentserver_jupyter_ext` retained | Task A1 (included in Dockerfile copy) |
| Mount only `session-data` (no workspace-drive) | Task A3 — no `case "jupyter"` volume mount add; default applies |
| `sandboxproxy/jupyter_proxy.go` mirroring claudecode pattern | Task A5 |
| Chart `sandbox.jupyter.{image,runtimeClass,subdomainPrefix}` | Task A6 |
| `JUPYTER_SUBDOMAIN_PREFIX` env on sandboxproxy | Task A6 step 3 |
| Frontend: jupyter selectable in create modal + tooltip | Task A7 |
| Frontend: NotebooksPanel + createNotebookSession removed | Task B1 |
| Server: `/api/notebooks/*` routes + vhost middleware removed | Task B2 |
| `cmd/serve.go` supervisor init removed | Task B3 |
| Packages `notebooksupervisor` + `notebookjwt` deleted | Task B4 |
| 3 `internal/server/notebook_*.go` files deleted | Task B4 |
| `Dockerfile.notebook` deleted | Task B4 |
| Chart bumped to 0.60.0 + notebook block removed + HTTPRoute removed | Task B5 |
| CI `build-notebook` removed, `build-jupyter` added | Task A1 + B5 |
| Pulumi: drop `notebookJwtSecret`, `notebook.*` values, `nb-*` HTTPRoute; add `sandbox.jupyter.image`; chart 0.60.0 | Task C1 |
| Cluster cleanup runbook | Phase D |

**Risks listed in spec**: each is acknowledged either inline in the task (B5 commit message: BREAKING CHANGE) or as a cleanup step (Phase D).

**Open items from spec**: cookie MaxAge 7d (matches claudecode — applied in Task A5 step 3); `JupyterPort` hardcoded 8888 (Task A2 default); `agentserver_jupyter_ext` retained (Task A1).
