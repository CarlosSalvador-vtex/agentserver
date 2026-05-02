# Mobile Agent — P0: Modelserver Device Flow Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add RFC 8628 (OAuth 2.0 Device Authorization Grant) support to modelserver so the Flutter mobile agent can obtain `user:inference` access tokens directly from the device, without involving the agentserver cluster in the LLM request path.

**Architecture:** Leverage the existing Ory Hydra v26.2 deployment that already fronts modelserver's OAuth. Hydra natively implements device authorization grant, so the work reduces to: (1) register a new public OAuth client for the mobile app, (2) reverse-proxy Hydra's public device auth and token endpoints through modelserver's proxy HTTP server at stable URLs so the mobile app has one base URL, (3) confirm the existing `/v1/messages` introspection path accepts the new client's tokens. All LLM traffic remains device→modelserver direct; agentserver is never touched.

**Tech Stack:** Go 1.26, chi v5, Ory Hydra v26.2, docker-compose for local dev, viper config, PostgreSQL.

**Repository:** `/root/coding/modelserver`

**Depends on:** None. This plan is the prerequisite for P2 (Flutter core auth).

**Consumed by:**
- P1 agentserver plan references the new client ID in documentation only (no code coupling).
- P2 Flutter core plan calls the new endpoints in every agent loop turn.

---

## Background & Key Facts

From codebase investigation:

- **Entry point:** `cmd/modelserver/main.go` mounts two chi routers — one for `/v1/*` (proxy, port 8080) and one for `/api/v1/*` (admin, port 8081). The two routers run on separate HTTP servers.
- **`/v1/messages`:** Defined at `internal/proxy/router.go:25`. Wrapped by `AuthMiddleware` (`internal/proxy/auth_middleware.go`) which already supports a Hydra token-introspection fallback when API key lookup fails. Tokens carry `project_id` and `user_id` in Hydra `ext` claims.
- **Hydra admin client:** `internal/admin/hydra_client.go` — `HydraClient` struct with login/consent/introspection helpers.
- **Existing OAuth public endpoints on admin server:** `GET /oauth/login`, `GET|POST /oauth/consent` mounted at `internal/admin/routes.go:42-44`.
- **Existing public client (Claude Code):** Hardcoded ID `9d1c250a-e61b-44d9-88ed-5944d1962f5e` in `internal/proxy/claudecode_oauth.go:26`, scopes `user:profile user:inference user:sessions:claude_code user:mcp_servers user:file_upload`.
- **Hydra bootstrap:** `docker-compose.yml` starts `hydra:v26.2.0` with `hydra-migrate` init container. There is no existing modelserver-side client registration script — Claude Code's client was manually registered with Hydra.
- **Deployment env vars:** `.env.hydra` holds `DSN`, `SECRETS_SYSTEM`, etc. Modelserver reads Hydra admin URL from `cfg.Auth.OAuth.Hydra.AdminURL` (viper key `auth.oauth.hydra.admin_url`).
- **Testing:** Standard Go `testing` package. Existing test file `internal/proxy/claudecode_oauth_test.go` uses `httptest.NewServer` to mock Hydra. Follow that pattern.

Design decision: mobile client scopes will be `user:inference offline_access` — the minimum set to call `/v1/messages` and obtain a refresh token. This reuses the existing login/consent flow where the user selects a project in the browser during authorization. `project_id` stamping into the token happens in the existing consent handler and requires **no changes**.

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `internal/admin/hydra_device.go` | `HydraClient` methods for device-flow-related admin operations (consent request handling for the device-grant flow). Mostly a small extension. |
| `internal/admin/device_flow_proxy.go` | Thin reverse-proxy handlers that expose Hydra's public device auth and token endpoints at stable modelserver URLs. |
| `internal/admin/device_flow_proxy_test.go` | Tests for the proxy (URL rewriting, method pass-through, error propagation). |
| `scripts/register-mobile-client.sh` | Idempotent shell script that registers the `agentserver-mobile-client` Hydra public client via the Hydra admin CLI. Run once per environment. |
| `deploy/hydra-clients.yaml` | Declarative client spec (YAML) that the script reads. Keeps the client list under source control. |

