# codex-app-gateway: agentserver-issued bearer tokens — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace codex-app-gateway's HMAC bearer with agentserver-minted, DB-backed tokens displayed in the web UI; simultaneously simplify supervisor key from `(workspace, thread)` to `workspace` so codex's native multi-thread protocol works unmodified.

**Architecture:** Three concentric layers built bottom-up. (1) agentserver gains a new `codex_remote_tokens` table, user-facing mint/list/revoke handlers, and an internal `verify` endpoint. (2) codex-app-gateway swaps `auth.HMACAuthenticator` for `auth.RemoteVerifier` (HTTP client to agentserver), drops `ThreadID` from `Identity` and `supervisor.Key`, and shortens its S3 key layout. (3) Web UI gains a workspace-settings sub-page; chart wires two new env vars; spec/plan supersede headers added.

**Tech Stack:** Go 1.26 (chi/v5 router, `golang.org/x/crypto/bcrypt`, `database/sql`+`lib/pq`); React 19 + Vite (existing `web/`); Postgres 18 (existing migration runner in `internal/db/db.go`); Helm v3 chart.

**Spec:** `/root/agentserver/docs/superpowers/specs/2026-05-15-codex-app-gateway-oauth-bridge-design.md` (read § Data model, § HTTP API, § codex-app-gateway changes before starting).

**Working directory:** `/root/agentserver`. Use a worktree per the superpowers:using-git-worktrees skill before starting.

**Module path:** `github.com/agentserver/agentserver`.

**Convention deviation from spec:** Spec § HTTP API shows the internal verify endpoint authenticated by `Authorization: Bearer <internal.apiSecret>`. The actual agentserver convention (see `internal/server/server.go:178-190` for the cc-broker workspace-token endpoint) is `X-Internal-Secret: <secret>` from the same `INTERNAL_API_SECRET` env var. **This plan follows the codebase convention** (`X-Internal-Secret`), not the spec's wording. Spec is correct in intent; only the header name differs.

---

## File Structure

| File | Responsibility | Phase |
|---|---|---|
| `internal/db/migrations/022_codex_remote_tokens.sql` | New table DDL + indexes | 1 |
| `internal/db/codex_tokens.go` | DB CRUD: `CreateCodexToken`, `GetCodexToken`, `ListCodexTokensForWorkspace`, `RevokeCodexToken`, `TouchCodexToken` | 1 |
| `internal/db/codex_tokens_test.go` | Per-method DB tests (skipped without `TEST_DATABASE_URL`) | 1 |
| `internal/server/codex_tokens.go` | User-facing handlers: `handleMintCodexToken`, `handleListCodexTokens`, `handleRevokeCodexToken` | 1 |
| `internal/server/codex_tokens_test.go` | Handler unit tests using `httptest` + a fake user via `auth.ContextWithUserID` | 1 |
| `internal/server/codex_tokens_internal.go` | Internal handler: `handleVerifyCodexToken` | 1 |
| `internal/server/codex_tokens_internal_test.go` | Bcrypt round-trip + 401 cases | 1 |
| `internal/server/server.go` | Wire 4 new routes (3 protected + 1 internal) | 1 |
| `internal/codexappgateway/auth/auth.go` | `Identity` drops `ThreadID`; `Authenticator.Verify` signature loses `threadID` | 2 |
| `internal/codexappgateway/auth/auth_test.go` | Adjust `HMACAuthenticator` tests to single-field `Identity` (still keeps Mint round-trip for break-glass) | 2 |
| `internal/codexappgateway/auth/remote_verifier.go` | `RemoteVerifier` HTTP client | 2 |
| `internal/codexappgateway/auth/remote_verifier_test.go` | `httptest`-stubbed agentserver covering success / 401 / network err | 2 |
| `internal/codexappgateway/supervisor/supervisor.go` | `Key` field reduced to `WorkspaceID` only | 2 |
| `internal/codexappgateway/supervisor/{spawn,reaper,supervisor}_test.go` | Update `Key{...}` literals + assertions | 2 |
| `internal/codexappgateway/codexhome/codexhome.go` | `Manager.NewTmpDir(workspaceID)`; remove `threadID` arg | 2 |
| `internal/codexappgateway/codexhome/s3.go` | `NewS3Backend(store, workspaceID)`; key template `codex-app-gateway/<ws>.tar.gz` | 2 |
| `internal/codexappgateway/codexhome/{codexhome,s3}_test.go` | Update fixtures + key assertions | 2 |
| `internal/codexappgateway/config.go` | Add `AgentserverInternalURL`, `AgentserverInternalSecret` to `ServeConfig` + `CXG_AGENTSERVER_*` env loaders | 2 |
| `internal/codexappgateway/config_test.go` | New required-env tests | 2 |
| `internal/codexappgateway/server.go` | Construct `RemoteVerifier`; rewrite `handleCodexAppWS` (drop URL parsing of `thread_id`); rename `handleAdminRestart` body field | 2 |
| `internal/codexappgateway/server_test.go` | Use `RemoteVerifier`-via-`httptest` instead of HMAC; drop `?thread_id=` URL form | 2 |
| `internal/codexappgateway/integration_test.go` | Same fixture switch | 2 |
| `internal/codexappgateway/buildconfig_test.go` | `makeBuildConfig` arity change (drops `threadID`) | 2 |
| `deploy/helm/agentserver/values.yaml` | (no change — env reuses `internal.apiSecret`) | 3 |
| `deploy/helm/agentserver/templates/codex-app-gateway.yaml` | Add `CXG_AGENTSERVER_INTERNAL_URL` + `CXG_AGENTSERVER_INTERNAL_SECRET` envs | 3 |
| `web/src/lib/api.ts` | Add `CodexToken` type + `listCodexTokens` / `mintCodexToken` / `revokeCodexToken` functions | 3 |
| `web/src/components/CodexTokensPanel.tsx` | New panel (list + generate modal + generated-modal) | 3 |
| `web/src/components/WorkspaceDetail.tsx` | Mount the new panel under workspace settings | 3 |
| `deploy/helm/agentserver/Chart.yaml` | Bump `version`/`appVersion` to `0.50.0` | 3 |
| `docs/superpowers/specs/2026-05-10-codex-app-gateway-subprocess.md` | Add SUPERSEDED header pointing at the new spec | 3 |
| `docs/superpowers/plans/2026-05-11-codex-app-gateway-subprocess.md` | Same | 3 |

Total new files: 9. Modified: 17. Estimated LOC including tests: ~1300.

---

## Phase 1 — agentserver backend (additive, ships independently)

### Task 1: DB migration + table

**Files:**
- Create: `internal/db/migrations/022_codex_remote_tokens.sql`

- [ ] **Step 1: Write the migration**

`internal/db/migrations/022_codex_remote_tokens.sql`:
```sql
CREATE TABLE IF NOT EXISTS codex_remote_tokens (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    workspace_id    TEXT NOT NULL,
    name            TEXT NOT NULL,
    token_hash      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_codex_tokens_user_workspace
    ON codex_remote_tokens(user_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_codex_tokens_workspace
    ON codex_remote_tokens(workspace_id);
```

- [ ] **Step 2: Verify migration loads (smoke build)**

```bash
cd /root/agentserver && go build ./internal/db/...
```
Expected: clean build (the embed.FS picks up the new file automatically).

- [ ] **Step 3: Commit**

```bash
git add internal/db/migrations/022_codex_remote_tokens.sql
git commit -m "feat(db): codex_remote_tokens table + indexes (migration 022)"
```

---

### Task 2: DB CRUD methods

**Files:**
- Create: `internal/db/codex_tokens.go`
- Create: `internal/db/codex_tokens_test.go`

- [ ] **Step 1: Write the failing tests**

`internal/db/codex_tokens_test.go`:
```go
package db

import (
	"context"
	"os"
	"testing"
	"time"
)

func newCodexTestDB(t *testing.T) *DB {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := Open(url)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		d.Exec(`DELETE FROM codex_remote_tokens`)
		d.Close()
	})
	return d
}

func TestCodexTokens_CreateAndGet(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	exp := time.Now().Add(90 * 24 * time.Hour).UTC()
	err := d.CreateCodexToken(ctx, CodexToken{
		ID:          "a3k9f7zq",
		UserID:      "usr_a",
		WorkspaceID: "ws_x",
		Name:        "my mac",
		TokenHash:   "$2a$12$abc",
		ExpiresAt:   exp,
	})
	if err != nil {
		t.Fatalf("CreateCodexToken: %v", err)
	}
	got, err := d.GetCodexToken(ctx, "a3k9f7zq")
	if err != nil || got == nil {
		t.Fatalf("GetCodexToken: %v %+v", err, got)
	}
	if got.UserID != "usr_a" || got.WorkspaceID != "ws_x" || got.Name != "my mac" {
		t.Errorf("got = %+v", got)
	}
}

func TestCodexTokens_GetMissing(t *testing.T) {
	d := newCodexTestDB(t)
	got, err := d.GetCodexToken(context.Background(), "missing")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != nil {
		t.Fatalf("want nil for missing, got %+v", got)
	}
}

func TestCodexTokens_ListAndRevoke(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	for i, id := range []string{"id1", "id2", "id3"} {
		_ = d.CreateCodexToken(ctx, CodexToken{
			ID: id, UserID: "usr_a", WorkspaceID: "ws_x",
			Name: id, TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour),
		})
		_ = i
	}
	rows, _ := d.ListCodexTokensForWorkspace(ctx, "ws_x", false)
	if len(rows) != 3 {
		t.Fatalf("want 3, got %d", len(rows))
	}
	if err := d.RevokeCodexToken(ctx, "id2"); err != nil {
		t.Fatalf("RevokeCodexToken: %v", err)
	}
	rows, _ = d.ListCodexTokensForWorkspace(ctx, "ws_x", false)
	if len(rows) != 2 {
		t.Fatalf("after revoke want 2, got %d", len(rows))
	}
	rows, _ = d.ListCodexTokensForWorkspace(ctx, "ws_x", true)
	if len(rows) != 3 {
		t.Fatalf("with include_revoked want 3, got %d", len(rows))
	}
}

func TestCodexTokens_Revoke_Idempotent(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	_ = d.CreateCodexToken(ctx, CodexToken{
		ID: "id1", UserID: "u", WorkspaceID: "w", Name: "n",
		TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err := d.RevokeCodexToken(ctx, "id1"); err != nil {
		t.Fatal(err)
	}
	if err := d.RevokeCodexToken(ctx, "id1"); err != nil {
		t.Fatal("second revoke must be idempotent")
	}
	if err := d.RevokeCodexToken(ctx, "missing"); err != nil {
		t.Fatal("revoke missing must be idempotent")
	}
}

func TestCodexTokens_Touch(t *testing.T) {
	d := newCodexTestDB(t)
	ctx := context.Background()
	_ = d.CreateCodexToken(ctx, CodexToken{
		ID: "id1", UserID: "u", WorkspaceID: "w", Name: "n",
		TokenHash: "h", ExpiresAt: time.Now().Add(time.Hour),
	})
	if err := d.TouchCodexToken(ctx, "id1"); err != nil {
		t.Fatalf("TouchCodexToken: %v", err)
	}
	got, _ := d.GetCodexToken(ctx, "id1")
	if got.LastUsedAt == nil {
		t.Fatal("LastUsedAt should be set after touch")
	}
}
```

