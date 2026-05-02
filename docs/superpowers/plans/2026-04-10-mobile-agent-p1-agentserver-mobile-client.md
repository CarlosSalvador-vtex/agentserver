# Mobile Agent — P1: agentserver Mobile Hydra Client + `mobile` Sandbox Type Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Teach agentserver that mobile agents exist. This plan adds a new public OAuth client `agentserver-agent-mobile` to the Helm-managed Hydra deployment, and extends `/api/agent/register` so it accepts `type="mobile"` as a valid sandbox type peer to `opencode` and `claudecode`. After this plan is shipped, the Flutter app will be able to complete the agentserver device flow and register itself as an agent card — nothing else on the agentserver side needs to change for that flow to work.

**Architecture:** Two small surface-area changes with no schema migration: (1) Helm chart one-shot Hydra client-registration Job grows a second client definition. (2) `handleAgentRegister` in `internal/server/agent_register.go` gets a normalized type-validation helper that accepts `mobile` and leaves `opencode_token` empty for that case. The existing `CreateLocalSandbox` helper already handles an empty opencode token (stored as `""`, never compared), so there is no DB-layer change. Because `sandboxes.type` is a plain `TEXT` column with no CHECK constraint, no migration is required.

**Tech Stack:** Go 1.26, chi v5, Ory Hydra v2.x, Helm, PostgreSQL (untouched here).

**Repository:** `/root/agentserver`

**Depends on:** None. This plan is a prerequisite for P4 (Flutter core auth) and P3 (mobile tunnel).

**Consumed by:**
- **P3** (mobile tunnel) assumes mobile sandboxes exist with `tunnel_token` set — this plan is what creates them.
- **P4** (Flutter core auth) calls `POST /api/oauth2/device/auth` with `client_id=agentserver-agent-mobile`, then `POST /api/agent/register` with `type="mobile"`. Both requirements land here.

---

## Background & Key Facts

From codebase investigation:

- **Hydra client registration** is a single Kubernetes Job defined in `deploy/helm/agentserver/templates/hydra.yaml` (lines 120-168). The Job runs once per release and registers exactly one client `agentserver-agent-cli` via `hydra update ... || hydra create ...`. There is no declarative YAML file for clients — they're imperative shell commands in the Job manifest.

- **Existing CLI client flags** (lines 160-166 of `deploy/helm/agentserver/templates/hydra.yaml`):
  ```
  --name Agentserver_Agent_CLI
  --grant-type urn:ietf:params:oauth:grant-type:device_code
  --grant-type refresh_token
  --response-type code
  --scope openid --scope profile --scope agent:register
  --token-endpoint-auth-method none
  --audience https://{{ .Values.platform.domain }}
  ```

- **`handleAgentRegister`** is at `internal/server/agent_register.go:15-118`. The relevant block for this plan is lines 76-83:
  ```go
  sandboxType := req.Type
  if sandboxType == "" {
      sandboxType = "opencode"
  }
  if sandboxType != "opencode" && sandboxType != "claudecode" {
      http.Error(w, "invalid type: must be opencode or claudecode", http.StatusBadRequest)
      return
  }
  ```
  Lines 85-107 generate `tunnelToken`, `proxyToken`, and an `opencodePassword` only when `sandboxType == "opencode"`, then call `s.DB.CreateLocalSandbox(...)`. The call signature is:
  ```go
  func (db *DB) CreateLocalSandbox(id, workspaceID, name, sandboxType, opencodeToken, proxyToken, tunnelToken, shortID string) error
  ```
  (see `internal/db/sandboxes.go:258-269`). Passing `""` for `opencodeToken` is safe — the INSERT stores the empty string, and no downstream code compares it to anything for mobile.

- **`sandboxes.type` column** is declared `TEXT NOT NULL DEFAULT 'opencode'` with no CHECK constraint (`internal/db/migrations/001_initial.sql:79`). Storing the value `"mobile"` requires no migration.

- **`opencode_token`** is only read by `internal/sandboxproxy/opencode_proxy.go:143` and `internal/sandboxproxy/tunnel.go:143`, each guarded by `if sbx.OpencodeToken != ""`. An empty value is a no-op there, which is exactly what we want for mobile.