### Modified files

| File | Changes |
|------|---------|
| `cmd/modelserver/main.go` | Pass `cfg.Auth.OAuth.Hydra.PublicURL` into admin route mounting so the device-flow proxy handlers know where to forward. |
| `internal/config/config.go` | Add `Auth.OAuth.Hydra.PublicURL` string field (default: empty → feature disabled). |
| `config.example.yml` | Document the new `hydra.public_url` key. |
| `internal/admin/routes.go` | Register the new `/oauth/device/auth` and `/oauth/token` public endpoints (no JWT required). |
| `internal/proxy/auth_middleware.go` | **Possibly no change.** Current introspection already works for any Hydra-issued token. Verified during Task 5 end-to-end test; if a scope check is missing we add one there. |
| `docker-compose.yml` | Add a `hydra-client-mobile` one-shot service that runs `scripts/register-mobile-client.sh` after `hydra` comes up. Makes local dev self-bootstrapping. |
| `.env.hydra` / `.env.modelserver.example` | Document the new public URL env var `MODELSERVER_AUTH_OAUTH_HYDRA_PUBLIC_URL`. |
| `README.md` (Mobile Agent section) | Brief note that modelserver now supports device flow and lists the public client ID. |

---

## Task 0: Scope check against Hydra version

**Files:**
- Read-only: `docker-compose.yml`, Hydra release notes

- [ ] **Step 1: Confirm Hydra version supports device flow**

Run: `grep -n 'hydra:v' /root/coding/modelserver/docker-compose.yml`
Expected output: `hydra:v26.2.0` (or newer). Ory Hydra added device authorization grant in v2.0 (2022) and it is stable in v26.

- [ ] **Step 2: Confirm Hydra public URL is reachable from modelserver container**

Run (with compose up): `docker compose exec modelserver wget -qO- http://hydra:4444/health/ready`
Expected: `{"status":"ok"}`

If the Hydra public port is not exposed internally, open it in `docker-compose.yml` (`hydra` service, `expose: ["4444"]`). This task produces no commit if nothing changes; otherwise commit the compose adjustment.

---

## Task 1: Add `Auth.OAuth.Hydra.PublicURL` to config

**Files:**
- Modify: `internal/config/config.go`
- Modify: `config.example.yml`

- [ ] **Step 1: Read current Hydra config struct**

Run: `grep -n 'Hydra' /root/coding/modelserver/internal/config/config.go`
Identify the `HydraConfig` struct. Expected: a struct with at least `AdminURL string`.

- [ ] **Step 2: Add `PublicURL` field**

Edit `internal/config/config.go` — the Hydra config struct:

```go
type HydraConfig struct {
    AdminURL  string `mapstructure:"admin_url"`
    PublicURL string `mapstructure:"public_url"` // NEW: for device-flow reverse proxy
}
```

And wire it in the viper `BindEnv` section (follow existing pattern — search for `"auth.oauth.hydra.admin_url"` to find the line and add the sibling immediately below):

```go
_ = v.BindEnv("auth.oauth.hydra.public_url", "MODELSERVER_AUTH_OAUTH_HYDRA_PUBLIC_URL")
```

- [ ] **Step 3: Document in `config.example.yml`**

Edit the `auth.oauth.hydra` block:

```yaml
auth:
  oauth:
    hydra:
      admin_url: http://hydra:4445
      public_url: http://hydra:4444   # NEW: required for device authorization grant
```

- [ ] **Step 4: Run the existing config tests**