- [ ] **Step 2: Run tests (expect build error)**

```bash
cd /root/agentserver && go test ./internal/db/ -run TestCodexTokens
```
Expected: build error (`undefined: CodexToken`).

- [ ] **Step 3: Implement `internal/db/codex_tokens.go`**

```go
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CodexToken is one row from codex_remote_tokens.
type CodexToken struct {
	ID           string
	UserID       string
	WorkspaceID  string
	Name         string
	TokenHash    string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	LastUsedAt   *time.Time
	RevokedAt    *time.Time
}

// CreateCodexToken inserts a new row. Caller mints the bcrypt hash.
func (db *DB) CreateCodexToken(ctx context.Context, t CodexToken) error {
	_, err := db.ExecContext(ctx, `
		INSERT INTO codex_remote_tokens (id, user_id, workspace_id, name, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		t.ID, t.UserID, t.WorkspaceID, t.Name, t.TokenHash, t.ExpiresAt)
	if err != nil {
		return fmt.Errorf("insert codex_remote_tokens: %w", err)
	}
	return nil
}

// GetCodexToken returns the row by id, or (nil, nil) if absent.
// It includes revoked rows so the verify endpoint can apply its own policy.
func (db *DB) GetCodexToken(ctx context.Context, id string) (*CodexToken, error) {
	row := db.QueryRowContext(ctx, `
		SELECT id, user_id, workspace_id, name, token_hash,
		       created_at, expires_at, last_used_at, revoked_at
		FROM codex_remote_tokens WHERE id = $1`, id)
	var t CodexToken
	var lastUsed, revoked sql.NullTime
	err := row.Scan(&t.ID, &t.UserID, &t.WorkspaceID, &t.Name, &t.TokenHash,
		&t.CreatedAt, &t.ExpiresAt, &lastUsed, &revoked)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan codex_remote_tokens: %w", err)
	}
	if lastUsed.Valid {
		v := lastUsed.Time
		t.LastUsedAt = &v
	}
	if revoked.Valid {
		v := revoked.Time
		t.RevokedAt = &v
	}
	return &t, nil
}