- **Hydra introspection** uses the `HydraClient.IntrospectToken` method at `internal/auth/hydra.go`. The handler checks `HasScope("agent:register")`, so the mobile client must keep that scope.

- **Test conventions.** The `internal/server` package has no `*_test.go` files in the current tree. The closest template is `internal/auth/hydra_test.go` which uses stdlib `testing` + `net/http/httptest`. We will introduce the first `internal/server/*_test.go` file with a single narrow, pure-function test. We will NOT add an integration test that spins up PostgreSQL — that's out of scope and the repo doesn't have that infrastructure.

### Design decisions not in spec

**R1 (from review): `type="mobile"` is the accepted value on `/api/agent/register`.**
- Default display name when body omits `name`: `"Mobile Agent"` (parallel to the existing `"Local Agent"` default for other types).
- `opencode_token` is stored as `""` for mobile sandboxes.
- `tunnel_token` and `proxy_token` are still generated — mobile uses the tunnel endpoint added in P3, and the proxy token is used for `/api/agent/discovery/*`, `/api/agent/tasks/*`, and `/api/agent/mailbox/*` (same as CLI agents).
- No CHECK constraint is added to `sandboxes.type`; the Go handler is the only place that validates.
- The mobile Hydra client requests the additional scope `offline_access` (CLI does not, but the design spec's Flow 1 explicitly asks for it so token refresh works for long-lived mobile sessions).

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `internal/server/agent_register_helpers.go` | Pure helper function `normalizeSandboxType(t string) (string, error)` — the sole place that enumerates valid sandbox types. Kept in its own file so the unit test file can compile without pulling in the chi router and database dependencies. |
| `internal/server/agent_register_helpers_test.go` | Table-driven test for `normalizeSandboxType`. |

### Modified files

| File | Changes |
|------|---------|
| `deploy/helm/agentserver/templates/hydra.yaml` | Extend the `hydra-client-register` Job's shell script to register a second client `agentserver-agent-mobile` in addition to `agentserver-agent-cli`. |
| `internal/server/agent_register.go` | Replace the inline `if sandboxType == ""` + `if sandboxType != "opencode" && sandboxType != "claudecode"` block (lines 76-83) with a call to `normalizeSandboxType`. When the normalized type is `"mobile"`, skip `opencodePassword` generation (leave it empty). Also change the default `req.Name` fallback so mobile gets `"Mobile Agent"` rather than `"Local Agent"` (optional but user-visible). |

### Unchanged files

- `internal/db/sandboxes.go` — `CreateLocalSandbox` already handles an empty `opencodeToken`.
- `internal/db/migrations/*` — no migration needed (no CHECK constraint on `sandboxes.type`).
- `internal/server/server.go` — the `/api/agent/register` route is already registered at line 169.
- `internal/auth/hydra.go` — the introspection logic is unchanged; the new client presents a valid token the same way `agentserver-agent-cli` does.

---

## Task 0: Sanity check — confirm no CHECK constraint on `sandboxes.type`

**Files:**
- Read-only: `internal/db/migrations/001_initial.sql`, all migrations under `internal/db/migrations/`

- [ ] **Step 1: Verify there is no CHECK constraint anywhere that restricts `sandboxes.type`**

Run: `grep -n "sandboxes.*type\|CHECK (type" /root/agentserver/internal/db/migrations/*.sql`
Expected: only line `001_initial.sql:79:    type               TEXT NOT NULL DEFAULT 'opencode',` appears. No CHECK clause, no trigger.

If any migration adds a CHECK constraint, STOP and add a new migration file `016_sandbox_type_allow_mobile.sql` at the end of Task 1 that drops/recreates the constraint. Otherwise proceed.

- [ ] **Step 2: Verify `opencode_token` is read behind a non-empty guard**

Run: `grep -n "OpencodeToken" /root/agentserver/internal/sandboxproxy/tunnel.go /root/agentserver/internal/sandboxproxy/opencode_proxy.go`
Expected: each hit is preceded by `if sbx.OpencodeToken != ""`. If any code path dereferences `OpencodeToken` without the guard, file a separate issue — do not try to fix it here.

No commit for this task.

---

## Task 1: Introduce `normalizeSandboxType` helper and its test

**Files:**
- Create: `internal/server/agent_register_helpers.go`
- Create: `internal/server/agent_register_helpers_test.go`

- [ ] **Step 1: Write the failing test first**

Create file `internal/server/agent_register_helpers_test.go` with exactly this content:

```go
package server

import (
	"testing"
)

func TestNormalizeSandboxType(t *testing.T) {
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"", "opencode", false},
		{"opencode", "opencode", false},
		{"claudecode", "claudecode", false},
		{"mobile", "mobile", false},
		{"OPENCODE", "", true},       // case-sensitive by design
		{"mobile ", "", true},        // no trim: caller must trim
		{"android", "", true},        // ios/android are not sandbox types
		{"bogus", "", true},
	}
	for _, c := range cases {
		got, err := normalizeSandboxType(c.input)
		if c.wantErr {
			if err == nil {
				t.Errorf("normalizeSandboxType(%q) err = nil, want error", c.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("normalizeSandboxType(%q) err = %v", c.input, err)
			continue
		}
		if got != c.want {
			t.Errorf("normalizeSandboxType(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test and verify it fails with a compile error**

Run: `cd /root/agentserver && go test ./internal/server/ -run TestNormalizeSandboxType -v`
Expected: FAIL with `undefined: normalizeSandboxType`.

- [ ] **Step 3: Write the minimal implementation**

Create file `internal/server/agent_register_helpers.go` with exactly this content:

```go
package server

import "fmt"

// normalizeSandboxType validates and normalizes the sandbox type from the
// agent-register request body. An empty string defaults to "opencode" for
// backward compatibility with the original CLI flow. Any other value is
// either a known type ("opencode", "claudecode", "mobile") or an error.
//
// This is the only place that enumerates valid sandbox types, so that new
// types can be added by touching exactly one function and one test file.
func normalizeSandboxType(t string) (string, error) {
	switch t {
	case "", "opencode":
		return "opencode", nil
	case "claudecode":
		return "claudecode", nil
	case "mobile":
		return "mobile", nil
	default:
		return "", fmt.Errorf("invalid type: must be opencode, claudecode, or mobile")
	}
}
```

- [ ] **Step 4: Run the test and verify it passes**

Run: `cd /root/agentserver && go test ./internal/server/ -run TestNormalizeSandboxType -v`
Expected: PASS, all 8 cases green.

- [ ] **Step 5: Verify the package still builds**

Run: `cd /root/agentserver && go build ./internal/server/...`
Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && git add internal/server/agent_register_helpers.go internal/server/agent_register_helpers_test.go
git commit -m "server: add normalizeSandboxType helper with unit test"
```

---

## Task 2: Wire `normalizeSandboxType` into `handleAgentRegister` and accept `mobile`

**Files:**
- Modify: `internal/server/agent_register.go` (lines 64-107)

- [ ] **Step 1: Read the current block to confirm line numbers**

Run: `grep -n "sandboxType\|opencodePassword\|req\.Name" /root/agentserver/internal/server/agent_register.go`
Expected output includes:
```
66:		Name string `json:"name"`
67:		Type string `json:"type"`
73:	if req.Name == "" {
74:		req.Name = "Local Agent"
75:	}
76:	sandboxType := req.Type
77:	if sandboxType == "" {
78:		sandboxType = "opencode"
79:	}
80:	if sandboxType != "opencode" && sandboxType != "claudecode" {
81:		http.Error(w, "invalid type: must be opencode or claudecode", http.StatusBadRequest)
82:		return
83:	}
...
89:	var opencodePassword string
90:	if sandboxType == "opencode" {
91:		opencodePassword = generatePassword()
92:	}
```

- [ ] **Step 2: Replace lines 73-83 with the helper call and a mobile-aware default name**

Edit `internal/server/agent_register.go` — replace this block:

```go
	if req.Name == "" {
		req.Name = "Local Agent"
	}
	sandboxType := req.Type
	if sandboxType == "" {
		sandboxType = "opencode"
	}
	if sandboxType != "opencode" && sandboxType != "claudecode" {
		http.Error(w, "invalid type: must be opencode or claudecode", http.StatusBadRequest)
		return
	}
```

with:

```go
	sandboxType, err := normalizeSandboxType(req.Type)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Name == "" {
		if sandboxType == "mobile" {
			req.Name = "Mobile Agent"
		} else {
			req.Name = "Local Agent"
		}
	}
```

Note: the existing handler already has an `err` variable declared at line 29 (`introspection, err := s.HydraClient.IntrospectToken(token)`). The new `err` here shadows it intentionally within the local block — Go allows this because the entire `err` assignment on the line `sandboxType, err := ...` is a `:=` short declaration with `sandboxType` being new. If `go vet` complains about shadowing, change `:=` to `=` and declare `var sandboxType string` on the line above.

- [ ] **Step 3: Verify the file still compiles**

Run: `cd /root/agentserver && go build ./internal/server/...`
Expected: no output (success). If there is a shadow warning or any error, fix it by hoisting the variable declaration as described above.

- [ ] **Step 4: Run the existing test plus the normalize test**

Run: `cd /root/agentserver && go test ./internal/server/ -run TestNormalize -v`
Expected: PASS.

- [ ] **Step 5: Run `go vet` on the changed package**

Run: `cd /root/agentserver && go vet ./internal/server/...`
Expected: no output (success).

- [ ] **Step 6: Commit**

```bash
cd /root/agentserver && git add internal/server/agent_register.go
git commit -m "server: accept type=mobile in agent registration"
```

---

## Task 3: Verify opencodePassword is not generated for mobile sandboxes

**Files:**
- Read-only: `internal/server/agent_register.go` (the block around lines 85-107 after Task 2's edit)

- [ ] **Step 1: Confirm the existing branch is still correct**

Run: `grep -A3 "var opencodePassword" /root/agentserver/internal/server/agent_register.go`
Expected:
```
	var opencodePassword string
	if sandboxType == "opencode" {
		opencodePassword = generatePassword()
	}
```

This is exactly the behaviour we want: for both `claudecode` and `mobile`, `opencodePassword` stays `""`, which means `CreateLocalSandbox` receives an empty string for the `opencodeToken` parameter. No change needed.

- [ ] **Step 2: Confirm downstream code guards against empty opencode token**

Run: `grep -B1 -A2 "OpencodeToken !=" /root/agentserver/internal/sandboxproxy/*.go`
Expected output: two hits, each wrapped in `if sbx.OpencodeToken != ""`. Both in:
- `internal/sandboxproxy/opencode_proxy.go:143`
- `internal/sandboxproxy/tunnel.go:143`

No commit for this task — it's a verification step only.

---

## Task 4: Register `agentserver-agent-mobile` in the Helm Hydra client Job

**Files:**
- Modify: `deploy/helm/agentserver/templates/hydra.yaml` (the `register-client` container, lines 152-168)

- [ ] **Step 1: Read the current `register-client` container block**

Run: `sed -n '152,170p' /root/agentserver/deploy/helm/agentserver/templates/hydra.yaml`
Expected: a shell script that computes `CLIENT_FLAGS` for `agentserver-agent-cli` and runs an update-or-create one-liner.

- [ ] **Step 2: Rewrite the container args to register both clients idempotently**

Edit `deploy/helm/agentserver/templates/hydra.yaml` — replace the `containers:` block (starting at line 152) with:

```yaml
      containers:
        - name: register-client
          image: {{ .Values.hydra.image.repository }}:{{ .Values.hydra.image.tag }}
          imagePullPolicy: {{ .Values.hydra.image.pullPolicy }}
          command: ["sh", "-c"]
          args:
            - |
              ENDPOINT="http://{{ .Release.Name }}-hydra-admin:{{ .Values.hydra.adminPort }}"

              # --- CLI agent (existing) ---
              CLI_FLAGS="--name Agentserver_Agent_CLI \
                --grant-type urn:ietf:params:oauth:grant-type:device_code \
                --grant-type refresh_token \
                --response-type code \
                --scope openid --scope profile --scope agent:register \
                --token-endpoint-auth-method none \
                --audience https://{{ .Values.platform.domain }}"
              hydra update oauth2-client agentserver-agent-cli --endpoint "$ENDPOINT" $CLI_FLAGS 2>/dev/null || \
              hydra create oauth2-client --endpoint "$ENDPOINT" --id agentserver-agent-cli $CLI_FLAGS

              # --- Mobile agent (new — P1 of mobile agent plan) ---
              MOBILE_FLAGS="--name Agentserver_Agent_Mobile \
                --grant-type urn:ietf:params:oauth:grant-type:device_code \
                --grant-type refresh_token \
                --response-type code \
                --scope openid --scope profile --scope agent:register --scope offline_access \
                --token-endpoint-auth-method none \
                --audience https://{{ .Values.platform.domain }}"
              hydra update oauth2-client agentserver-agent-mobile --endpoint "$ENDPOINT" $MOBILE_FLAGS 2>/dev/null || \
              hydra create oauth2-client --endpoint "$ENDPOINT" --id agentserver-agent-mobile $MOBILE_FLAGS
```

- [ ] **Step 3: Verify the template still renders**

Run: `helm template /root/agentserver/deploy/helm/agentserver --set platform.domain=example.com 2>&1 | grep -A30 'register-client' | head -60`
Expected: both `hydra update/create oauth2-client agentserver-agent-cli` and `hydra update/create oauth2-client agentserver-agent-mobile` lines appear in the rendered Job.

If `helm` is not installed in the work environment, fall back to:
`cd /root/agentserver/deploy/helm/agentserver && cat templates/hydra.yaml | grep -c 'agentserver-agent-mobile'`
Expected: `2` (one for update, one for create).

- [ ] **Step 4: Commit**

```bash
cd /root/agentserver && git add deploy/helm/agentserver/templates/hydra.yaml
git commit -m "helm: register agentserver-agent-mobile Hydra client"
```

---

## Task 5: Manual end-to-end verification

**Prerequisite:** A running cluster with this Helm release deployed, or a local compose stack that mounts the updated hydra.yaml Job.

**Files:**
- None to modify. This is a smoke test before marking the plan done.

- [ ] **Step 1: Confirm both Hydra clients exist**

Port-forward Hydra admin (`kubectl port-forward svc/<release>-hydra-admin 4445:4445`) and run:

```bash
curl -s http://localhost:4445/admin/clients | jq '.[].client_id' | sort
```

Expected output includes:
```
"agentserver-agent-cli"
"agentserver-agent-mobile"
```

- [ ] **Step 2: Request a device code as the mobile client**

```bash
curl -sS -X POST https://<agentserver-host>/api/oauth2/device/auth \
  -d 'client_id=agentserver-agent-mobile' \
  -d 'scope=openid profile agent:register offline_access' | jq
```

Expected: a JSON body containing `device_code`, `user_code`, `verification_uri`, `verification_uri_complete`, `interval`, `expires_in`.
Failure mode: if Hydra returns `{"error":"invalid_client"}`, the registration Job didn't run or didn't see the updated template — re-run it with `kubectl delete job -l <label>` or `helm upgrade`.

- [ ] **Step 3: Complete the flow in a browser and obtain a token**

Open `verification_uri_complete` in a browser, approve, then poll the token endpoint:

```bash
curl -sS -X POST https://<agentserver-host>/api/oauth2/token \
  -d 'client_id=agentserver-agent-mobile' \
  -d 'device_code=<device_code_from_step_2>' \
  -d 'grant_type=urn:ietf:params:oauth:grant-type:device_code' | jq
```

Expected: `{ "access_token": "...", "refresh_token": "...", "expires_in": 3600, "token_type": "bearer", "scope": "openid profile agent:register offline_access" }`. The presence of `refresh_token` proves `offline_access` was granted.

- [ ] **Step 4: Register a mobile sandbox**

```bash
curl -sS -X POST https://<agentserver-host>/api/agent/register \
  -H "Authorization: Bearer <access_token_from_step_3>" \
  -H "Content-Type: application/json" \
  -d '{"name":"iPhone-test","type":"mobile"}' | jq
```

Expected: HTTP 201 and a JSON response with `sandbox_id`, `tunnel_token`, `proxy_token`, `workspace_id`, `short_id`.

- [ ] **Step 5: Confirm the sandbox row looks right**

```bash
kubectl exec -it <postgres-pod> -- psql -U agentserver -d agentserver \
  -c "SELECT id, name, type, opencode_token IS NULL OR opencode_token = '' AS opencode_empty FROM sandboxes WHERE id = '<sandbox_id>';"
```

Expected: one row with `type = mobile`, `opencode_empty = t`.

- [ ] **Step 6: Reject an invalid type**

```bash
curl -sS -o /dev/stdout -w '\nHTTP %{http_code}\n' -X POST https://<agentserver-host>/api/agent/register \
  -H "Authorization: Bearer <access_token>" \
  -H "Content-Type: application/json" \
  -d '{"name":"bogus","type":"android"}'
```

Expected: `invalid type: must be opencode, claudecode, or mobile` followed by `HTTP 400`.

This task produces no commits — it's an acceptance gate before marking the plan complete.

---

## Task 6: Update the mobile-agent spec with the concrete client ID (documentation)

**Files:**
- Modify: `docs/superpowers/specs/2026-04-10-mobile-agent-design.md`

- [ ] **Step 1: Locate the device-flow section in the spec**

Run: `grep -n "client_id=agentserver-agent-mobile" /root/agentserver/docs/superpowers/specs/2026-04-10-mobile-agent-design.md`
Expected: exactly one hit around line 262 inside the "Flow 1 — Agentserver device flow" block.

- [ ] **Step 2: Append a note to the "Server-Side Changes → Hydra OAuth client for mobile" section (around lines 441-453)**

Add a one-line parenthetical after the YAML block so future readers know this was actually delivered:

Find this line (around line 453):
```
  token_endpoint_auth_method: none    # public client
```

Append (one line below, outside the ```yaml fence):
```
Implemented in `deploy/helm/agentserver/templates/hydra.yaml` by plan `2026-04-10-mobile-agent-p1-agentserver-mobile-client.md`.
```

- [ ] **Step 3: Commit**

```bash
cd /root/agentserver && git add docs/superpowers/specs/2026-04-10-mobile-agent-design.md
git commit -m "docs: note p1 delivery of mobile Hydra client"
```

---

## Self-Review Checklist

Before marking this plan done, confirm:

- [ ] `go test ./internal/server/... -run TestNormalize -v` passes.
- [ ] `go vet ./internal/server/...` is clean.
- [ ] `go build ./...` at the repo root succeeds.
- [ ] `grep -n 'agentserver-agent-mobile' deploy/helm/agentserver/templates/hydra.yaml` returns exactly two lines (update + create).
- [ ] `grep -n 'case "mobile"' internal/server/agent_register_helpers.go` returns exactly one line.
- [ ] The two new commits plus the two existing commits in this plan (helper + handler wiring + helm + docs) land on the branch cleanly.
- [ ] Manual Task 5 either ran successfully, or is recorded in the PR description as "deferred to staging smoke test".

---

## Out of scope (explicitly not this plan)

- **Push notifications.** Covered by P2.
- **Mobile tunnel (`/api/tunnel/mobile/{sandboxId}`).** Covered by P3.
- **Modelserver device flow.** Covered by the existing P0 plan `2026-04-10-mobile-agent-p0-modelserver.md`.
- **Flutter app.** Covered by P4.
- **Schema CHECK constraint on `sandboxes.type`.** Not added; the handler is the single source of truth. Adding a CHECK constraint would require coordinating every future agent type across DB + Go code, which has no benefit while the handler is already strict.
- **Refactoring `handleAgentRegister` to accept a DB interface for unit tests.** Nice to have, but out of scope. If P2 or P3 needs this refactor it should be its own mini-plan.