Run: `cd /root/coding/modelserver && go test ./internal/config/...`
Expected: all green. The new field is a no-op for callers that don't read it yet.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go config.example.yml
git commit -m "config: add auth.oauth.hydra.public_url for device flow proxy"
```

---

## Task 2: Device-flow reverse proxy handlers

**Files:**
- Create: `internal/admin/device_flow_proxy.go`
- Create: `internal/admin/device_flow_proxy_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/admin/device_flow_proxy_test.go`:

```go
package admin

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestDeviceFlowProxy_ForwardsToHydraDeviceAuth(t *testing.T) {
	// Fake Hydra that records the incoming request.
	var gotMethod, gotPath, gotBody string
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotBody = string(body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"device_code":"abc","user_code":"ZZZZ","verification_uri":"http://hydra/verify","verification_uri_complete":"http://hydra/verify?user_code=ZZZZ","expires_in":1800,"interval":5}`))
	}))
	defer hydra.Close()

	u, _ := url.Parse(hydra.URL)
	h := NewDeviceFlowProxy(u)

	rec := httptest.NewRecorder()
	body := strings.NewReader("client_id=agentserver-mobile-client&scope=user:inference+offline_access")
	req := httptest.NewRequest("POST", "/oauth/device/auth", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.HandleDeviceAuth(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotMethod != "POST" {
		t.Errorf("upstream method = %s, want POST", gotMethod)
	}
	if gotPath != "/oauth2/device/auth" {
		t.Errorf("upstream path = %s, want /oauth2/device/auth", gotPath)
	}
	if !strings.Contains(gotBody, "agentserver-mobile-client") {
		t.Errorf("upstream body missing client_id: %q", gotBody)
	}
	if !strings.Contains(rec.Body.String(), `"device_code":"abc"`) {
		t.Errorf("response body missing device_code: %s", rec.Body.String())
	}
}

func TestDeviceFlowProxy_ForwardsToHydraToken(t *testing.T) {
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth2/token" {
			t.Errorf("path = %s, want /oauth2/token", r.URL.Path)
		}
		w.WriteHeader(http.StatusBadRequest) // authorization_pending
		_, _ = w.Write([]byte(`{"error":"authorization_pending"}`))
	}))
	defer hydra.Close()

	u, _ := url.Parse(hydra.URL)
	h := NewDeviceFlowProxy(u)

	rec := httptest.NewRecorder()
	body := strings.NewReader("grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=abc&client_id=agentserver-mobile-client")
	req := httptest.NewRequest("POST", "/oauth/token", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	h.HandleToken(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "authorization_pending") {
		t.Errorf("body = %s", rec.Body.String())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /root/coding/modelserver && go test ./internal/admin/ -run TestDeviceFlowProxy -v`
Expected: FAIL — `undefined: NewDeviceFlowProxy`.

- [ ] **Step 3: Write the implementation**

Create `internal/admin/device_flow_proxy.go`:

```go
package admin

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// DeviceFlowProxy exposes Hydra's public device authorization endpoints
// through modelserver so the mobile client sees a single base URL.
//
// All requests are forwarded unchanged to Hydra; status code, headers,
// and body are streamed back. Hydra enforces all policy (client auth,
// scope validation, polling rate limits).
type DeviceFlowProxy struct {
	hydraPublic *url.URL
	proxy       *httputil.ReverseProxy
}

// NewDeviceFlowProxy builds a proxy rooted at the Hydra public URL.
func NewDeviceFlowProxy(hydraPublic *url.URL) *DeviceFlowProxy {
	h := &DeviceFlowProxy{hydraPublic: hydraPublic}
	h.proxy = &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			// Path is set by the calling handler before each request.
			req.URL.Scheme = hydraPublic.Scheme
			req.URL.Host = hydraPublic.Host
			req.Host = hydraPublic.Host
			// Strip any mount-path prefix — we overwrite Path below.
		},
	}
	return h
}

// HandleDeviceAuth proxies POST /oauth/device/auth → Hydra /oauth2/device/auth.
func (h *DeviceFlowProxy) HandleDeviceAuth(w http.ResponseWriter, r *http.Request) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/oauth2/device/auth"
	h.proxy.ServeHTTP(w, r2)
}

// HandleToken proxies POST /oauth/token → Hydra /oauth2/token.
// Used for both device_code and refresh_token grants.
func (h *DeviceFlowProxy) HandleToken(w http.ResponseWriter, r *http.Request) {
	r2 := r.Clone(r.Context())
	r2.URL.Path = "/oauth2/token"
	h.proxy.ServeHTTP(w, r2)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /root/coding/modelserver && go test ./internal/admin/ -run TestDeviceFlowProxy -v`
Expected: PASS, both subtests.

- [ ] **Step 5: Commit**

```bash
git add internal/admin/device_flow_proxy.go internal/admin/device_flow_proxy_test.go
git commit -m "admin: add device flow reverse proxy to Hydra public endpoints"
```

---

## Task 3: Wire device-flow endpoints into admin router

**Files:**
- Modify: `internal/admin/routes.go`
- Modify: `cmd/modelserver/main.go`

- [ ] **Step 1: Read existing mount code**

Run: `grep -n 'oauth/login\|oauth/consent' /root/coding/modelserver/internal/admin/routes.go`
Identify where the public `/oauth/login` and `/oauth/consent` handlers are registered (around line 42).

- [ ] **Step 2: Update `MountRoutes` signature to accept Hydra public URL**

Edit `internal/admin/routes.go`, change the function signature to include `hydraPublicURL string`:

```go
func MountRoutes(
    r chi.Router, st *store.Store, cfg *config.Config, encKey []byte,
    jwtMgr *auth.JWTManager,
) {
```

becomes (keep existing behaviour if empty string):

```go
func MountRoutes(
    r chi.Router, st *store.Store, cfg *config.Config, encKey []byte,
    jwtMgr *auth.JWTManager,
) {
    // ... existing hydraClient setup ...

    // Mount device flow reverse proxy (public — no JWT auth required).
    if cfg.Auth.OAuth.Hydra.PublicURL != "" {
        pub, err := url.Parse(cfg.Auth.OAuth.Hydra.PublicURL)
        if err != nil {
            panic("admin: invalid hydra public_url: " + err.Error())
        }
        dfp := NewDeviceFlowProxy(pub)
        r.Post("/oauth/device/auth", dfp.HandleDeviceAuth)
        r.Post("/oauth/token", dfp.HandleToken)
    }
    // ... existing /oauth/login, /oauth/consent registration follows ...
}
```

Add `"net/url"` to the imports at the top of the file.

- [ ] **Step 3: Verify callers still compile**

Run: `cd /root/coding/modelserver && go build ./...`
Expected: clean build. `cmd/modelserver/main.go` already passes `cfg`, so no caller change is required.

- [ ] **Step 4: Write the failing integration test**

Create `internal/admin/routes_device_flow_test.go`:

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestMountRoutes_DeviceFlowProxyRegistered(t *testing.T) {
	// Fake Hydra: respond 200 to /oauth2/device/auth, 400 to /oauth2/token.
	hydra := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth2/device/auth":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_code":"dc_1","user_code":"ABCD","expires_in":1800,"interval":5,"verification_uri":"http://hydra/verify","verification_uri_complete":"http://hydra/verify?user_code=ABCD"}`))
		case "/oauth2/token":
			http.Error(w, `{"error":"authorization_pending"}`, http.StatusBadRequest)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer hydra.Close()

	pub, _ := url.Parse(hydra.URL)
	r := chi.NewRouter()
	// Mount only the device flow piece for this test.
	dfp := NewDeviceFlowProxy(pub)
	r.Post("/oauth/device/auth", dfp.HandleDeviceAuth)
	r.Post("/oauth/token", dfp.HandleToken)

	srv := httptest.NewServer(r)
	defer srv.Close()

	// Call /oauth/device/auth.
	resp, err := http.Post(srv.URL+"/oauth/device/auth",
		"application/x-www-form-urlencoded",
		strings.NewReader("client_id=agentserver-mobile-client&scope=user:inference+offline_access"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Errorf("device/auth status = %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Call /oauth/token — expect 400 (authorization_pending).
	resp, err = http.Post(srv.URL+"/oauth/token",
		"application/x-www-form-urlencoded",
		strings.NewReader("grant_type=urn:ietf:params:oauth:grant-type:device_code&device_code=dc_1&client_id=agentserver-mobile-client"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Errorf("token status = %d, want 400", resp.StatusCode)
	}
	resp.Body.Close()
}
```

- [ ] **Step 5: Run the new test**

Run: `cd /root/coding/modelserver && go test ./internal/admin/ -run TestMountRoutes_DeviceFlowProxyRegistered -v`
Expected: PASS.

- [ ] **Step 6: Run full admin test suite**

Run: `cd /root/coding/modelserver && go test ./internal/admin/...`
Expected: all green, no regressions.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/routes.go internal/admin/routes_device_flow_test.go
git commit -m "admin: register /oauth/device/auth and /oauth/token device flow endpoints"
```

---

## Task 4: Declarative Hydra client spec + registration script

**Files:**
- Create: `deploy/hydra-clients.yaml`
- Create: `scripts/register-mobile-client.sh`

- [ ] **Step 1: Author the client spec**

Create `deploy/hydra-clients.yaml`:

```yaml
# Hydra OAuth2 clients registered by scripts/register-mobile-client.sh.
# Keep this file as the authoritative record of production/dev client IDs.
clients:
  - client_id: agentserver-mobile-client
    client_name: Agentserver Mobile Agent
    token_endpoint_auth_method: none        # public client
    grant_types:
      - urn:ietf:params:oauth:grant-type:device_code
      - refresh_token
    response_types: []                      # device flow has no redirect-based response
    scope: "user:inference offline_access"
    audience: []
    # Optional metadata used by modelserver for UI (not enforced by Hydra).
    metadata:
      platform: mobile
      owner: agentserver-team
```

- [ ] **Step 2: Write the idempotent registration script**

Create `scripts/register-mobile-client.sh` with execute permission (`chmod +x`):

```bash
#!/usr/bin/env sh
# Idempotently register OAuth clients from deploy/hydra-clients.yaml.
# Requires: hydra CLI in PATH, HYDRA_ADMIN_URL env var.
#
# Usage:
#   HYDRA_ADMIN_URL=http://localhost:4445 ./scripts/register-mobile-client.sh
set -eu

: "${HYDRA_ADMIN_URL:?HYDRA_ADMIN_URL must be set}"

wait_for_hydra() {
    i=0
    until wget -qO- "${HYDRA_ADMIN_URL}/health/ready" 2>/dev/null | grep -q ok; do
        i=$((i + 1))
        if [ "$i" -gt 60 ]; then
            echo "timed out waiting for hydra admin at ${HYDRA_ADMIN_URL}" >&2
            exit 1
        fi
        sleep 2
    done
}

register_mobile_client() {
    FLAGS="--name Agentserver_Mobile_Agent \
        --grant-type urn:ietf:params:oauth:grant-type:device_code \
        --grant-type refresh_token \
        --scope user:inference --scope offline_access \
        --token-endpoint-auth-method none"

    # Try update; fall back to create. Both paths are idempotent.
    if hydra update oauth2-client agentserver-mobile-client \
        --endpoint "${HYDRA_ADMIN_URL}" $FLAGS >/dev/null 2>&1; then
        echo "updated oauth2-client: agentserver-mobile-client"
    else
        hydra create oauth2-client \
            --endpoint "${HYDRA_ADMIN_URL}" \
            --id agentserver-mobile-client \
            $FLAGS
        echo "created oauth2-client: agentserver-mobile-client"
    fi
}

wait_for_hydra
register_mobile_client
```

- [ ] **Step 3: Smoke-test against a local Hydra**

Run (with docker-compose up): `HYDRA_ADMIN_URL=http://localhost:4445 ./scripts/register-mobile-client.sh`
Expected output: `created oauth2-client: agentserver-mobile-client` on first run, `updated …` on subsequent runs.

- [ ] **Step 4: Verify the client exists**

Run: `hydra get oauth2-client agentserver-mobile-client --endpoint http://localhost:4445 --format json | jq '.client_id, .grant_types, .scope'`
Expected:
```
"agentserver-mobile-client"
[
  "urn:ietf:params:oauth:grant-type:device_code",
  "refresh_token"
]
"user:inference offline_access"
```

- [ ] **Step 5: Commit**

```bash
git add deploy/hydra-clients.yaml scripts/register-mobile-client.sh
git commit -m "deploy: add Hydra mobile client registration script"
```

---

## Task 5: docker-compose auto-registration service

**Files:**
- Modify: `docker-compose.yml`

- [ ] **Step 1: Read the existing `hydra-migrate` service**

Run: `grep -n 'hydra' /root/coding/modelserver/docker-compose.yml`
Find the `hydra-migrate` service block (the init container for Hydra schema).

- [ ] **Step 2: Append the new one-shot service**

Edit `docker-compose.yml`, add below the `hydra-migrate` service:

```yaml
  hydra-client-mobile:
    image: oryd/hydra:v26.2.0
    depends_on:
      hydra:
        condition: service_started
    environment:
      HYDRA_ADMIN_URL: http://hydra:4445
    entrypoint: ["/bin/sh", "-c"]
    command: ["/scripts/register-mobile-client.sh"]
    volumes:
      - ./scripts:/scripts:ro
    restart: on-failure
```

- [ ] **Step 3: Bring up the stack and verify**

Run:
```bash
cd /root/coding/modelserver
docker compose down
docker compose up -d hydra hydra-migrate hydra-client-mobile
docker compose logs hydra-client-mobile
```

Expected log line: `created oauth2-client: agentserver-mobile-client` (first run) or `updated …` (reruns). Exit code 0.

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml
git commit -m "compose: auto-register mobile OAuth client after hydra start"
```

---

## Task 6: End-to-end integration test — full device flow

**Files:**
- Create: `internal/admin/device_flow_e2e_test.go`

The goal of this test: validate that a real Hydra instance accepts the mobile client, issues tokens, and those tokens work against `/v1/messages` introspection.

- [ ] **Step 1: Write the test**

Create `internal/admin/device_flow_e2e_test.go`:

```go
//go:build e2e
// +build e2e

// Requires HYDRA_ADMIN_URL, HYDRA_PUBLIC_URL, and a running Hydra with the
// mobile client registered (see scripts/register-mobile-client.sh).
//
// Run: go test -tags=e2e ./internal/admin/ -run TestDeviceFlow_EndToEnd -v

package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDeviceFlow_EndToEnd(t *testing.T) {
	pub := os.Getenv("HYDRA_PUBLIC_URL")
	if pub == "" {
		t.Skip("HYDRA_PUBLIC_URL not set; skipping e2e")
	}

	// 1. Initiate device authorization.
	authResp, err := http.Post(pub+"/oauth2/device/auth",
		"application/x-www-form-urlencoded",
		strings.NewReader("client_id=agentserver-mobile-client&scope=user:inference+offline_access"))
	if err != nil {
		t.Fatal(err)
	}
	if authResp.StatusCode != 200 {
		body, _ := io.ReadAll(authResp.Body)
		t.Fatalf("device/auth: %d %s", authResp.StatusCode, body)
	}
	var ar struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		Interval                int    `json:"interval"`
		ExpiresIn               int    `json:"expires_in"`
	}
	if err := json.NewDecoder(authResp.Body).Decode(&ar); err != nil {
		t.Fatal(err)
	}
	authResp.Body.Close()

	if ar.DeviceCode == "" || ar.UserCode == "" {
		t.Fatalf("empty device_code or user_code: %+v", ar)
	}
	t.Logf("Open this URL in a browser to authorize: %s", ar.VerificationURIComplete)

	// 2. Poll the token endpoint. In e2e the tester must manually approve
	//    the verification URL within EXPIRES_IN seconds. Cap polling at 2 min.
	deadline := time.Now().Add(2 * time.Minute)
	var accessToken, refreshToken string
	for time.Now().Before(deadline) {
		form := url.Values{
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {ar.DeviceCode},
			"client_id":   {"agentserver-mobile-client"},
		}
		resp, err := http.PostForm(pub+"/oauth2/token", form)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode == 200 {
			var tr struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				TokenType    string `json:"token_type"`
			}
			if err := json.Unmarshal(body, &tr); err != nil {
				t.Fatal(err)
			}
			accessToken = tr.AccessToken
			refreshToken = tr.RefreshToken
			break
		}
		// Expect authorization_pending until user approves.
		if !strings.Contains(string(body), "authorization_pending") &&
			!strings.Contains(string(body), "slow_down") {
			t.Fatalf("token poll: %d %s", resp.StatusCode, body)
		}
		time.Sleep(time.Duration(ar.Interval) * time.Second)
	}
	if accessToken == "" {
		t.Fatal("no access token obtained within deadline")
	}
	t.Logf("access token obtained (len=%d), refresh token obtained (len=%d)",
		len(accessToken), len(refreshToken))
}
```

- [ ] **Step 2: Run the e2e test (manual approval required)**

Run (with docker-compose up):
```bash
HYDRA_PUBLIC_URL=http://localhost:4444 \
  go test -tags=e2e ./internal/admin/ -run TestDeviceFlow_EndToEnd -v -timeout 5m