// ListCodexTokensForWorkspace returns tokens for a workspace, ordered by created_at desc.
// includeRevoked controls whether soft-revoked rows are included.
func (db *DB) ListCodexTokensForWorkspace(ctx context.Context, workspaceID string, includeRevoked bool) ([]CodexToken, error) {
	q := `SELECT id, user_id, workspace_id, name, token_hash,
	             created_at, expires_at, last_used_at, revoked_at
	      FROM codex_remote_tokens
	      WHERE workspace_id = $1`
	if !includeRevoked {
		q += ` AND revoked_at IS NULL`
	}
	q += ` ORDER BY created_at DESC`
	rows, err := db.QueryContext(ctx, q, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("query codex_remote_tokens: %w", err)
	}
	defer rows.Close()
	var out []CodexToken
	for rows.Next() {
		var t CodexToken
		var lastUsed, revoked sql.NullTime
		if err := rows.Scan(&t.ID, &t.UserID, &t.WorkspaceID, &t.Name, &t.TokenHash,
			&t.CreatedAt, &t.ExpiresAt, &lastUsed, &revoked); err != nil {
			return nil, err
		}
		if lastUsed.Valid {
			v := lastUsed.Time
			t.LastUsedAt = &v
		}
		if revoked.Valid {
			v := revoked.Time
			t.RevokedAt = &v
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeCodexToken soft-revokes a token. Idempotent: re-revoke and missing-id are no-ops.
func (db *DB) RevokeCodexToken(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE codex_remote_tokens
		SET revoked_at = NOW()
		WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		return fmt.Errorf("revoke codex_remote_tokens: %w", err)
	}
	return nil
}

// TouchCodexToken sets last_used_at = NOW(). Best-effort use only — caller
// should fire-and-forget in a goroutine and log warnings, not propagate errors.
func (db *DB) TouchCodexToken(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE codex_remote_tokens SET last_used_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("touch codex_remote_tokens: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run tests (expect pass / skip)**

```bash
cd /root/agentserver && TEST_DATABASE_URL=postgres://... go test ./internal/db/ -run TestCodexTokens -v
```
Expected: 5 PASS (or 5 SKIP with no `TEST_DATABASE_URL`).

- [ ] **Step 5: Commit**

```bash
git add internal/db/codex_tokens.go internal/db/codex_tokens_test.go
git commit -m "feat(db): codex_remote_tokens CRUD + Touch (best-effort last_used_at)"
```

---

### Task 3: Token format helpers

**Files:**
- Create: `internal/server/codex_token_format.go`
- Create: `internal/server/codex_token_format_test.go`

The token shape `ast_<id>_<secret>` is parsed identically by mint (return) and verify (decompose). Centralise to a small helper.

- [ ] **Step 1: Write failing tests**

`internal/server/codex_token_format_test.go`:
```go
package server

import (
	"strings"
	"testing"
)

func TestGenerateCodexToken_ShapeAndUniqueness(t *testing.T) {
	t1, id1, secret1, err := generateCodexToken()
	if err != nil {
		t.Fatalf("gen: %v", err)
	}
	if !strings.HasPrefix(t1, "ast_") {
		t.Errorf("missing prefix: %s", t1)
	}
	if id1 == "" || len(id1) != 8 {
		t.Errorf("id len: %q", id1)
	}
	if len(secret1) < 40 {
		t.Errorf("secret too short: %d", len(secret1))
	}
	if t1 != "ast_"+id1+"_"+secret1 {
		t.Errorf("token recombination mismatch: %s", t1)
	}
	t2, _, _, _ := generateCodexToken()
	if t1 == t2 {
		t.Error("two generated tokens collide")
	}
}

func TestParseCodexToken(t *testing.T) {
	cases := []struct {
		in        string
		wantID    string
		wantSec   string
		wantErr   bool
	}{
		{"ast_a3k9f7zq_n2p4xj8m", "a3k9f7zq", "n2p4xj8m", false},
		{"", "", "", true},
		{"ast_only_two_segments", "", "", true},
		{"foo_a3k9f7zq_secret", "", "", true},
		{"ast__secret", "", "", true},
		{"ast_id_", "", "", true},
	}
	for _, c := range cases {
		id, sec, err := parseCodexToken(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("%q: want err, got id=%q sec=%q", c.in, id, sec)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", c.in, err)
		}
		if id != c.wantID || sec != c.wantSec {
			t.Errorf("%q: got id=%q sec=%q", c.in, id, sec)
		}
	}
}
```

- [ ] **Step 2: Implement `codex_token_format.go`**

```go
package server

import (
	"crypto/rand"
	"errors"
	"math/big"
	"strings"
)

const (
	codexTokenPrefix    = "ast_"
	codexTokenIDLen     = 8
	codexTokenSecretLen = 40
	codexTokenAlphabet  = "0123456789abcdefghijklmnopqrstuvwxyz"
)

var errBadCodexToken = errors.New("bad codex token format")

// generateCodexToken returns (full_token, id, secret, err) where full_token
// is what we hand the user and id/secret are the parts we persist (id as PK,
// bcrypt(secret) as token_hash).
func generateCodexToken() (full, id, secret string, err error) {
	id, err = randomBase36(codexTokenIDLen)
	if err != nil {
		return
	}
	secret, err = randomBase36(codexTokenSecretLen)
	if err != nil {
		return
	}
	full = codexTokenPrefix + id + "_" + secret
	return
}

// parseCodexToken validates shape and splits a token into (id, secret).
func parseCodexToken(tok string) (id, secret string, err error) {
	if !strings.HasPrefix(tok, codexTokenPrefix) {
		return "", "", errBadCodexToken
	}
	rest := tok[len(codexTokenPrefix):]
	sep := strings.IndexByte(rest, '_')
	if sep < 1 || sep == len(rest)-1 {
		return "", "", errBadCodexToken
	}
	return rest[:sep], rest[sep+1:], nil
}

func randomBase36(n int) (string, error) {
	b := make([]byte, n)
	max := big.NewInt(int64(len(codexTokenAlphabet)))
	for i := range b {
		k, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = codexTokenAlphabet[k.Int64()]
	}
	return string(b), nil
}
```

- [ ] **Step 3: Run tests (expect pass)**

```bash
cd /root/agentserver && go test ./internal/server/ -run "TestGenerateCodexToken|TestParseCodexToken" -v
```
Expected: 2 PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/server/codex_token_format.go internal/server/codex_token_format_test.go
git commit -m "feat(server): codex token generate + parse helpers"
```

---

### Task 4: Mint / list / revoke handlers

**Files:**
- Create: `internal/server/codex_tokens.go`
- Create: `internal/server/codex_tokens_test.go`

- [ ] **Step 1: Write failing tests**

`internal/server/codex_tokens_test.go`:
```go
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func newCodexTokensTestServer(t *testing.T) (*Server, *db.DB) {
	t.Helper()
	d := newCodexTestDBForServer(t)
	srv := &Server{DB: d}
	return srv, d
}

// newCodexTestDBForServer is a small wrapper that produces a *db.DB the
// codex_tokens handlers can use. Skips the test if no TEST_DATABASE_URL.
func newCodexTestDBForServer(t *testing.T) *db.DB {
	t.Helper()
	// Re-use db package's existing skip-when-no-URL pattern.
	import_d, err := openTestDBForServer(t)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		import_d.Exec(`DELETE FROM codex_remote_tokens`)
	})
	return import_d
}

func ctxWithUser(uid string) context.Context {
	return auth.ContextWithUserID(context.Background(), uid)
}

func TestHandleMintCodexToken_HappyPath(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	// Seed: user u1 is a member of ws_a.
	seedWorkspaceMember(t, d, "ws_a", "u1", "owner")

	body := bytes.NewReader([]byte(`{"workspace_id":"ws_a","name":"my mac","ttl_days":30}`))
	req := httptest.NewRequest(http.MethodPost, "/api/codex/tokens", body).
		WithContext(ctxWithUser("u1"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleMintCodexToken(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		ID, Token, Name, WorkspaceID, ExpiresAt string
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID == "" || len(resp.Token) < 30 {
		t.Fatalf("missing fields: %+v", resp)
	}
	id, secret, err := parseCodexToken(resp.Token)
	if err != nil || id != resp.ID {
		t.Fatalf("token shape: %v id=%q resp.ID=%q", err, id, resp.ID)
	}
	row, _ := d.GetCodexToken(req.Context(), id)
	if row == nil {
		t.Fatal("row missing")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.TokenHash), []byte(secret)); err != nil {
		t.Fatalf("hash verify: %v", err)
	}
}

func TestHandleMintCodexToken_NotMember_403(t *testing.T) {
	srv, _ := newCodexTokensTestServer(t)
	body := bytes.NewReader([]byte(`{"workspace_id":"ws_a","name":"x"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/codex/tokens", body).
		WithContext(ctxWithUser("u_no"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleMintCodexToken(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleMintCodexToken_TTLClamp(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	seedWorkspaceMember(t, d, "ws_a", "u1", "owner")
	body := bytes.NewReader([]byte(`{"workspace_id":"ws_a","name":"x","ttl_days":99999}`))
	req := httptest.NewRequest(http.MethodPost, "/api/codex/tokens", body).
		WithContext(ctxWithUser("u1"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleMintCodexToken(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rr.Code)
	}
}

func TestHandleListCodexTokens(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	seedWorkspaceMember(t, d, "ws_a", "u1", "owner")
	for _, n := range []string{"a", "b"} {
		body := bytes.NewReader([]byte(`{"workspace_id":"ws_a","name":"` + n + `"}`))
		req := httptest.NewRequest(http.MethodPost, "/api/codex/tokens", body).
			WithContext(ctxWithUser("u1"))
		req.Header.Set("Content-Type", "application/json")
		srv.handleMintCodexToken(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/codex/tokens?workspace_id=ws_a", nil).
		WithContext(ctxWithUser("u1"))
	rr := httptest.NewRecorder()
	srv.handleListCodexTokens(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	var got []map[string]any
	json.Unmarshal(rr.Body.Bytes(), &got)
	if len(got) != 2 {
		t.Fatalf("want 2 tokens, got %d: %v", len(got), got)
	}
	if _, ok := got[0]["token"]; ok {
		t.Fatal("list response must NOT include raw token")
	}
}

func TestHandleRevokeCodexToken(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	seedWorkspaceMember(t, d, "ws_a", "u1", "owner")
	body := bytes.NewReader([]byte(`{"workspace_id":"ws_a","name":"x"}`))
	mintReq := httptest.NewRequest(http.MethodPost, "/api/codex/tokens", body).
		WithContext(ctxWithUser("u1"))
	mintReq.Header.Set("Content-Type", "application/json")
	mintRR := httptest.NewRecorder()
	srv.handleMintCodexToken(mintRR, mintReq)
	var mr struct{ ID string }
	json.Unmarshal(mintRR.Body.Bytes(), &mr)

	delReq := httptest.NewRequest(http.MethodDelete, "/api/codex/tokens/"+mr.ID, nil).
		WithContext(ctxWithUser("u1"))
	rr := httptest.NewRecorder()
	srv.routesForCodexTokens().ServeHTTP(rr, delReq)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body=%s", rr.Code, rr.Body.String())
	}
	// Idempotent.
	rr2 := httptest.NewRecorder()
	srv.routesForCodexTokens().ServeHTTP(rr2, delReq)
	if rr2.Code != http.StatusNoContent {
		t.Fatalf("second delete status = %d", rr2.Code)
	}
}
```

- [ ] **Step 2: Run (expect build error)**

```bash
cd /root/agentserver && go test ./internal/server/ -run TestHandleMintCodexToken
```
Expected: build error (`undefined: handleMintCodexToken`, `seedWorkspaceMember`, `openTestDBForServer`, `routesForCodexTokens`).

- [ ] **Step 3: Add helpers**

Search for existing `seedWorkspaceMember` analog or test-DB helpers:

```bash
cd /root/agentserver && grep -rn "func seedWorkspaceMember\|func openTestDB\|TEST_DATABASE_URL" internal/server/ 2>&1 | head
```

If neither helper exists, add a small `internal/server/codex_tokens_testhelper_test.go`:

```go
package server

import (
	"os"
	"testing"

	"github.com/agentserver/agentserver/internal/db"
)

func openTestDBForServer(t *testing.T) (*db.DB, error) {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	d, err := db.Open(url)
	if err == nil {
		t.Cleanup(func() { d.Close() })
	}
	return d, err
}

func seedWorkspaceMember(t *testing.T, d *db.DB, wid, uid, role string) {
	t.Helper()
	if _, err := d.Exec(`INSERT INTO workspaces (id, name) VALUES ($1, $2) ON CONFLICT DO NOTHING`, wid, "test ws"); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO users (id, email) VALUES ($1, $2) ON CONFLICT DO NOTHING`, uid, uid+"@test"); err != nil {
		t.Fatalf("insert user: %v", err)
	}
	if _, err := d.Exec(`INSERT INTO workspace_members (workspace_id, user_id, role) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, wid, uid, role); err != nil {
		t.Fatalf("insert member: %v", err)
	}
}
```

(If your repo's `users` schema differs — check `internal/db/migrations/001_initial.sql` — adjust the INSERT columns. The point is that the workspace + member rows must exist for `GetWorkspaceMemberRole` to return non-empty.)

- [ ] **Step 4: Implement `internal/server/codex_tokens.go`**

```go
package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/agentserver/agentserver/internal/auth"
	"github.com/agentserver/agentserver/internal/db"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"
)

const (
	codexTokenDefaultTTLDays = 90
	codexTokenMinTTLDays     = 1
	codexTokenMaxTTLDays     = 365
)

type mintCodexTokenReq struct {
	WorkspaceID string `json:"workspace_id"`
	Name        string `json:"name"`
	TTLDays     int    `json:"ttl_days,omitempty"`
}

type mintCodexTokenResp struct {
	ID          string    `json:"id"`
	Token       string    `json:"token"`
	Name        string    `json:"name"`
	WorkspaceID string    `json:"workspace_id"`
	ExpiresAt   time.Time `json:"expires_at"`
	CreatedAt   time.Time `json:"created_at"`
}

type listCodexTokenItem struct {
	ID          string     `json:"id"`
	Name        string     `json:"name"`
	WorkspaceID string     `json:"workspace_id"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	Revoked     bool       `json:"revoked"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

func (s *Server) handleMintCodexToken(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var req mintCodexTokenReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.WorkspaceID == "" || req.Name == "" {
		http.Error(w, "workspace_id and name are required", http.StatusUnprocessableEntity)
		return
	}
	if req.TTLDays == 0 {
		req.TTLDays = codexTokenDefaultTTLDays
	}
	if req.TTLDays < codexTokenMinTTLDays || req.TTLDays > codexTokenMaxTTLDays {
		http.Error(w, "ttl_days out of range [1, 365]", http.StatusUnprocessableEntity)
		return
	}

	role, err := s.DB.GetWorkspaceMemberRole(req.WorkspaceID, userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if role == "" || role == "guest" {
		http.Error(w, "not a member of this workspace", http.StatusForbidden)
		return
	}

	full, id, secret, err := generateCodexToken()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	exp := time.Now().Add(time.Duration(req.TTLDays) * 24 * time.Hour).UTC()
	if err := s.DB.CreateCodexToken(r.Context(), db.CodexToken{
		ID: id, UserID: userID, WorkspaceID: req.WorkspaceID, Name: req.Name,
		TokenHash: string(hash), ExpiresAt: exp,
	}); err != nil {
		http.Error(w, "create failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(mintCodexTokenResp{
		ID: id, Token: full, Name: req.Name, WorkspaceID: req.WorkspaceID,
		ExpiresAt: exp, CreatedAt: time.Now().UTC(),
	})
}

func (s *Server) handleListCodexTokens(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	wid := r.URL.Query().Get("workspace_id")
	if wid == "" {
		http.Error(w, "workspace_id required", http.StatusBadRequest)
		return
	}
	role, err := s.DB.GetWorkspaceMemberRole(wid, userID)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if role == "" {
		http.Error(w, "not a member", http.StatusForbidden)
		return
	}
	includeRevoked := r.URL.Query().Get("include_revoked") == "true"
	rows, err := s.DB.ListCodexTokensForWorkspace(r.Context(), wid, includeRevoked)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]listCodexTokenItem, 0, len(rows))
	for _, t := range rows {
		out = append(out, listCodexTokenItem{
			ID: t.ID, Name: t.Name, WorkspaceID: t.WorkspaceID,
			CreatedAt: t.CreatedAt, ExpiresAt: t.ExpiresAt,
			LastUsedAt: t.LastUsedAt, Revoked: t.RevokedAt != nil, RevokedAt: t.RevokedAt,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func (s *Server) handleRevokeCodexToken(w http.ResponseWriter, r *http.Request) {
	userID := auth.UserIDFromContext(r.Context())
	if userID == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	id := chi.URLParam(r, "id")
	if id == "" {
		http.Error(w, "id required", http.StatusBadRequest)
		return
	}
	row, err := s.DB.GetCodexToken(r.Context(), id)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	if row == nil {
		// Idempotent: deleting a missing id is a 204.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	role, _ := s.DB.GetWorkspaceMemberRole(row.WorkspaceID, userID)
	isOwner := row.UserID == userID
	isAdmin := role == "owner" || role == "maintainer"
	if !isOwner && !isAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.DB.RevokeCodexToken(r.Context(), id); err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// routesForCodexTokens is a small chi sub-router used only by tests so the
// `{id}` URL param resolves correctly when calling the handler outside the
// main Routes() wiring.
func (s *Server) routesForCodexTokens() http.Handler {
	r := chi.NewRouter()
	r.Post("/api/codex/tokens", s.handleMintCodexToken)
	r.Get("/api/codex/tokens", s.handleListCodexTokens)
	r.Delete("/api/codex/tokens/{id}", s.handleRevokeCodexToken)
	return r
}

// Compile-time guards against accidental signature drift.
var (
	_ = errors.New
)
```

- [ ] **Step 5: Run tests (expect pass / skip)**

```bash
cd /root/agentserver && TEST_DATABASE_URL=postgres://... go test ./internal/server/ -run "TestHandleMintCodexToken|TestHandleListCodexTokens|TestHandleRevokeCodexToken" -v
```
Expected: 5 PASS (or SKIP without DB).

- [ ] **Step 6: Commit**

```bash
git add internal/server/codex_tokens.go internal/server/codex_tokens_test.go internal/server/codex_token_format.go internal/server/codex_token_format_test.go internal/server/codex_tokens_testhelper_test.go
git commit -m "feat(server): codex remote token mint/list/revoke endpoints"
```

---

### Task 5: Internal verify endpoint

**Files:**
- Create: `internal/server/codex_tokens_internal.go`
- Create: `internal/server/codex_tokens_internal_test.go`

- [ ] **Step 1: Write failing tests**

`internal/server/codex_tokens_internal_test.go`:
```go
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/agentserver/agentserver/internal/db"
	"golang.org/x/crypto/bcrypt"
)

func mintRow(t *testing.T, d *db.DB, id, secret, uid, wid string, exp time.Time, revokedAt *time.Time) {
	t.Helper()
	hash, _ := bcrypt.GenerateFromPassword([]byte(secret), bcrypt.MinCost)
	row := db.CodexToken{
		ID: id, UserID: uid, WorkspaceID: wid, Name: "n",
		TokenHash: string(hash), ExpiresAt: exp,
	}
	if err := d.CreateCodexToken(context.Background(), row); err != nil {
		t.Fatalf("create: %v", err)
	}
	if revokedAt != nil {
		_, _ = d.Exec(`UPDATE codex_remote_tokens SET revoked_at = $1 WHERE id = $2`, *revokedAt, id)
	}
}

func TestHandleVerifyCodexToken_HappyPath(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	mintRow(t, d, "abc12345", "supersecret", "u1", "ws_a", time.Now().Add(time.Hour), nil)

	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_supersecret"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var resp struct {
		UserID, WorkspaceID string
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.UserID != "u1" || resp.WorkspaceID != "ws_a" {
		t.Fatalf("got %+v", resp)
	}
}

func TestHandleVerifyCodexToken_BadSecret_401(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	mintRow(t, d, "abc12345", "rightsecret", "u1", "ws_a", time.Now().Add(time.Hour), nil)
	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_wrongsecret"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_Expired_401(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	mintRow(t, d, "abc12345", "s", "u1", "ws_a", time.Now().Add(-time.Hour), nil)
	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_s"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_Revoked_401(t *testing.T) {
	srv, d := newCodexTokensTestServer(t)
	now := time.Now()
	mintRow(t, d, "abc12345", "s", "u1", "ws_a", time.Now().Add(time.Hour), &now)
	body := bytes.NewReader([]byte(`{"token":"ast_abc12345_s"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_NotFound_401(t *testing.T) {
	srv, _ := newCodexTokensTestServer(t)
	body := bytes.NewReader([]byte(`{"token":"ast_zzzzzzzz_zzzz"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}

func TestHandleVerifyCodexToken_BadShape_401(t *testing.T) {
	srv, _ := newCodexTokensTestServer(t)
	body := bytes.NewReader([]byte(`{"token":"garbage"}`))
	req := httptest.NewRequest(http.MethodPost, "/api/internal/codex/tokens/verify", body)
	rr := httptest.NewRecorder()
	srv.handleVerifyCodexToken(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rr.Code)
	}
}
```

- [ ] **Step 2: Implement `internal/server/codex_tokens_internal.go`**

```go
package server

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type verifyReq struct {
	Token string `json:"token"`
}

type verifyResp struct {
	UserID      string `json:"user_id"`
	WorkspaceID string `json:"workspace_id"`
}

func (s *Server) handleVerifyCodexToken(w http.ResponseWriter, r *http.Request) {
	var req verifyReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeVerifyUnauthorized(w)
		return
	}
	id, secret, err := parseCodexToken(req.Token)
	if err != nil {
		writeVerifyUnauthorized(w)
		return
	}
	row, err := s.DB.GetCodexToken(r.Context(), id)
	if err != nil {
		log.Printf("verify codex token: get row: %v", err)
		writeVerifyUnauthorized(w)
		return
	}
	if row == nil {
		writeVerifyUnauthorized(w)
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(row.TokenHash), []byte(secret)); err != nil {
		writeVerifyUnauthorized(w)
		return
	}
	if row.RevokedAt != nil || time.Now().UTC().After(row.ExpiresAt) {
		writeVerifyUnauthorized(w)
		return
	}

	// Async best-effort touch — caller's response is not blocked on this.
	go func(id string) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.DB.TouchCodexToken(ctx, id); err != nil {
			log.Printf("verify codex token: touch %s: %v", id, err)
		}
	}(id)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(verifyResp{
		UserID: row.UserID, WorkspaceID: row.WorkspaceID,
	})
}

func writeVerifyUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`{"error":"invalid_token"}`))
}
```

- [ ] **Step 3: Run tests (expect pass / skip)**

```bash
cd /root/agentserver && TEST_DATABASE_URL=postgres://... go test ./internal/server/ -run TestHandleVerifyCodexToken -v
```
Expected: 6 PASS (or SKIP without DB).

- [ ] **Step 4: Commit**

```bash
git add internal/server/codex_tokens_internal.go internal/server/codex_tokens_internal_test.go
git commit -m "feat(server): internal codex token verify endpoint"
```

---

### Task 6: Wire routes into agentserver

**Files:**
- Modify: `internal/server/server.go`

- [ ] **Step 1: Add the protected routes**

Inside the existing `r.Group(func(r chi.Router) { r.Use(s.Auth.Middleware); ... })` block (search for that block — see existing `r.Get("/api/auth/me", s.handleMe)` etc.), add three lines:

```go
		// Codex remote-access tokens (per-user, per-workspace, DB-backed).
		r.Post("/api/codex/tokens", s.handleMintCodexToken)
		r.Get("/api/codex/tokens", s.handleListCodexTokens)
		r.Delete("/api/codex/tokens/{id}", s.handleRevokeCodexToken)
```

- [ ] **Step 2: Add the internal route**

Outside the auth group (in the same area where `/internal/workspace-token` lives, around server.go:175-200), add:

```go
	// Internal API for codex-app-gateway to verify a remote-access bearer.
	// Auth: X-Internal-Secret matching INTERNAL_API_SECRET.
	r.Post("/api/internal/codex/tokens/verify", func(w http.ResponseWriter, r *http.Request) {
		secret := os.Getenv("INTERNAL_API_SECRET")
		if secret != "" {
			if r.Header.Get("X-Internal-Secret") != secret {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		s.handleVerifyCodexToken(w, r)
	})
```

- [ ] **Step 3: Build + lint**

```bash
cd /root/agentserver && go build ./... && go vet ./...
```
Expected: clean.

- [ ] **Step 4: Smoke-test the route wiring**

Add a small integration check at the top of `internal/server/server.go` if there is a `Routes()` accessor; otherwise verify via httptest in an existing `server_test.go`. If neither exists, do a manual smoke (run agentserver against `TEST_DATABASE_URL`, curl `POST /api/codex/tokens` with cookie auth) and skip an automated smoke step here.

- [ ] **Step 5: Commit**

```bash
git add internal/server/server.go
git commit -m "feat(server): wire codex token routes (protected + internal)"
```

---

## Phase 2 — codex-app-gateway auth swap + supervisor key reduction

### Task 7: `Identity` + `Authenticator` simplification

**Files:**
- Modify: `internal/codexappgateway/auth/auth.go`
- Modify: `internal/codexappgateway/auth/auth_test.go`

- [ ] **Step 1: Update `Identity` and `Authenticator`**

Replace the `Identity` struct + `Authenticator` interface in `internal/codexappgateway/auth/auth.go`:

```go
// Identity is the result of a successful Verify. ThreadID is no longer
// part of identity — codex's app-server manages threads internally via
// JSON-RPC after connect, so the gateway only needs (user, workspace).
type Identity struct {
	UserID      string
	WorkspaceID string
}

// Authenticator is the seam between Server and any concrete bearer scheme.
type Authenticator interface {
	Verify(ctx context.Context, token string) (Identity, error)
}
```

(Keep `ExtractBearer`, the `HMAC` type, and `NewHMAC` — they stay valid for tests + break-glass tooling, but `HMAC.Verify` now returns `Identity{WorkspaceID: parts[0]}` only and ignores the threadID portion of the legacy 3-part token to keep tests passing. Leave `HMAC.Mint` unchanged so legacy mint helpers still work; just stop reading thread from the verified output.)

Concretely in `auth.go`:

```go
func (a *HMAC) Verify(token string) (Identity, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return Identity{}, errors.New("auth: malformed token")
	}
	expected := a.Mint(parts[0], parts[1])
	if !hmac.Equal([]byte(expected), []byte(token)) {
		return Identity{}, errors.New("auth: signature mismatch")
	}
	// Phase-2: parts[1] (legacy threadID) is intentionally discarded; codex
	// manages threads. Workspace-only Identity is the new contract.
	return Identity{WorkspaceID: parts[0]}, nil
}
```

The existing interface impl satisfies the new `Authenticator` because the parent context arg is added in `RemoteVerifier`. To keep the same interface, change `HMAC.Verify` to also accept `ctx context.Context` (unused):

```go
// Update method signature to satisfy the new Authenticator interface:
func (a *HMAC) Verify(_ context.Context, token string) (Identity, error) { ... }
```

(Add `"context"` to the imports.)

- [ ] **Step 2: Update `auth_test.go`**

Existing tests assert `Identity{WorkspaceID, ThreadID}`. Reduce to `WorkspaceID` only:

```go
// Replace:
got, err := a.Verify(tok)
// with:
got, err := a.Verify(context.Background(), tok)

// Replace:
if got.WorkspaceID != "ws_alpha" || got.ThreadID != "thr_42" { ... }
// with (drop the ThreadID assertion):
if got.WorkspaceID != "ws_alpha" {
    t.Errorf("decoded = %+v", got)
}
```

Add `"context"` import to the test file.

- [ ] **Step 3: Run tests**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/auth/ -v
```
Expected: PASS (HMAC tests still cover round-trip semantics, just no longer check threadID).

- [ ] **Step 4: Commit**

```bash
git add internal/codexappgateway/auth/auth.go internal/codexappgateway/auth/auth_test.go
git commit -m "refactor(codex-app-gateway/auth): drop ThreadID from Identity"
```

---

### Task 8: `RemoteVerifier`

**Files:**
- Create: `internal/codexappgateway/auth/remote_verifier.go`
- Create: `internal/codexappgateway/auth/remote_verifier_test.go`

- [ ] **Step 1: Write failing tests**

`internal/codexappgateway/auth/remote_verifier_test.go`:
```go
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteVerifier_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Internal-Secret"); got != "s3cret" {
			http.Error(w, "no auth", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/api/internal/codex/tokens/verify" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body struct{ Token string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasPrefix(body.Token, "ast_") {
			t.Errorf("body token = %q", body.Token)
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"user_id": "u1", "workspace_id": "ws_a",
		})
	}))
	defer srv.Close()

	v := NewRemoteVerifier(srv.URL, "s3cret")
	id, err := v.Verify(context.Background(), "ast_a3k9_secret")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if id.UserID != "u1" || id.WorkspaceID != "ws_a" {
		t.Errorf("identity = %+v", id)
	}
}

func TestRemoteVerifier_401_ReturnsErrUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	v := NewRemoteVerifier(srv.URL, "s")
	_, err := v.Verify(context.Background(), "ast_x_y")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want ErrUnauthorized, got %v", err)
	}
}

func TestRemoteVerifier_500_ReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()
	v := NewRemoteVerifier(srv.URL, "s")
	_, err := v.Verify(context.Background(), "ast_x_y")
	if err == nil || errors.Is(err, ErrUnauthorized) {
		t.Fatalf("want non-401 error, got %v", err)
	}
}

func TestRemoteVerifier_NetworkError(t *testing.T) {
	v := NewRemoteVerifier("http://127.0.0.1:1", "s")
	v.httpClient.Timeout = 200 * time.Millisecond
	_, err := v.Verify(context.Background(), "ast_x_y")
	if err == nil {
		t.Fatal("want error on unreachable host")
	}
}
```

- [ ] **Step 2: Run (expect build error)**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/auth/ -run TestRemoteVerifier
```
Expected: build error (`undefined: NewRemoteVerifier, ErrUnauthorized`).

- [ ] **Step 3: Implement `internal/codexappgateway/auth/remote_verifier.go`**

```go
// Package auth implements inbound bearer-token verification.
//
// Phase 2 default is RemoteVerifier: each ws connect POSTs the supplied
// bearer to agentserver's /api/internal/codex/tokens/verify, which owns
// the codex_remote_tokens table and applies bcrypt + expiry + revocation
// policy. This couples the gateway to agentserver's lifecycle but keeps
// the gateway stateless.
//
// HMACAuthenticator stays in the package as a break-glass / local-test
// implementation but is no longer used in chart-deployed pods.
package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrUnauthorized is returned by Verify when agentserver responds 401.
// Distinguishable so handlers can map directly to HTTP 401 without leaking
// other error reasons (network failure → 500, etc.).
var ErrUnauthorized = errors.New("auth: unauthorized")

// RemoteVerifier delegates token verification to agentserver's internal API.
type RemoteVerifier struct {
	baseURL    string
	bearer     string
	httpClient *http.Client
}

// NewRemoteVerifier constructs a verifier targeting agentserver's internal
// HTTP API. baseURL is the http base (e.g.
// "http://release-agentserver.namespace.svc:8080"); bearer is the value of
// INTERNAL_API_SECRET used as the X-Internal-Secret header.
func NewRemoteVerifier(baseURL, bearer string) *RemoteVerifier {
	return &RemoteVerifier{
		baseURL:    strings.TrimRight(baseURL, "/"),
		bearer:     bearer,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// Verify implements Authenticator.
func (v *RemoteVerifier) Verify(ctx context.Context, token string) (Identity, error) {
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		return Identity{}, fmt.Errorf("marshal verify body: %w", err)
	}
	url := v.baseURL + "/api/internal/codex/tokens/verify"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Identity{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Secret", v.bearer)
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return Identity{}, fmt.Errorf("verify call: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return Identity{}, ErrUnauthorized
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return Identity{}, fmt.Errorf("verify call: status=%d body=%q", resp.StatusCode, b)
	}

	var out struct {
		UserID      string `json:"user_id"`
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Identity{}, fmt.Errorf("decode verify response: %w", err)
	}
	return Identity{UserID: out.UserID, WorkspaceID: out.WorkspaceID}, nil
}
```

- [ ] **Step 4: Run tests (expect pass)**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/auth/ -run TestRemoteVerifier -v
```
Expected: 4 PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/auth/remote_verifier.go internal/codexappgateway/auth/remote_verifier_test.go
git commit -m "feat(codex-app-gateway/auth): RemoteVerifier (HTTP client → agentserver)"
```

---

### Task 9: Supervisor key reduction

**Files:**
- Modify: `internal/codexappgateway/supervisor/supervisor.go`
- Modify: `internal/codexappgateway/supervisor/supervisor_test.go`
- Modify: `internal/codexappgateway/supervisor/reaper_test.go`

- [ ] **Step 1: Reduce `Key` to single field**

In `internal/codexappgateway/supervisor/supervisor.go`, replace the `Key` definition:

```go
// Key identifies one workspace's codex app-server subprocess. The
// codex-app-server process internally manages multiple threads via its
// own JSON-RPC protocol; the gateway does not see thread IDs.
type Key struct {
	WorkspaceID string
}
```

The rest of `supervisor.go` already uses `Key` as an opaque map key — no other changes required in the file body.

- [ ] **Step 2: Update test fixtures**

In `internal/codexappgateway/supervisor/supervisor_test.go` and `reaper_test.go`, replace every `Key{WorkspaceID: "ws_a", ThreadID: "thr_..."}` with `Key{WorkspaceID: "ws_a"}` (or distinct workspaces if the test relies on parallelism — pick `ws_a`/`ws_b`/etc.).

- [ ] **Step 3: Run tests**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/supervisor/ -v
```
Expected: PASS (build first, then runtime). Existing fake-codex tests don't care about Key shape.

- [ ] **Step 4: Commit**

```bash
git add internal/codexappgateway/supervisor/
git commit -m "refactor(codex-app-gateway/supervisor): Key now {WorkspaceID} only"
```

---

### Task 10: codexhome key + S3 layout reduction

**Files:**
- Modify: `internal/codexappgateway/codexhome/codexhome.go`
- Modify: `internal/codexappgateway/codexhome/codexhome_test.go`
- Modify: `internal/codexappgateway/codexhome/s3.go`
- Modify: `internal/codexappgateway/codexhome/s3_test.go`

- [ ] **Step 1: Update `Manager.NewTmpDir` signature**

In `codexhome.go`:

```go
// NewTmpDir creates `<root>/<workspaceID>/` with mode 0700. Idempotent.
func (m *Manager) NewTmpDir(workspaceID string) (string, error) {
	if workspaceID == "" {
		return "", fmt.Errorf("codexhome: empty workspace id")
	}
	d := filepath.Join(m.root, workspaceID)
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", d, err)
	}
	if err := os.Chmod(d, 0o700); err != nil {
		return "", fmt.Errorf("chmod %s: %w", d, err)
	}
	return d, nil
}
```

(Drop the threadID parameter and the second filepath segment.)

- [ ] **Step 2: Update `S3Backend`**

In `s3.go`:

```go
type S3Backend struct {
	store       ObjectStore
	workspaceID string
}

func NewS3Backend(store ObjectStore, workspaceID string) *S3Backend {
	return &S3Backend{store: store, workspaceID: workspaceID}
}

// Key is the S3 object key. New layout: codex-app-gateway/<workspace>.tar.gz.
func (b *S3Backend) Key() string {
	return fmt.Sprintf("codex-app-gateway/%s.tar.gz", b.workspaceID)
}
```

- [ ] **Step 3: Update tests**

`codexhome_test.go`: every `NewTmpDir("ws_a", "thr_1")` becomes `NewTmpDir("ws_a")`. Path-prefix assertions move from `filepath.Join(root, "ws_a", "thr_1")` to `filepath.Join(root, "ws_a")`.

`s3_test.go`: every `NewS3Backend(store, "ws_a", "thr_1")` becomes `NewS3Backend(store, "ws_a")`. Key assertion `codex-app-gateway/ws_a/thr_1.tar.gz` becomes `codex-app-gateway/ws_a.tar.gz`.

- [ ] **Step 4: Run tests**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/codexhome/ -v
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/codexappgateway/codexhome/
git commit -m "refactor(codex-app-gateway/codexhome): per-workspace tmpdir + S3 key"
```

---

### Task 11: Supervisor + reaper compile-cleanup

**Files:**
- Modify: `internal/codexappgateway/supervisor/supervisor.go`
- Modify: `internal/codexappgateway/supervisor/spawn.go`
- Modify: `internal/codexappgateway/supervisor/spawn_test.go`

`supervisor.go` calls `s.cfg.HomeMgr.NewTmpDir(key.WorkspaceID, key.ThreadID)` and `codexhome.NewS3Backend(s.cfg.Store, key.WorkspaceID, key.ThreadID)` — both signatures changed in Task 10.

- [ ] **Step 1: Update call sites in `supervisor.go`**

```go
// In EnsureSubprocess:
codexHome, err := s.cfg.HomeMgr.NewTmpDir(key.WorkspaceID)
// ...
backend := codexhome.NewS3Backend(s.cfg.Store, key.WorkspaceID)
```

Same change in `Shutdown` (the `backend := codexhome.NewS3Backend(...)` line).

- [ ] **Step 2: Verify build + tests**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/supervisor/ ./internal/codexappgateway/codexhome/ -v
```
Expected: PASS.

- [ ] **Step 3: Commit**

```bash
git add internal/codexappgateway/supervisor/supervisor.go
git commit -m "refactor(codex-app-gateway/supervisor): adopt single-arg HomeMgr/S3Backend"
```

---

### Task 12: Config additions

**Files:**
- Modify: `internal/codexappgateway/config.go`
- Modify: `internal/codexappgateway/config_test.go`

- [ ] **Step 1: Add fields + env loaders**

In `internal/codexappgateway/config.go`, extend `ServeConfig`:

```go
type ServeConfig struct {
	// ... existing fields ...

	// AgentserverInternalURL is the http base for codex token verification
	// (e.g. "http://release-agentserver.namespace.svc:8080"). Required.
	AgentserverInternalURL string

	// AgentserverInternalSecret matches the agentserver's INTERNAL_API_SECRET
	// env. Sent in every verify request as X-Internal-Secret.
	AgentserverInternalSecret string
}
```

In `LoadServeConfigFromEnv`:

```go
cfg.AgentserverInternalURL = os.Getenv("CXG_AGENTSERVER_INTERNAL_URL")
cfg.AgentserverInternalSecret = os.Getenv("CXG_AGENTSERVER_INTERNAL_SECRET")
// ...
if cfg.AgentserverInternalURL == "" {
	return cfg, fmt.Errorf("CXG_AGENTSERVER_INTERNAL_URL is required")
}
if cfg.AgentserverInternalSecret == "" {
	return cfg, fmt.Errorf("CXG_AGENTSERVER_INTERNAL_SECRET is required")
}
```

`CXG_INBOUND_HMAC_SECRET` becomes optional. Replace its required-check with a no-op (still load if set; only fail if both it and AgentserverInternal are empty in the future — for this task just drop the required check).

- [ ] **Step 2: Update + add tests**

In `config_test.go`:

```go
func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("CXG_S3_ENDPOINT", "http://s3")
	t.Setenv("CXG_S3_BUCKET", "buck")
	t.Setenv("CXG_EXEC_GATEWAY_URL", "ws://exec-gw:6060")
	t.Setenv("CXG_EXEC_GATEWAY_INTERNAL_URL", "http://exec-gw:6060")
	t.Setenv("CXG_EXEC_GATEWAY_INTERNAL_SECRET", "internal-sec")
	t.Setenv("CXG_CAPTOKEN_HMAC_SECRET", "captok-sec")
	t.Setenv("CXG_AGENTSERVER_INTERNAL_URL", "http://agentserver:8080")
	t.Setenv("CXG_AGENTSERVER_INTERNAL_SECRET", "agentserver-internal-sec")
	// CXG_INBOUND_HMAC_SECRET intentionally not required anymore
}

func TestLoadServeConfig_RequiresAgentserverURL(t *testing.T) {
	setRequired(t)
	t.Setenv("CXG_AGENTSERVER_INTERNAL_URL", "")
	_, err := LoadServeConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "CXG_AGENTSERVER_INTERNAL_URL") {
		t.Fatalf("want agentserver-url-required, got %v", err)
	}
}

func TestLoadServeConfig_RequiresAgentserverSecret(t *testing.T) {
	setRequired(t)
	t.Setenv("CXG_AGENTSERVER_INTERNAL_SECRET", "")
	_, err := LoadServeConfigFromEnv()
	if err == nil || !strings.Contains(err.Error(), "CXG_AGENTSERVER_INTERNAL_SECRET") {
		t.Fatalf("want agentserver-secret-required, got %v", err)
	}
}
```

Also remove `TestLoadServeConfig_RequiresInboundSecret` (or change its assertion to "no error when empty"). The HMAC inbound secret is optional now.

- [ ] **Step 3: Run tests**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/ -run TestLoadServeConfig -v
```
Expected: all PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/codexappgateway/config.go internal/codexappgateway/config_test.go
git commit -m "feat(codex-app-gateway/config): add CXG_AGENTSERVER_INTERNAL_{URL,SECRET}"
```

---

### Task 13: Server rewire — use `RemoteVerifier`, drop `?thread_id=`, rename admin endpoint

**Files:**
- Modify: `internal/codexappgateway/server.go`

- [ ] **Step 1: Update `NewServer` to construct `RemoteVerifier`**

Replace the existing `auth: auth.NewHMAC(cfg.InboundHMACSecret),` line with:

```go
auth: auth.NewRemoteVerifier(cfg.AgentserverInternalURL, cfg.AgentserverInternalSecret),
```

- [ ] **Step 2: Update `makeBuildConfig` arity**

The current signature is `func(ctx context.Context, workspaceID, threadID string)`. Drop `threadID`:

```go
buildConfig func(ctx context.Context, workspaceID string) (codexhome.ConfigInput, error)

func makeBuildConfig(cfg ServeConfig, client connectedClient, selfBin string, logger *slog.Logger) func(context.Context, string) (codexhome.ConfigInput, error) {
	return func(ctx context.Context, workspaceID string) (codexhome.ConfigInput, error) {
		// ... body unchanged except no threadID parameter ...
	}
}
```

- [ ] **Step 3: Update `handleCodexAppWS`**

```go
func (s *Server) handleCodexAppWS(w http.ResponseWriter, r *http.Request) {
	tok, ok := auth.ExtractBearer(r)
	if !ok {
		http.Error(w, "missing Bearer", http.StatusUnauthorized)
		return
	}
	id, err := s.auth.Verify(r.Context(), tok)
	if err != nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	userWS, err := websocket.Accept(w, r, nil)
	if err != nil {
		s.logger.Warn("ws accept failed", "err", err)
		return
	}
	defer userWS.Close(websocket.StatusNormalClosure, "client closing")

	key := supervisor.Key{WorkspaceID: id.WorkspaceID}
	ctx := r.Context()
	handle, err := s.sup.EnsureSubprocess(ctx, key, func() (codexhome.ConfigInput, error) {
		return s.buildConfig(ctx, id.WorkspaceID)
	})
	if err != nil {
		s.logger.Error("ensure subprocess", "err", err, "key", key)
		_ = userWS.Close(websocket.StatusInternalError, "subprocess unavailable")
		return
	}

	childWS, _, err := websocket.Dial(ctx, handle.WSURL, &websocket.DialOptions{
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		s.logger.Error("dial child", "err", err, "url", handle.WSURL)
		_ = userWS.Close(websocket.StatusInternalError, "subprocess dial failed")
		return
	}
	defer childWS.Close(websocket.StatusNormalClosure, "gateway closing")

	s.sup.Touch(key)
	if err := wsbridge.RunProxy(ctx, userWS, childWS, func() { s.sup.Touch(key) }); err != nil {
		s.logger.Info("proxy ended", "err", err, "key", key)
	}
}
```

- [ ] **Step 4: Rename admin endpoint**

In `Routes()`, change:

```go
r.Post("/admin/threads/restart", s.handleAdminRestart)
```
to:
```go
r.Post("/admin/sessions/restart", s.handleAdminRestart)
```

In `handleAdminRestart`:

```go
var body struct {
	WorkspaceID string `json:"workspaceId"`
}
if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
	http.Error(w, "bad body", http.StatusBadRequest)
	return
}
if body.WorkspaceID == "" {
	http.Error(w, "workspaceId required", http.StatusBadRequest)
	return
}
if err := s.sup.Shutdown(r.Context(), supervisor.Key{WorkspaceID: body.WorkspaceID}); err != nil {
	http.Error(w, err.Error(), http.StatusInternalServerError)
	return
}
w.WriteHeader(http.StatusNoContent)
```

(Drop the bearer-verify call's threadID arg; pass `r.Context()` to `Verify`.)

- [ ] **Step 5: Build**

```bash
cd /root/agentserver && go build ./internal/codexappgateway/ ./cmd/codex-app-gateway/
```
Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add internal/codexappgateway/server.go
git commit -m "feat(codex-app-gateway): adopt RemoteVerifier; drop URL thread_id; rename admin endpoint"
```

---

### Task 14: Update gateway tests for new wiring

**Files:**
- Modify: `internal/codexappgateway/server_test.go`
- Modify: `internal/codexappgateway/server_testhelper_test.go`
- Modify: `internal/codexappgateway/integration_test.go`
- Modify: `internal/codexappgateway/buildconfig_test.go`

- [ ] **Step 1: Switch `makeTestServer` to a fake-agentserver `RemoteVerifier`**

In `server_test.go`:

```go
func makeTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	bin := makeFakeCodex(t)
	store := makeFakeStore(t)
	mgr := codexhome.NewManager(t.TempDir())
	sup := supervisor.NewSupervisor(supervisor.SupervisorConfig{CodexBin: bin, HomeMgr: mgr, Store: store})
	t.Cleanup(func() { sup.ShutdownAll(context.Background()) })

	// Fake agentserver: any token starting with "ast_" verifies as (u_test, ws_test).
	asSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/internal/codex/tokens/verify" {
			http.Error(w, "404", 404)
			return
		}
		var body struct{ Token string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		if !strings.HasPrefix(body.Token, "ast_") {
			http.Error(w, `{"error":"invalid_token"}`, http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"user_id": "u_test", "workspace_id": "ws_test",
		})
	}))
	t.Cleanup(asSrv.Close)

	logger := slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil))
	srv := &Server{
		cfg: ServeConfig{},
		auth: auth.NewRemoteVerifier(asSrv.URL, "ignored"),
		sup:     sup,
		homeMgr: mgr,
		logger:  logger,
		buildConfig: func(_ context.Context, ws string) (codexhome.ConfigInput, error) {
			return codexhome.ConfigInput{
				ModelProvider:  "p", Model: "m",
				ModelProviders: map[string]codexhome.ModelProvider{"p": {Name: "p", BaseURL: "http://x", EnvKey: "K", WireAPI: "responses"}},
			}, nil
		},
	}
	return httptest.NewServer(srv.Routes())
}
```

(Add `"encoding/json"`, `"strings"` to imports if not already present.)

- [ ] **Step 2: Update tests that mint HMAC tokens**

Replace `auth.NewHMAC([]byte("test-secret")).Mint("ws_a", "thr_1")` with literal `"ast_dummytoken_anything"` (the fake handler accepts any `ast_` prefix).

- [ ] **Step 3: Update `TestServer_AdminRestart_KillsSubprocess`**

Change body to `{"workspaceId":"ws_test"}` and PATH to `/admin/sessions/restart`.

- [ ] **Step 4: Update `integration_test.go`**

Same fixture switch — fake-agentserver httptest server returning `(usr_int, ws_int)` for any `ast_*` token.

- [ ] **Step 5: Update `buildconfig_test.go`**

`makeBuildConfig` arity changed:

```go
build := makeBuildConfig(cfg, stub, "/usr/local/bin/codex-app-gateway", newDiscardLogger())
got, err := build(context.Background(), "ws_a")  // dropped thread arg
```

Drop any thread-id assertions.

- [ ] **Step 6: Run all gateway tests**

```bash
cd /root/agentserver && go test ./internal/codexappgateway/... ./cmd/codex-app-gateway/ -v
```
Expected: all PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/codexappgateway/server_test.go \
        internal/codexappgateway/server_testhelper_test.go \
        internal/codexappgateway/integration_test.go \
        internal/codexappgateway/buildconfig_test.go
git commit -m "test(codex-app-gateway): switch fixtures to RemoteVerifier; drop thread_id"
```

---

## Phase 3 — chart wiring + UI + version bump + supersede headers

### Task 15: Chart template additions

**Files:**
- Modify: `deploy/helm/agentserver/templates/codex-app-gateway.yaml`

- [ ] **Step 1: Add the two env vars**

Inside the codex-app-gateway container `env:` block (after the existing `CXG_*` entries), append:

```yaml
            - name: CXG_AGENTSERVER_INTERNAL_URL
              value: "http://{{ .Release.Name }}.{{ .Release.Namespace }}.svc:{{ .Values.service.port }}"
            - name: CXG_AGENTSERVER_INTERNAL_SECRET
              value: {{ required "internal.apiSecret is required when codexAppGateway.enabled is true" .Values.internal.apiSecret | quote }}
```

- [ ] **Step 2: Render-test**

```bash
helm template t deploy/helm/agentserver/ \
  --set codexAppGateway.enabled=true \
  --set codexAppGateway.codexApiKey=test \
  --set codexAppGateway.s3.endpoint=http://m \
  --set codexAppGateway.s3.bucket=b \
  --set codexAppGateway.s3.existingSecret=ms \
  --set codexExecGateway.enabled=true \
  --set internal.apiSecret=test-internal \
  | grep -A1 "CXG_AGENTSERVER_INTERNAL"
```
Expected: both env entries rendered with correct values.

- [ ] **Step 3: Commit**

```bash
git add deploy/helm/agentserver/templates/codex-app-gateway.yaml
git commit -m "feat(chart): pass agentserver internal URL+secret to codex-app-gateway"
```

---

### Task 16: API client functions

**Files:**
- Modify: `web/src/lib/api.ts`

- [ ] **Step 1: Add types + functions**

Append to `web/src/lib/api.ts`:

```typescript
export interface CodexToken {
  id: string
  name: string
  workspace_id: string
  created_at: string
  expires_at: string
  last_used_at?: string
  revoked: boolean
  revoked_at?: string
}

export interface MintCodexTokenRequest {
  workspace_id: string
  name: string
  ttl_days?: number
}

export interface MintCodexTokenResponse {
  id: string
  token: string
  name: string
  workspace_id: string
  expires_at: string
  created_at: string
}

export async function listCodexTokens(workspaceId: string): Promise<CodexToken[]> {
  const res = await fetch(`/api/codex/tokens?workspace_id=${encodeURIComponent(workspaceId)}`)
  if (!res.ok) throw new Error('Failed to list codex tokens')
  return res.json()
}

export async function mintCodexToken(req: MintCodexTokenRequest): Promise<MintCodexTokenResponse> {
  const res = await fetch('/api/codex/tokens', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
  })
  if (!res.ok) {
    const text = await res.text()
    throw new Error(text || 'Failed to mint codex token')
  }
  return res.json()
}