```
Open the URL printed by the test in a browser, log in with a test user, pick a project, approve. Expected: final log line `access token obtained (len=…), refresh token obtained (len=…)`, test PASSES.

- [ ] **Step 3: Sanity-check the issued token against introspection**

This is a single command rather than an assertion because the exact introspection endpoint depends on the admin URL. Paste the token into:

```bash
curl -s -u ':' -X POST http://localhost:4445/admin/oauth2/introspect \
  -d "token=<paste access_token>" | jq
```

Expected: `"active": true`, `"client_id": "agentserver-mobile-client"`, `"scope": "user:inference offline_access"`, `"ext"` map contains `project_id` and `user_id`.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/device_flow_e2e_test.go
git commit -m "test: add e2e device flow test (behind e2e build tag)"
```

---

## Task 7: Confirm `/v1/messages` accepts device-flow tokens

The existing auth middleware (`internal/proxy/auth_middleware.go:286-405`) already handles OAuth introspection and extracts `project_id`/`user_id` from `ext` claims. We need to verify it does not reject the mobile client.

**Files:**
- Read-only: `internal/proxy/auth_middleware.go`
- Modify (only if a gap is found): same file

- [ ] **Step 1: Audit scope enforcement in `handleTokenIntrospectionAuth`**

Run: `grep -n 'scope\|Scope' /root/coding/modelserver/internal/proxy/auth_middleware.go`
Read the introspection path (≈ lines 286–405).

Decision branches:
1. **If the code only checks `active == true` and extracts `project_id`/`user_id`:** no change needed. Document this in the next step as the expected state.
2. **If the code requires a specific scope (e.g. `user:inference`):** the mobile client already requests `user:inference`, so still no change, but note the requirement in the mobile client spec.
3. **If the code requires a scope the mobile client does not have:** add `user:inference` to the mobile client's scopes in `scripts/register-mobile-client.sh` and rerun the registration.

Produce a one-line summary in the commit body.

- [ ] **Step 2: Write a unit test covering the mobile client path**

Extend `internal/proxy/auth_middleware_test.go` (or create if missing) with:

```go
func TestHandleTokenIntrospectionAuth_MobileClient(t *testing.T) {
    // Fake Hydra introspection responding with a minimal active mobile token.
    introspect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        _, _ = w.Write([]byte(`{
            "active": true,
            "client_id": "agentserver-mobile-client",
            "scope": "user:inference offline_access",
            "sub": "user-42",
            "ext": {"project_id": "proj-123", "user_id": "user-42"}
        }`))
    }))
    defer introspect.Close()

    // Build middleware with a store that knows about proj-123.
    // ... follow the fixture pattern already in auth_middleware_test.go ...

    // Call the /v1/messages endpoint with Authorization: Bearer fake-mobile-token.
    // Assert: status != 401 and context carries the expected apiKey/project.
}
```