export async function revokeCodexToken(id: string): Promise<void> {
  const res = await fetch(`/api/codex/tokens/${encodeURIComponent(id)}`, { method: 'DELETE' })
  if (!res.ok) throw new Error('Failed to revoke codex token')
}
```

- [ ] **Step 2: Build the frontend**

```bash
cd /root/agentserver/web && pnpm build
```
Expected: clean type-check.

- [ ] **Step 3: Commit**

```bash
git add web/src/lib/api.ts
git commit -m "feat(web/api): list/mint/revoke codex tokens"
```

---

### Task 17: `CodexTokensPanel` component

**Files:**
- Create: `web/src/components/CodexTokensPanel.tsx`
- Modify: `web/src/components/WorkspaceDetail.tsx`

- [ ] **Step 1: Implement the panel**

`web/src/components/CodexTokensPanel.tsx`:
```tsx
import { useState, useEffect, useCallback } from 'react'
import { Plus, Trash2, Copy, Check, X } from 'lucide-react'
import {
  CodexToken, MintCodexTokenResponse,
  listCodexTokens, mintCodexToken, revokeCodexToken,
} from '../lib/api'

interface Props {
  workspaceId: string
}

const TTL_OPTIONS = [1, 7, 30, 90, 180, 365] as const

export default function CodexTokensPanel({ workspaceId }: Props) {
  const [tokens, setTokens] = useState<CodexToken[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const [showMint, setShowMint] = useState(false)
  const [newName, setNewName] = useState('')
  const [newTTL, setNewTTL] = useState<number>(90)
  const [generated, setGenerated] = useState<MintCodexTokenResponse | null>(null)
  const [copied, setCopied] = useState(false)

  const refresh = useCallback(async () => {
    setLoading(true)
    try {
      const rows = await listCodexTokens(workspaceId)
      setTokens(rows)
      setError(null)
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    } finally {
      setLoading(false)
    }
  }, [workspaceId])

  useEffect(() => { void refresh() }, [refresh])

  const onMint = async () => {
    try {
      const resp = await mintCodexToken({
        workspace_id: workspaceId,
        name: newName,
        ttl_days: newTTL,
      })
      setGenerated(resp)
      setShowMint(false)
      setNewName('')
      setNewTTL(90)
      void refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const onRevoke = async (id: string) => {
    if (!confirm('Revoke this token? Any active codex --remote sessions using it will be cut at next reconnect.')) return
    try {
      await revokeCodexToken(id)
      void refresh()
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e))
    }
  }

  const copyToken = async () => {
    if (!generated) return
    await navigator.clipboard.writeText(generated.token)
    setCopied(true)
    setTimeout(() => setCopied(false), 1500)
  }

  return (
    <div className="space-y-4">
      <div className="flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold">Codex Remote Access</h3>
          <p className="text-sm text-gray-500">
            Use these tokens with{' '}
            <code className="px-1 bg-gray-100 rounded">
              codex --remote wss://&lt;host&gt;/codex-app/ws --remote-auth-token-env &lt;ENV_VAR&gt;
            </code>
          </p>
        </div>
        <button
          onClick={() => setShowMint(true)}
          className="flex items-center gap-1 px-3 py-1.5 bg-blue-600 text-white rounded hover:bg-blue-700"
        >
          <Plus size={16} />
          Generate token
        </button>
      </div>

      {error && <div className="p-2 bg-red-50 text-red-700 text-sm rounded">{error}</div>}

      {loading ? (
        <div className="text-gray-500">Loading…</div>
      ) : tokens.length === 0 ? (
        <div className="text-gray-500 italic">No tokens yet.</div>
      ) : (
        <table className="w-full text-sm">
          <thead className="text-left text-gray-500 border-b">
            <tr>
              <th className="py-2">Name</th>
              <th className="py-2">Created</th>
              <th className="py-2">Expires</th>
              <th className="py-2">Last used</th>
              <th className="py-2"></th>
            </tr>
          </thead>
          <tbody>
            {tokens.map(t => (
              <tr key={t.id} className="border-b last:border-0">
                <td className="py-2">{t.name}</td>
                <td className="py-2">{new Date(t.created_at).toLocaleDateString()}</td>
                <td className="py-2">{new Date(t.expires_at).toLocaleDateString()}</td>
                <td className="py-2">
                  {t.last_used_at ? new Date(t.last_used_at).toLocaleString() : <span className="text-gray-400">never</span>}
                </td>
                <td className="py-2 text-right">
                  <button
                    onClick={() => onRevoke(t.id)}
                    className="text-red-600 hover:text-red-800"
                    aria-label="Revoke token"
                  >
                    <Trash2 size={16} />
                  </button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {showMint && (
        <div className="fixed inset-0 z-50 bg-black/30 flex items-center justify-center">
          <div className="bg-white rounded shadow-lg p-6 w-96 space-y-3">
            <div className="flex items-center justify-between">
              <h4 className="font-semibold">Generate codex token</h4>
              <button onClick={() => setShowMint(false)}><X size={16} /></button>
            </div>
            <label className="block text-sm">
              Name
              <input
                type="text"
                value={newName}
                onChange={e => setNewName(e.target.value)}
                className="mt-1 w-full border rounded px-2 py-1"
                placeholder="my mac"
              />
            </label>
            <label className="block text-sm">
              TTL (days)
              <select
                value={newTTL}
                onChange={e => setNewTTL(parseInt(e.target.value, 10))}
                className="mt-1 w-full border rounded px-2 py-1"
              >
                {TTL_OPTIONS.map(d => <option key={d} value={d}>{d}</option>)}
              </select>
            </label>
            <div className="flex justify-end gap-2 pt-2">
              <button onClick={() => setShowMint(false)} className="px-3 py-1 border rounded">Cancel</button>
              <button
                onClick={onMint}
                disabled={!newName.trim()}
                className="px-3 py-1 bg-blue-600 text-white rounded disabled:opacity-50"
              >
                Generate
              </button>
            </div>
          </div>
        </div>
      )}

      {generated && (
        <div className="fixed inset-0 z-50 bg-black/30 flex items-center justify-center">
          <div className="bg-white rounded shadow-lg p-6 w-[36rem] space-y-3">
            <h4 className="font-semibold text-green-700">✓ Token generated</h4>
            <p className="text-sm text-gray-700">
              Copy it now — you won't see it again.
            </p>
            <div className="flex items-center gap-2">
              <code className="flex-1 px-2 py-2 bg-gray-100 rounded font-mono text-xs break-all">
                {generated.token}
              </code>
              <button
                onClick={copyToken}
                className="p-2 border rounded hover:bg-gray-50"
                aria-label="Copy token"
              >
                {copied ? <Check size={16} /> : <Copy size={16} />}
              </button>
            </div>
            <pre className="text-xs bg-gray-50 p-2 rounded overflow-x-auto">{`export AGENTSERVER_TOKEN='${generated.token}'
codex --remote wss://<host>/codex-app/ws \\
      --remote-auth-token-env AGENTSERVER_TOKEN`}</pre>
            <div className="flex justify-end pt-2">
              <button
                onClick={() => setGenerated(null)}
                className="px-3 py-1 bg-blue-600 text-white rounded"
              >
                I've saved it
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}
```

- [ ] **Step 2: Mount in `WorkspaceDetail.tsx`**

In `web/src/components/WorkspaceDetail.tsx`, find the existing settings/tab area (search for the existing `<Settings />` or members section) and add a new tab/section that renders `<CodexTokensPanel workspaceId={...} />`. Match the local pattern — if the file uses tabs keyed by a string state variable, add a `'codex'` tab; otherwise insert as a sibling card.

Minimal-friction insertion (no tab refactor): add at the bottom of the existing settings group:

```tsx
import CodexTokensPanel from './CodexTokensPanel'

// inside the JSX:
<section className="mt-6 p-4 border rounded">
  <CodexTokensPanel workspaceId={workspace.id} />
</section>
```

(Where `workspace.id` is the workspace state already in scope.)

- [ ] **Step 3: Build**

```bash
cd /root/agentserver/web && pnpm build
```
Expected: clean.

- [ ] **Step 4: Commit**

```bash
git add web/src/components/CodexTokensPanel.tsx web/src/components/WorkspaceDetail.tsx
git commit -m "feat(web): codex remote-access tokens panel under workspace settings"
```

---

### Task 18: Chart bump + supersede headers

**Files:**
- Modify: `deploy/helm/agentserver/Chart.yaml`
- Modify: `docs/superpowers/specs/2026-05-10-codex-app-gateway-subprocess.md`
- Modify: `docs/superpowers/plans/2026-05-11-codex-app-gateway-subprocess.md`

- [ ] **Step 1: Bump chart**

Edit `Chart.yaml`:
```yaml
version: 0.50.0
appVersion: "0.50.0"
```

- [ ] **Step 2: Add SUPERSEDED header to old spec**

At the very top of `docs/superpowers/specs/2026-05-10-codex-app-gateway-subprocess.md`, before the existing first heading, prepend:

```markdown
> **SUPERSEDED 2026-05-15.** The supervisor key + inbound auth model are
> superseded by `2026-05-15-codex-app-gateway-oauth-bridge-design.md`.
> The subprocess lifecycle, S3 round-trip, ws frame proxy, and reaper
> sections of THIS document remain in force.
```

- [ ] **Step 3: Add SUPERSEDED header to old plan**

At the very top of `docs/superpowers/plans/2026-05-11-codex-app-gateway-subprocess.md`, prepend the same warning, replacing "design" with "plan":

```markdown
> **PARTIALLY SUPERSEDED 2026-05-15.** Tasks that touch supervisor key or
> inbound auth (`Identity{ThreadID}`, HMAC-only inbound) are superseded
> by `2026-05-15-codex-app-gateway-oauth-bridge.md`. Subprocess lifecycle,
> S3 round-trip, ws frame proxy, idle reaper tasks remain valid.
```

- [ ] **Step 4: Commit**

```bash
git add deploy/helm/agentserver/Chart.yaml \
        docs/superpowers/specs/2026-05-10-codex-app-gateway-subprocess.md \
        docs/superpowers/plans/2026-05-11-codex-app-gateway-subprocess.md
git commit -m "chore(chart): bump to 0.50.0 + supersede headers on old codex-app-gateway plan/spec"
```

---

### Task 19: Production data cleanup precondition + final smoke

**Files:** none (operational task — recorded in plan for completeness)

- [ ] **Step 1: List existing per-thread tarballs**

```bash
# Replace bucket name with the real value from values.yaml; runs against the cluster's S3.
aws s3 ls s3://<bucket>/codex-app-gateway/ --recursive
```
Expected: zero or a small handful — confirm none correspond to real user traffic.

- [ ] **Step 2: Remove old layout (if any)**

```bash
aws s3 rm s3://<bucket>/codex-app-gateway/ --recursive
```
Expected: completion message; subsequent `pulumi up` writes the new `<workspace>.tar.gz` layout fresh.

- [ ] **Step 3: Tag + push (release pipeline runs)**

```bash
cd /root/agentserver
git push github main
git push github v0.50.0
```

The GHA `Build and Publish` workflow runs `test` + 12 image builds + `publish-helm`. Wait for green; chart `oci://ghcr.io/agentserver/charts/agentserver:0.50.0` becomes available.

- [ ] **Step 4: Pulumi pin + apply**

In `/root/k8s/stacks/agentserver.ts`, bump the `version: "0.49.x"` line to `"0.50.0"`. Then:

```bash
cd /root/k8s && pulumi up --stack nj-prod \
  --target 'urn:pulumi:nj-prod::k8s::kubernetes:helm.sh/v3:Release::helm-agentserver'
```

Expected: helm release upgraded; `codex_remote_tokens` migration applied automatically; `codex-app-gateway` pod restarted with new `CXG_AGENTSERVER_INTERNAL_*` env.

- [ ] **Step 5: End-to-end smoke**

1. Open the agentserver web UI in a browser; open a workspace; navigate to the new "Codex Remote Access" panel
2. Click "Generate token" → name it "smoke", TTL 1 day
3. Copy the displayed `ast_*` token
4. From a developer machine:
   ```bash
   export AS_TOKEN='ast_...copied...'
   codex --remote wss://platform.agentserver.dev/codex-app/ws --remote-auth-token-env AS_TOKEN
   ```
5. Verify the codex TUI starts, `thread/list` returns whatever's in sqlite for that workspace (likely empty), and `thread/start` creates a new thread
6. In another terminal: `kubectl logs deploy/agentserver-codex-app-gateway` should show `auth verified user_id=usr_… workspace_id=ws_…` (or equivalent)
7. Revoke the token via web UI → close + reopen codex with the same env → should fail at ws upgrade with 401

If any step fails, surface the failure mode + roll back chart pin (`pulumi config` or revert pulumi script + `pulumi up` again).

---

## Self-Review

**Spec coverage matrix:**

| Spec section | Task |
|---|---|
| Data model — `codex_remote_tokens` table + indexes | Task 1 |
| Data model — token format `ast_<id>_<secret>` | Task 3 |
| Data model — TTL defaults + revoke semantics | Tasks 2, 4 |
| HTTP API — POST /api/codex/tokens (mint) | Task 4 |
| HTTP API — GET /api/codex/tokens (list) | Task 4 |
| HTTP API — DELETE /api/codex/tokens/{id} (revoke) | Task 4 |
| HTTP API — POST /api/internal/codex/tokens/verify | Task 5 |
| HTTP API — wire all 4 routes in server.go | Task 6 |
| HTTP API — internal verify uses INTERNAL_API_SECRET (X-Internal-Secret) | Task 6 (deviation noted in plan header) |
| codex-app-gateway — Identity drops ThreadID | Task 7 |
| codex-app-gateway — Authenticator interface | Task 7 |
| codex-app-gateway — RemoteVerifier impl | Task 8 |
| codex-app-gateway — supervisor.Key reduction | Task 9 |
| codex-app-gateway — codexhome S3 layout change | Task 10 |
| codex-app-gateway — supervisor + spawn call sites | Task 11 |
| codex-app-gateway — config additions | Task 12 |
| codex-app-gateway — handleCodexAppWS rewrite | Task 13 |
| codex-app-gateway — admin endpoint rename | Task 13 |
| codex-app-gateway — fixture updates | Task 14 |
| Web UI — workspace settings panel | Tasks 16, 17 |
| Chart — env additions | Task 15 |
| Chart — bump 0.50.0 | Task 18 |
| Spec/plan supersede markers | Task 18 |
| Rollout — S3 cleanup + tag + pulumi | Task 19 |

**Placeholder scan:** none of the steps say "TBD", "TODO", "appropriate", "similar to". Every step has either a code block (impl) or an exact command (verify/build/test/commit).

**Type consistency check:**

| Symbol | Defined in task | Used in task |
|---|---|---|
| `CodexToken` (Go struct) | Task 2 | Tasks 4, 5 |
| `generateCodexToken` / `parseCodexToken` | Task 3 | Tasks 4, 5 |
| `handleMintCodexToken` / `handleListCodexTokens` / `handleRevokeCodexToken` | Task 4 | Task 6 |
| `handleVerifyCodexToken` | Task 5 | Task 6 |
| `Identity{UserID, WorkspaceID}` | Task 7 | Tasks 8, 13 |
| `Authenticator.Verify(ctx, token)` | Task 7 | Tasks 8, 13 |
| `RemoteVerifier` / `NewRemoteVerifier` / `ErrUnauthorized` | Task 8 | Tasks 13, 14 |
| `Key{WorkspaceID}` | Task 9 | Tasks 11, 13 |
| `Manager.NewTmpDir(workspaceID)` | Task 10 | Task 11 |
| `NewS3Backend(store, workspaceID)` | Task 10 | Task 11 |
| `ServeConfig.AgentserverInternal{URL,Secret}` + envs | Task 12 | Tasks 13, 15 |
| `CodexToken` (TS interface) | Task 16 | Task 17 |
| `mintCodexToken` / `listCodexTokens` / `revokeCodexToken` (TS) | Task 16 | Task 17 |

All identifiers match across their definition + use sites.

**Scope check:** Tasks 1–6 produce a self-contained agentserver feature (commits can ship independently — the new endpoints work even if the gateway never uses them). Tasks 7–14 are coupled refactors of one Go module; they all need to land together for the gateway to build+work. Tasks 15–19 are wiring + ops. Three logical phases, one PR is acceptable.