If no existing `auth_middleware_test.go` exists in the proxy package, model the test after the introspector interface. You may need to wire a `TokenIntrospector` mock.

- [ ] **Step 3: Run proxy package tests**

Run: `cd /root/coding/modelserver && go test ./internal/proxy/...`
Expected: green.

- [ ] **Step 4: Commit**

```bash
git add internal/proxy/auth_middleware_test.go
git commit -m "test: verify /v1/messages accepts mobile client introspection tokens"
```

---

## Task 8: Environment variable docs + example file

**Files:**
- Modify: `.env.modelserver.example` (create if absent)
- Modify: `README.md`

- [ ] **Step 1: Document the new env var**

If `.env.modelserver.example` exists, append:

```
# Hydra public URL used by the device authorization grant reverse proxy.
# Required to enable the mobile agent device flow.
MODELSERVER_AUTH_OAUTH_HYDRA_PUBLIC_URL=http://hydra:4444
```

If it does not exist, create it with that block.

- [ ] **Step 2: Add a Mobile Agent section to README**

Insert (or update) a `## OAuth Clients` section in `README.md`:

```markdown
## OAuth Clients

Modelserver delegates OAuth to Ory Hydra. Registered public clients:

| Client ID | Purpose | Grants | Scopes |
|-----------|---------|--------|--------|
| `9d1c250a-e61b-44d9-88ed-5944d1962f5e` | Claude Code desktop | PKCE code | `user:profile user:inference …` |
| `agentserver-mobile-client` | Flutter mobile agent | Device code + refresh | `user:inference offline_access` |

The mobile client exposes Hydra's device authorization grant at the
modelserver public URL:

- `POST /oauth/device/auth` → Hydra `/oauth2/device/auth`
- `POST /oauth/token`       → Hydra `/oauth2/token`

After approval the issued access token is accepted by `POST /v1/messages`
without any additional configuration.
```

- [ ] **Step 3: Commit**

```bash
git add .env.modelserver.example README.md
git commit -m "docs: document mobile OAuth client and device flow endpoints"
```

---

## Self-Review Checklist

- [ ] **Spec coverage**
  - RFC 8628 device authorization at modelserver base URL — Tasks 2, 3, 4
  - Device code grant on token endpoint — Task 3 (Hydra does the grant; proxy passes through)
  - Refresh token support — Included in client registration (Task 4) + proxy (Task 3) — verified by e2e (Task 6)
  - Public client `agentserver-mobile-client` with `user:inference offline_access` scopes — Task 4
  - `/v1/messages` accepts end-user bearer tokens from this client — Task 7
- [ ] **Placeholder scan** — no TBDs, no "handle errors", no vague references.
- [ ] **Type consistency** — `NewDeviceFlowProxy(*url.URL)` used identically in Task 2/3; `HandleDeviceAuth`/`HandleToken` method names consistent.
- [ ] **External coordination** — None beyond this repo. Hydra is already deployed; new env var has a safe default (empty = feature off).

---

## Done criteria

1. `go test ./...` green.
2. `go test -tags=e2e ./internal/admin/ -run TestDeviceFlow_EndToEnd` completes with a valid access token after manual browser approval.
3. `docker compose up` on a clean checkout registers the mobile client automatically without manual steps.
4. `curl -X POST http://localhost:8080/v1/messages -H "Authorization: Bearer <device-flow-token>" …` returns a real inference response (or the expected model-provider error, not 401).

Once those are green, P2 (Flutter core auth) is unblocked.
