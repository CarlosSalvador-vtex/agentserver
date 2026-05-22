# Unified `expires_at` Across `ask_` and `ast_` Tokens Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the `ttl_days` int field on codex token mint requests with a unified `expires_at` RFC3339 string field across both `ask_*` (workspace API keys) and `ast_*` (codex remote tokens), and add expiration support to workspace API keys which currently have none.

**Architecture:** A shared `resolveExpiresAt` helper in `internal/server/expiration.go` enforces the validation rules (default 90d, cap 365d, reject past). The DB layer for `workspace_api_keys` gets `expires_at` plumbed through all three query functions. Both mint handlers call `resolveExpiresAt` instead of the old `ttl_days` block. Frontend modals compute an ISO timestamp from a "days" dropdown and pass it as `expires_at`.

**Tech Stack:** Go (database/sql, chi, standard library), PostgreSQL (via lib/pq), React/TypeScript (openapi-typescript generated types), swaggo for OpenAPI annotations.

---

## File Map

| File | Change |
|------|--------|
| `internal/db/migrations/030_workspace_api_keys_expiration.sql` | Already created — review only |
| `internal/db/workspace_api_keys.go` | Plumb `expires_at` into INSERT, SELECT, WHERE |
| `internal/db/workspace_api_keys_test.go` | Add `ExpiresAt` to fixtures; add `TestWorkspaceAPIKey_ValidateExpired` |
| `internal/server/expiration.go` | New: shared `resolveExpiresAt` helper |
| `internal/server/expiration_test.go` | New: unit tests for `resolveExpiresAt` |
| `internal/server/api_types.go` | `CodexTokenMintRequest` TTLDays→ExpiresAt; add ExpiresAt to 3 workspace-key types |
| `internal/server/codex_tokens.go` | Remove TTL constants; use `resolveExpiresAt` |
| `internal/server/codex_tokens_test.go` | Update tests: `expires_at` instead of `ttl_days` |
| `internal/server/workspace_api_keys.go` | Add `resolveExpiresAt` call; pass ExpiresAt to DB + response |
| `internal/server/workspace_api_keys_test.go` | Update `mintKeyViaHandler`; add 4 new expiration tests |
| `web/src/lib/api.ts` | Add `expiresAt?` param to `mintWorkspaceAPIKey`; `mintCodexToken` passes `expires_at` |
| `web/src/components/MintAPIKeyModal.tsx` | Add expiry dropdown; pass `expiresAt` to `mintWorkspaceAPIKey` |
| `web/src/components/CodexTokensPanel.tsx` | Replace `TTL_OPTIONS`/`newTTL`/`ttl_days` with `expiresAt` pattern |
| `web/src/components/WorkspaceAPIKeysTab.tsx` | Add "Expires" column with relative-time badge |

---

## Task 1: Review migration 030 and verify it's correct

**Files:**
- Review: `internal/db/migrations/030_workspace_api_keys_expiration.sql`

- [ ] **Step 1: Read the migration**

```bash
cat /root/agentserver/internal/db/migrations/030_workspace_api_keys_expiration.sql
```

Expected content — it should ADD the `expires_at TIMESTAMPTZ` column, backfill with `NOW() + INTERVAL '90 days'`, then `SET NOT NULL`. If the file matches this, no changes needed. If it's missing or different, fix it to match:

```sql
ALTER TABLE workspace_api_keys
    ADD COLUMN expires_at TIMESTAMPTZ;

UPDATE workspace_api_keys
   SET expires_at = NOW() + INTERVAL '90 days'
 WHERE expires_at IS NULL;

ALTER TABLE workspace_api_keys
    ALTER COLUMN expires_at SET NOT NULL;
```

- [ ] **Step 2: Verify no migration 030 conflicts exist**

```bash
ls /root/agentserver/internal/db/migrations/030_*.sql
```

Expected: exactly one file, `030_workspace_api_keys_expiration.sql`. Done — no commit needed for this task.

---

## Task 2: Create `internal/server/expiration.go` with `resolveExpiresAt`

**Files:**
- Create: `internal/server/expiration.go`
- Create: `internal/server/expiration_test.go`

- [ ] **Step 1: Write the failing tests first**

Create `/root/agentserver/internal/server/expiration_test.go`:

```go
package server

import (
	"testing"
	"time"
)

func TestResolveExpiresAt_Empty_DefaultsTo90Days(t *testing.T) {
	before := time.Now().UTC()
	got, err := resolveExpiresAt("")
	after := time.Now().UTC()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	lo := before.Add(expirationDefaultDuration)
	hi := after.Add(expirationDefaultDuration)
	if got.Before(lo) || got.After(hi) {
		t.Fatalf("got %v, want in [%v, %v]", got, lo, hi)
	}
}

func TestResolveExpiresAt_ValidTimestamp(t *testing.T) {
	ts := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	raw := ts.Format(time.RFC3339)
	got, err := resolveExpiresAt(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.Equal(ts) {
		t.Fatalf("got %v, want %v", got, ts)
	}
}

func TestResolveExpiresAt_RejectsInvalidFormat(t *testing.T) {
	_, err := resolveExpiresAt("2026-08-20")
	if err == nil {
		t.Fatal("expected error for non-RFC3339 string")
	}
}

func TestResolveExpiresAt_RejectsPastTimestamp(t *testing.T) {
	past := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339)
	_, err := resolveExpiresAt(past)
	if err == nil {
		t.Fatal("expected error for past timestamp")
	}
}

func TestResolveExpiresAt_RejectsMoreThan365Days(t *testing.T) {
	future := time.Now().UTC().Add(400 * 24 * time.Hour).Format(time.RFC3339)
	_, err := resolveExpiresAt(future)
	if err == nil {
		t.Fatal("expected error for >365d future timestamp")
	}
}

func TestResolveExpiresAt_AcceptsClockSkewBoundary(t *testing.T) {
	// 30 seconds in the past is within the 1-minute clock skew tolerance.
	nearPast := time.Now().UTC().Add(-30 * time.Second).Format(time.RFC3339)
	_, err := resolveExpiresAt(nearPast)
	if err != nil {
		t.Fatalf("expected no error within clock skew, got: %v", err)
	}
}

func TestResolveExpiresAt_RejectsJustBeyondClockSkew(t *testing.T) {
	// 2 minutes in the past exceeds the 1-minute clock skew tolerance.
	beyondSkew := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339)
	_, err := resolveExpiresAt(beyondSkew)
	if err == nil {
		t.Fatal("expected error for timestamp beyond clock skew")
	}
}

func TestResolveExpiresAt_ResultIsUTC(t *testing.T) {
	// Supply a timestamp with timezone offset — result must be UTC.
	ts := time.Now().Add(10 * 24 * time.Hour)
	// Use a +05:30 offset string by adding 5.5h and formatting with a fixed zone
	loc := time.FixedZone("IST", 5*3600+30*60)
	raw := ts.In(loc).Format(time.RFC3339)
	got, err := resolveExpiresAt(raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Location() != time.UTC {
		t.Fatalf("expected UTC, got %v", got.Location())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail (function not yet defined)**

```bash
cd /root/agentserver && go test ./internal/server/ -run "TestResolveExpiresAt" -count=1 2>&1 | head -20
```

Expected: compile error — `resolveExpiresAt undefined` and `expirationDefaultDuration undefined`.

- [ ] **Step 3: Create `internal/server/expiration.go`**

```go
package server

import (
	"errors"
	"time"
)

const (
	expirationDefaultDuration = 90 * 24 * time.Hour
	expirationMaxDuration     = 365 * 24 * time.Hour
	expirationClockSkew       = 1 * time.Minute // tolerance for "in the past"
)

// resolveExpiresAt parses a client-supplied RFC3339 timestamp into a UTC
// time.Time. When raw is empty, returns NOW + 90 days. Returns an error
// string suitable for an HTTP 422 response when:
//   - the string is not RFC3339-parseable
//   - the parsed time is in the past (beyond clock-skew tolerance)
//   - the parsed time is more than 365 days in the future
//
// All returned times are in UTC.
func resolveExpiresAt(raw string) (time.Time, error) {
	now := time.Now().UTC()
	if raw == "" {
		return now.Add(expirationDefaultDuration), nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, errors.New("expires_at must be an RFC3339 timestamp")
	}
	t = t.UTC()
	if t.Before(now.Add(-expirationClockSkew)) {
		return time.Time{}, errors.New("expires_at is in the past")
	}
	if t.After(now.Add(expirationMaxDuration)) {
		return time.Time{}, errors.New("expires_at is more than 365 days in the future")
	}
	return t, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
cd /root/agentserver && go test ./internal/server/ -run "TestResolveExpiresAt" -count=1 -v 2>&1 | tail -20
```

Expected: all 7 `TestResolveExpiresAt_*` tests pass.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add internal/server/expiration.go internal/server/expiration_test.go
git commit -m "feat(api-keys): add resolveExpiresAt helper with unit tests"
```

---

## Task 3: Plumb `expires_at` through DB layer (`workspace_api_keys.go`)

**Files:**
- Modify: `internal/db/workspace_api_keys.go`
- Modify: `internal/db/workspace_api_keys_test.go`

- [ ] **Step 1: Add `ExpiresAt` to DB test fixtures and add expired-key test**

In `internal/db/workspace_api_keys_test.go`, every `WorkspaceAPIKey{}` literal that calls `CreateWorkspaceAPIKey` is currently missing `ExpiresAt`. Add `ExpiresAt: time.Now().Add(time.Hour)` to each one. Then append the new `TestWorkspaceAPIKey_ValidateExpired` test.

Find all occurrences of `WorkspaceAPIKey{` in the test file. The fixtures are:
- `TestWorkspaceAPIKey_CreateAndGet` — key named `"wak_testcreate"`
- `TestWorkspaceAPIKey_ValidateHashMatch` — key named `"wak_hashtest1"`
- `TestWorkspaceAPIKey_ValidateHashMismatch` — key named `"wak_hashmism1"`
- `TestWorkspaceAPIKey_ValidateRevoked` — key named `"wak_revoktest"`
- `TestWorkspaceAPIKeys_ListExcludesSecretHash` — key named `"wak_listsecret"`
- `TestWorkspaceAPIKey_TouchLastUsed` — key named `"wak_touchtest1"`
- `TestWorkspaceAPIKey_MultipleScopes` — key named `"wak_multiscope"`

For each, add `ExpiresAt: time.Now().Add(time.Hour),` after the `Scopes` line.

Then add the new test at the end of the file:

```go
func TestWorkspaceAPIKey_ValidateExpired(t *testing.T) {
	d := newTestDB(t)
	ctx := context.Background()
	wsID, uID := setupAPIKeyFixtures(t, d)

	secret := "wak_expirtest_secretvalue0000000000000000"
	key := WorkspaceAPIKey{
		ID:          "wak_expirtest",
		WorkspaceID: wsID,
		UserID:      uID,
		Name:        "expire-test",
		Prefix:      "wak_expirtest",
		SecretHash:  makeHash(secret),
		Scopes:      []string{"turns:submit"},
		ExpiresAt:   time.Now().Add(-time.Hour), // already expired
	}
	if err := d.CreateWorkspaceAPIKey(ctx, key); err != nil {
		t.Fatalf("CreateWorkspaceAPIKey: %v", err)
	}

	_, err := d.ValidateWorkspaceAPIKeySecret(ctx, "wak_expirtest", secret)
	if err != sql.ErrNoRows {
		t.Fatalf("want sql.ErrNoRows for expired key, got %v", err)
	}
}
```

- [ ] **Step 2: Run the DB tests to verify they fail**

```bash
cd /root/agentserver && go test ./internal/db/ -run "TestWorkspaceAPIKey" -count=1 2>&1 | tail -30
```

Expected: compile errors because `ExpiresAt` is now being passed to `CreateWorkspaceAPIKey` but the INSERT doesn't include it yet — or the DB migration hasn't run. The test binary will compile (the struct field exists) but `CreateWorkspaceAPIKey` will fail at runtime when `expires_at` column is NOT NULL without a default after migration 030 applies.

Actually the tests will compile fine (the struct field is already there). They'll fail at runtime because `INSERT` omits `expires_at` which is NOT NULL (after migration 030 runs against the test DB). Confirm the test setup runs migrations.

- [ ] **Step 3: Update `CreateWorkspaceAPIKey` to include `expires_at`**

In `internal/db/workspace_api_keys.go`, replace the INSERT in `CreateWorkspaceAPIKey`:

Old:
```go
	_, err := db.ExecContext(ctx, `
		INSERT INTO workspace_api_keys
		    (id, workspace_id, user_id, name, prefix, secret_hash, scopes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		k.ID, k.WorkspaceID, k.UserID, k.Name, k.Prefix, k.SecretHash, pq.Array(scopes))
```

New:
```go
	_, err := db.ExecContext(ctx, `
		INSERT INTO workspace_api_keys
		    (id, workspace_id, user_id, name, prefix, secret_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		k.ID, k.WorkspaceID, k.UserID, k.Name, k.Prefix, k.SecretHash, pq.Array(scopes), k.ExpiresAt)
```

- [ ] **Step 4: Update `ListWorkspaceAPIKeys` to select and scan `expires_at`**

In `ListWorkspaceAPIKeys`, replace the SELECT query and scan:

Old SELECT:
```go
	rows, err := db.QueryContext(ctx, `
		SELECT id, workspace_id, user_id, name, prefix, scopes,
		       created_at, last_used_at, revoked_at
		  FROM workspace_api_keys
		 WHERE workspace_id = $1
		 ORDER BY created_at DESC`, workspaceID)
```

New SELECT:
```go
	rows, err := db.QueryContext(ctx, `
		SELECT id, workspace_id, user_id, name, prefix, scopes,
		       created_at, expires_at, last_used_at, revoked_at
		  FROM workspace_api_keys
		 WHERE workspace_id = $1
		 ORDER BY created_at DESC`, workspaceID)
```

Old scan:
```go
		if err := rows.Scan(&k.ID, &k.WorkspaceID, &k.UserID, &k.Name, &k.Prefix, &scopes,
			&k.CreatedAt, &lastUsed, &revoked); err != nil {
```

New scan:
```go
		if err := rows.Scan(&k.ID, &k.WorkspaceID, &k.UserID, &k.Name, &k.Prefix, &scopes,
			&k.CreatedAt, &k.ExpiresAt, &lastUsed, &revoked); err != nil {
```

- [ ] **Step 5: Update `ValidateWorkspaceAPIKeySecret` to filter expired keys**

Replace the SELECT query in `ValidateWorkspaceAPIKeySecret`:

Old:
```go
	row := db.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, name, prefix, secret_hash, scopes,
		       created_at, last_used_at, revoked_at
		  FROM workspace_api_keys
		 WHERE prefix = $1 AND revoked_at IS NULL`, prefix)
	var k WorkspaceAPIKey
	var lastUsed, revoked sql.NullTime
	var scopes pq.StringArray
	if err := row.Scan(&k.ID, &k.WorkspaceID, &k.UserID, &k.Name, &k.Prefix, &k.SecretHash, &scopes,
		&k.CreatedAt, &lastUsed, &revoked); err != nil {
```

New:
```go
	row := db.QueryRowContext(ctx, `
		SELECT id, workspace_id, user_id, name, prefix, secret_hash, scopes,
		       created_at, expires_at, last_used_at, revoked_at
		  FROM workspace_api_keys
		 WHERE prefix = $1 AND revoked_at IS NULL AND expires_at > NOW()`, prefix)
	var k WorkspaceAPIKey
	var lastUsed, revoked sql.NullTime
	var scopes pq.StringArray
	if err := row.Scan(&k.ID, &k.WorkspaceID, &k.UserID, &k.Name, &k.Prefix, &k.SecretHash, &scopes,
		&k.CreatedAt, &k.ExpiresAt, &lastUsed, &revoked); err != nil {
```

- [ ] **Step 6: Run the DB tests to verify they pass**

```bash
cd /root/agentserver && go test ./internal/db/ -run "TestWorkspaceAPIKey" -count=1 -v 2>&1 | tail -30
```

Expected: all `TestWorkspaceAPIKey_*` tests pass, including `TestWorkspaceAPIKey_ValidateExpired`.

- [ ] **Step 7: Commit**

```bash
cd /root/agentserver
git add internal/db/workspace_api_keys.go internal/db/workspace_api_keys_test.go
git commit -m "feat(db): plumb expires_at into workspace_api_keys queries + expired-key test"
```

---

## Task 4: Update DTOs in `api_types.go`

**Files:**
- Modify: `internal/server/api_types.go`

- [ ] **Step 1: Replace `CodexTokenMintRequest.TTLDays` with `ExpiresAt`**

In `internal/server/api_types.go`, find and replace the `CodexTokenMintRequest` struct. The old struct is:

```go
// CodexTokenMintRequest is the body for POST /api/codex/tokens.
// ttl_days is optional; defaults to 90 (range: 1–365).
type CodexTokenMintRequest struct {
	WorkspaceID string `json:"workspace_id" validate:"required"`
	Name        string `json:"name" validate:"required" example:"my mac"`
	TTLDays     int    `json:"ttl_days,omitempty" example:"90"`
} // @name CodexTokenMintRequest
```

Replace with:

```go
// CodexTokenMintRequest is the body for POST /api/codex/tokens.
// expires_at is optional; defaults to NOW + 90 days, capped at NOW + 365d.
type CodexTokenMintRequest struct {
	WorkspaceID string `json:"workspace_id" validate:"required"`
	Name        string `json:"name" validate:"required" example:"my mac"`
	ExpiresAt   string `json:"expires_at,omitempty" example:"2026-08-20T08:30:00Z"`
} // @name CodexTokenMintRequest
```

- [ ] **Step 2: Add `ExpiresAt` to `WorkspaceAPIKeyMintRequest`**

Find the old struct:

```go
type WorkspaceAPIKeyMintRequest struct {
	Name   string   `json:"name" validate:"required" example:"my-bot-integration"`
	Scopes []string `json:"scopes" validate:"required" example:"[\"turns:submit\"]"`
} // @name WorkspaceAPIKeyMintRequest
```

Replace with:

```go
type WorkspaceAPIKeyMintRequest struct {
	Name      string   `json:"name" validate:"required" example:"my-bot-integration"`
	Scopes    []string `json:"scopes" validate:"required" example:"[\"turns:submit\"]"`
	ExpiresAt string   `json:"expires_at,omitempty" example:"2026-08-20T08:30:00Z"`
} // @name WorkspaceAPIKeyMintRequest
```

- [ ] **Step 3: Add `ExpiresAt` to `WorkspaceAPIKeyMintResponse`**

Find:

```go
type WorkspaceAPIKeyMintResponse struct {
	ID        string   `json:"id" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Name      string   `json:"name" validate:"required"`
	Prefix    string   `json:"prefix" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Secret    string   `json:"secret" validate:"required" example:"ask_a1b2c3d4e5f6g7h8_X9y8Z7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2H1g0F9e8D7c6B5a4AbCdEf"`
	Scopes    []string `json:"scopes" validate:"required"`
	CreatedAt string   `json:"created_at" validate:"required"`
} // @name WorkspaceAPIKeyMintResponse
```

Replace with:

```go
type WorkspaceAPIKeyMintResponse struct {
	ID        string   `json:"id" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Name      string   `json:"name" validate:"required"`
	Prefix    string   `json:"prefix" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Secret    string   `json:"secret" validate:"required" example:"ask_a1b2c3d4e5f6g7h8_X9y8Z7w6V5u4T3s2R1q0P9o8N7m6L5k4J3i2H1g0F9e8D7c6B5a4AbCdEf"`
	Scopes    []string `json:"scopes" validate:"required"`
	CreatedAt string   `json:"created_at" validate:"required"`
	ExpiresAt string   `json:"expires_at" validate:"required" example:"2026-08-20T08:30:00Z"`
} // @name WorkspaceAPIKeyMintResponse
```

- [ ] **Step 4: Add `ExpiresAt` to `WorkspaceAPIKey` (list item DTO)**

Find:

```go
type WorkspaceAPIKey struct {
	ID         string   `json:"id" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Name       string   `json:"name" validate:"required"`
	Prefix     string   `json:"prefix" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Scopes     []string `json:"scopes" validate:"required"`
	CreatedAt  string   `json:"created_at" validate:"required"`
	LastUsedAt *string  `json:"last_used_at" extensions:"x-nullable=true"`
	RevokedAt  *string  `json:"revoked_at" extensions:"x-nullable=true"`
} // @name WorkspaceAPIKey
```

Replace with:

```go
type WorkspaceAPIKey struct {
	ID         string   `json:"id" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Name       string   `json:"name" validate:"required"`
	Prefix     string   `json:"prefix" validate:"required" example:"ask_a1b2c3d4e5f6g7h8"`
	Scopes     []string `json:"scopes" validate:"required"`
	CreatedAt  string   `json:"created_at" validate:"required"`
	ExpiresAt  string   `json:"expires_at" validate:"required" example:"2026-08-20T08:30:00Z"`
	LastUsedAt *string  `json:"last_used_at" extensions:"x-nullable=true"`
	RevokedAt  *string  `json:"revoked_at" extensions:"x-nullable=true"`
} // @name WorkspaceAPIKey
```

- [ ] **Step 5: Verify the file compiles**

```bash
cd /root/agentserver && go build ./internal/server/ 2>&1
```

Expected: compile errors about `req.TTLDays` used in `codex_tokens.go` — that's expected and will be fixed in the next task. If there are unexpected errors (typos in struct tags, etc.), fix them first.

- [ ] **Step 6: Commit (even with the expected compilation errors in codex_tokens.go)**

Wait — do not commit until codex_tokens.go is also updated in Task 5. Continue directly to Task 5.

---

## Task 5: Update codex token mint handler (`codex_tokens.go`)

**Files:**
- Modify: `internal/server/codex_tokens.go`
- Modify: `internal/server/codex_tokens_test.go`

- [ ] **Step 1: Update the mint handler in `codex_tokens.go`**

Remove the three TTL constants at the top of the file:

```go
const (
	codexTokenDefaultTTLDays = 90
	codexTokenMinTTLDays     = 1
	codexTokenMaxTTLDays     = 365
)
```

Remove these lines entirely (the file will no longer declare any constants).

Replace the TTL validation block in `handleMintCodexToken`. The old block is:

```go
	if req.TTLDays == 0 {
		req.TTLDays = codexTokenDefaultTTLDays
	}
	if req.TTLDays < codexTokenMinTTLDays || req.TTLDays > codexTokenMaxTTLDays {
		http.Error(w, "ttl_days out of range [1, 365]", http.StatusUnprocessableEntity)
		return
	}
```

And the `exp` line:
```go
	exp := time.Now().Add(time.Duration(req.TTLDays) * 24 * time.Hour).UTC()
```

Replace the three-line block (the two if blocks) with:

```go
	exp, err := resolveExpiresAt(req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
```

And delete the separate `exp :=` line (it's now set by `resolveExpiresAt`).

Also update the swagger `@Failure 422` comment:

Old:
```go
//	@Failure   422  {string}  string  "workspace_id and name are required / ttl_days out of range"
```

New:
```go
//	@Failure   422  {string}  string  "workspace_id and name are required / expires_at invalid"
```

Also remove the `"time"` import if it's no longer needed. Check: `time` is still used for `time.Now().UTC()` in the `CreateCodexToken` call and `CodexTokenMintResponse` — keep it.

- [ ] **Step 2: Verify the file compiles**

```bash
cd /root/agentserver && go build ./internal/server/ 2>&1
```

Expected: clean build. If there are lingering references to `req.TTLDays` or the removed constants, fix them.

- [ ] **Step 3: Update existing codex token tests**

In `internal/server/codex_tokens_test.go`, three tests send `ttl_days` in JSON:

1. `TestHandleMintCodexToken_HappyPath` — body: `{"workspace_id":"ws_a","name":"my mac","ttl_days":30}`

   Change to: `{"workspace_id":"ws_a","name":"my mac","expires_at":"` + time.Now().UTC().Add(30*24*time.Hour).Format(time.RFC3339) + `"}`
   
   But since hardcoding a timestamp is fragile, use a helper approach. Replace the literal JSON with:
   ```go
   exp30d := time.Now().UTC().Add(30 * 24 * time.Hour).Format(time.RFC3339)
   body := bytes.NewReader([]byte(`{"workspace_id":"ws_a","name":"my mac","expires_at":"` + exp30d + `"}`))
   ```

2. `TestHandleMintCodexToken_TTLClamp` — body: `{"workspace_id":"ws_a","name":"x","ttl_days":99999}`

   Change to send a future timestamp beyond 365 days:
   ```go
   tooFar := time.Now().UTC().Add(400 * 24 * time.Hour).Format(time.RFC3339)
   body := bytes.NewReader([]byte(`{"workspace_id":"ws_a","name":"x","expires_at":"` + tooFar + `"}`))
   ```
   The test name `TestHandleMintCodexToken_TTLClamp` can be renamed `TestHandleMintCodexToken_ExpiresAtTooFar` if preferred, but it's fine to keep the name.

3. `TestHandleListCodexTokens` — two mints with no `ttl_days` (they relied on defaults). These are fine as-is — the mint body `{"workspace_id":"ws_a","name":"a"}` will default to NOW+90d via `resolveExpiresAt("")`.

- [ ] **Step 4: Run codex token tests**

```bash
cd /root/agentserver && go test ./internal/server/ -run "TestHandleMint\|TestHandleList\|TestHandleRevoke" -count=1 -v 2>&1 | tail -30
```

Expected: all codex token handler tests pass.

- [ ] **Step 5: Commit**

```bash
cd /root/agentserver
git add internal/server/api_types.go internal/server/codex_tokens.go internal/server/codex_tokens_test.go
git commit -m "feat(codex-tokens): replace ttl_days with expires_at (uses resolveExpiresAt)"
```

---

## Task 6: Update workspace API key mint handler (`workspace_api_keys.go`) and tests

**Files:**
- Modify: `internal/server/workspace_api_keys.go`
- Modify: `internal/server/workspace_api_keys_test.go`

- [ ] **Step 1: Update `handleMintWorkspaceAPIKey`**

In `internal/server/workspace_api_keys.go`, in `handleMintWorkspaceAPIKey`, after the scope validation block:

```go
	if err := validateScopes(req.Scopes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
```

Add:

```go
	exp, err := resolveExpiresAt(req.ExpiresAt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
```

Then update the `db.WorkspaceAPIKey` literal to include `ExpiresAt`:

Old:
```go
	row := db.WorkspaceAPIKey{
		ID:          tok.ID,
		WorkspaceID: wid,
		UserID:      userID,
		Name:        req.Name,
		Prefix:      tok.ID,
		SecretHash:  tok.Hash,
		Scopes:      req.Scopes,
	}
```

New:
```go
	row := db.WorkspaceAPIKey{
		ID:          tok.ID,
		WorkspaceID: wid,
		UserID:      userID,
		Name:        req.Name,
		Prefix:      tok.ID,
		SecretHash:  tok.Hash,
		Scopes:      req.Scopes,
		ExpiresAt:   exp,
	}
```

Then update the `WorkspaceAPIKeyMintResponse` literal to include `ExpiresAt`:

Old:
```go
	_ = json.NewEncoder(w).Encode(WorkspaceAPIKeyMintResponse{
		ID:        tok.ID,
		Name:      req.Name,
		Prefix:    tok.ID,
		Secret:    tok.Full,
		Scopes:    req.Scopes,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	})
```

New:
```go
	_ = json.NewEncoder(w).Encode(WorkspaceAPIKeyMintResponse{
		ID:        tok.ID,
		Name:      req.Name,
		Prefix:    tok.ID,
		Secret:    tok.Full,
		Scopes:    req.Scopes,
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
		ExpiresAt: exp.Format(time.RFC3339),
	})
```

- [ ] **Step 2: Update `handleListWorkspaceAPIKeys` to include `ExpiresAt` in response**

In the `for _, k := range rows` loop, in the `WorkspaceAPIKey{}` literal:

Old:
```go
		out = append(out, WorkspaceAPIKey{
			ID:         k.ID,
			Name:       k.Name,
			Prefix:     k.Prefix,
			Scopes:     scopes,
			CreatedAt:  k.CreatedAt.UTC().Format(time.RFC3339),
			LastUsedAt: rfc3339Ptr(k.LastUsedAt),
			RevokedAt:  rfc3339Ptr(k.RevokedAt),
		})
```

New:
```go
		out = append(out, WorkspaceAPIKey{
			ID:         k.ID,
			Name:       k.Name,
			Prefix:     k.Prefix,
			Scopes:     scopes,
			CreatedAt:  k.CreatedAt.UTC().Format(time.RFC3339),
			ExpiresAt:  k.ExpiresAt.UTC().Format(time.RFC3339),
			LastUsedAt: rfc3339Ptr(k.LastUsedAt),
			RevokedAt:  rfc3339Ptr(k.RevokedAt),
		})
```

- [ ] **Step 3: Update the swagger comment for the mint handler**

Add `@Failure 422` after the existing `@Failure 400` lines:

```go
//	@Failure     400   {string}  string  "name required / scope not available / at least one scope required"
//	@Failure     422   {string}  string  "expires_at invalid (bad RFC3339 / in past / >365d in future)"
//	@Failure     403   {string}  string  "owner or maintainer required"
```

- [ ] **Step 4: Verify compilation**

```bash
cd /root/agentserver && go build ./internal/server/ 2>&1
```

Expected: clean build.

- [ ] **Step 5: Update `workspace_api_keys_test.go` — update `mintKeyViaHandler` helper**

The `mintKeyViaHandler` helper currently sends `WorkspaceAPIKeyMintRequest{Name: name, Scopes: scopes}`. The struct now has `ExpiresAt` — but since it's `omitempty`, the existing call still compiles fine and will use the server default. No change needed to the helper itself.

However, we need to update `TestMintListRevoke` to verify that the response now includes `expires_at`:

In `TestMintListRevoke`, after the mint assertion block, add:

```go
	if resp.ExpiresAt == "" {
		t.Fatal("mint response should include expires_at")
	}
	if _, err := time.Parse(time.RFC3339, resp.ExpiresAt); err != nil {
		t.Fatalf("expires_at is not RFC3339: %q", resp.ExpiresAt)
	}
```

And after the list assertion block (`keys[0].ID != resp.ID`), add:

```go
	if keys[0].ExpiresAt == "" {
		t.Fatal("list response should include expires_at")
	}
```

- [ ] **Step 6: Add the four new expiration tests**

Add these tests to `internal/server/workspace_api_keys_test.go`:

```go
// TestMint_DefaultExpiration verifies that omitting expires_at results in
// a response with expires_at approximately NOW + 90 days.
func TestMint_DefaultExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp1", "u_exp1", "owner")

	resp := mintKeyViaHandler(t, srv, "ws_exp1", "u_exp1", "default-exp", []string{"turns:submit"})
	if resp.ExpiresAt == "" {
		t.Fatal("expires_at must be set")
	}
	exp, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at not RFC3339: %q — %v", resp.ExpiresAt, err)
	}
	lo := time.Now().UTC().Add(89 * 24 * time.Hour)
	hi := time.Now().UTC().Add(91 * 24 * time.Hour)
	if exp.Before(lo) || exp.After(hi) {
		t.Fatalf("expected ~NOW+90d, got %v (lo=%v hi=%v)", exp, lo, hi)
	}
}

// TestMint_RejectsPastExpiration verifies that sending an expires_at in the
// past returns HTTP 422.
func TestMint_RejectsPastExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp2", "u_exp2", "owner")

	pastExp := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	body, _ := json.Marshal(WorkspaceAPIKeyMintRequest{
		Name:      "past-exp",
		Scopes:    []string{"turns:submit"},
		ExpiresAt: pastExp,
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_exp2/api-keys", "u_exp2", body, map[string]string{"wid": "ws_exp2"})
	rr := httptest.NewRecorder()
	srv.handleMintWorkspaceAPIKey(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestMint_RejectsTooFarFutureExpiration verifies that sending an expires_at
// more than 365 days in the future returns HTTP 422.
func TestMint_RejectsTooFarFutureExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp3", "u_exp3", "owner")

	tooFar := time.Now().UTC().Add(400 * 24 * time.Hour).Format(time.RFC3339)
	body, _ := json.Marshal(WorkspaceAPIKeyMintRequest{
		Name:      "too-far",
		Scopes:    []string{"turns:submit"},
		ExpiresAt: tooFar,
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_exp3/api-keys", "u_exp3", body, map[string]string{"wid": "ws_exp3"})
	rr := httptest.NewRecorder()
	srv.handleMintWorkspaceAPIKey(rr, req)
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d — %s", rr.Code, rr.Body.String())
	}
}

// TestMint_AcceptsValidExpiration verifies that a valid expires_at in the
// allowed window returns HTTP 201 and echoes the expires_at back.
func TestMint_AcceptsValidExpiration(t *testing.T) {
	srv := newAPIKeyTestServer(t)
	seedWorkspaceMember(t, srv.DB, "ws_exp4", "u_exp4", "owner")

	exp30d := time.Now().UTC().Add(30 * 24 * time.Hour).Truncate(time.Second)
	body, _ := json.Marshal(WorkspaceAPIKeyMintRequest{
		Name:      "valid-exp",
		Scopes:    []string{"turns:submit"},
		ExpiresAt: exp30d.Format(time.RFC3339),
	})
	req := reqWithUser(http.MethodPost, "/api/workspaces/ws_exp4/api-keys", "u_exp4", body, map[string]string{"wid": "ws_exp4"})
	rr := httptest.NewRecorder()
	srv.handleMintWorkspaceAPIKey(rr, req)
	if rr.Code != http.StatusCreated {
		t.Fatalf("want 201, got %d — %s", rr.Code, rr.Body.String())
	}
	var resp WorkspaceAPIKeyMintResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	gotExp, err := time.Parse(time.RFC3339, resp.ExpiresAt)
	if err != nil {
		t.Fatalf("expires_at not RFC3339: %q", resp.ExpiresAt)
	}
	// Allow 1s tolerance for round-trip through RFC3339 format.
	if gotExp.Sub(exp30d).Abs() > time.Second {
		t.Fatalf("expires_at mismatch: got %v, want ~%v", gotExp, exp30d)
	}
}
```

Note: `TestMint_AcceptsValidExpiration` uses `time.Duration.Abs()` which was added in Go 1.19. If the project is on an older Go version, replace `.Abs()` with `func absD(d time.Duration) time.Duration { if d < 0 { return -d }; return d }(gotExp.Sub(exp30d))`.

Check the Go version:
```bash
head -3 /root/agentserver/go.mod
```

- [ ] **Step 7: Add missing imports to `workspace_api_keys_test.go`**

The new tests use `time`, `json`, `http`, and `httptest`. Verify the existing imports already include these (they do — check the existing import block at the top of the file). They're already there.

- [ ] **Step 8: Run the workspace API key server tests**

```bash
cd /root/agentserver && go test ./internal/server/ -run "TestMint\|TestMintList" -count=1 -v 2>&1 | tail -40
```

Expected: all workspace API key tests pass, including the 4 new expiration tests.

- [ ] **Step 9: Commit**

```bash
cd /root/agentserver
git add internal/server/workspace_api_keys.go internal/server/workspace_api_keys_test.go
git commit -m "feat(api-keys): plumb expires_at through workspace API key mint/list handlers + tests"
```

---

## Task 7: Run all Go tests and build

**Files:** None changed — verification task.

- [ ] **Step 1: Full Go test run**

```bash
cd /root/agentserver && go test ./internal/secrets/ ./internal/db/ ./internal/server/ ./internal/auth/ -count=1 2>&1 | tail -30
```

Expected: all packages pass (`ok  github.com/...`). Any failures must be fixed before proceeding.

- [ ] **Step 2: Full build**

```bash
cd /root/agentserver && go build ./... 2>&1
```

Expected: clean. Fix any compile errors.

---

## Task 8: Update `web/src/lib/api.ts`

**Files:**
- Modify: `web/src/lib/api.ts`

This file uses generated OpenAPI types (`components['schemas']['...']`). After we update `api_types.go`, regenerating the spec is needed before the TS types update. However, the `openapi-typescript` generated file (`web/src/api/generated.d.ts` or similar) needs to be regenerated. We do this in Task 10. For Task 8, we update the function signatures — TypeScript will type-check after regeneration.

- [ ] **Step 1: Update `mintWorkspaceAPIKey` to accept `expiresAt?`**

Find the existing function (around line 941):

```typescript
export async function mintWorkspaceAPIKey(
  workspaceId: string,
  name: string,
  scopes: string[],
): Promise<WorkspaceAPIKeyMintResponse> {
  return apiFetch<WorkspaceAPIKeyMintResponse>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/api-keys`,
    body: { name, scopes } satisfies components['schemas']['WorkspaceAPIKeyMintRequest'],
  })
}
```

Replace with:

```typescript
export async function mintWorkspaceAPIKey(
  workspaceId: string,
  name: string,
  scopes: string[],
  expiresAt?: string,
): Promise<WorkspaceAPIKeyMintResponse> {
  return apiFetch<WorkspaceAPIKeyMintResponse>({
    method: 'POST',
    path: `/api/workspaces/${encodeURIComponent(workspaceId)}/api-keys`,
    body: { name, scopes, expires_at: expiresAt } satisfies components['schemas']['WorkspaceAPIKeyMintRequest'],
  })
}
```

- [ ] **Step 2: Update `mintCodexToken` — check its current shape**

Find `mintCodexToken` around line 833:

```typescript
export async function mintCodexToken(req: MintCodexTokenRequest): Promise<MintCodexTokenResponse> {
  return apiFetch<MintCodexTokenResponse>({
    method: 'POST',
    path: '/api/codex/tokens',
    body: req satisfies components['schemas']['CodexTokenMintRequest'],
  })
}
```

This function passes `req` directly as the body with a `satisfies` type guard. Because `MintCodexTokenRequest = components['schemas']['CodexTokenMintRequest']`, updating `api_types.go` to remove `TTLDays` and add `ExpiresAt` will automatically propagate here after spec regeneration. No code change needed in this function — the `satisfies` cast enforces the right shape.

However, the **caller** (`CodexTokensPanel.tsx`) passes `{ ..., ttl_days: newTTL }`. That needs updating in Task 9.

- [ ] **Step 3: Verify the file has no syntax errors**

```bash
cd /root/agentserver/web && pnpm tsc --noEmit 2>&1 | head -20
```

Expected: TypeScript errors about `expires_at` not being in the generated schema (until spec is regenerated in Task 10). That's acceptable at this stage — note the errors, proceed.

---

## Task 9: Update frontend components

**Files:**
- Modify: `web/src/components/MintAPIKeyModal.tsx`
- Modify: `web/src/components/CodexTokensPanel.tsx`
- Modify: `web/src/components/WorkspaceAPIKeysTab.tsx`

### MintAPIKeyModal.tsx

- [ ] **Step 1: Add expiry state and dropdown to the form**

At the top of `MintAPIKeyModal`, after the `checkedScopes` state line, add:

```typescript
const EXPIRY_OPTIONS = [
  { label: '7 days',   days: 7   },
  { label: '30 days',  days: 30  },
  { label: '90 days',  days: 90  },
  { label: '180 days', days: 180 },
  { label: '365 days', days: 365 },
] as const

const [expiryDays, setExpiryDays] = useState<number>(90)
```

- [ ] **Step 2: Update `handleCreate` to compute and pass `expiresAt`**

Old:
```typescript
    const result = await mintWorkspaceAPIKey(workspaceId, name.trim(), Array.from(checkedScopes))
```

New:
```typescript
    const expiresAt = new Date(Date.now() + expiryDays * 24 * 60 * 60 * 1000).toISOString()
    const result = await mintWorkspaceAPIKey(workspaceId, name.trim(), Array.from(checkedScopes), expiresAt)
```

- [ ] **Step 3: Add the expiry dropdown to the form JSX**

After the scopes section (after the closing `</div>` of the scopes block, before the submit buttons `<div className="flex gap-2 justify-end pt-1">`), insert:

```tsx
            <div>
              <label className="block text-xs font-medium text-[var(--muted-foreground)] mb-1">
                Expires in
              </label>
              <select
                value={expiryDays}
                onChange={(e) => setExpiryDays(parseInt(e.target.value, 10))}
                disabled={submitting}
                className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm text-[var(--foreground)] focus:outline-none focus:ring-1 focus:ring-[var(--primary)] disabled:opacity-50"
              >
                {EXPIRY_OPTIONS.map((opt) => (
                  <option key={opt.days} value={opt.days}>{opt.label}</option>
                ))}
              </select>
            </div>
```

### CodexTokensPanel.tsx

- [ ] **Step 4: Replace `TTL_OPTIONS`/`newTTL` with `expiryDays`**

Current state declarations to replace:
```typescript
const TTL_OPTIONS = [1, 7, 30, 90, 180, 365] as const
// ...
const [newTTL, setNewTTL] = useState<number>(90)
```

Replace the `TTL_OPTIONS` const and the `newTTL` state with:
```typescript
const EXPIRY_OPTIONS = [
  { label: '7 days',   days: 7   },
  { label: '30 days',  days: 30  },
  { label: '90 days',  days: 90  },
  { label: '180 days', days: 180 },
  { label: '365 days', days: 365 },
] as const

// (in component body, replacing `const [newTTL, setNewTTL] = useState<number>(90)`)
const [expiryDays, setExpiryDays] = useState<number>(90)
```

- [ ] **Step 5: Update `onMint` in `CodexTokensPanel`**

Old:
```typescript
      const resp = await mintCodexToken({
        workspace_id: workspaceId,
        name: newName.trim(),
        ttl_days: newTTL,
      })
```

New:
```typescript
      const expiresAt = new Date(Date.now() + expiryDays * 24 * 60 * 60 * 1000).toISOString()
      const resp = await mintCodexToken({
        workspace_id: workspaceId,
        name: newName.trim(),
        expires_at: expiresAt,
      })
```

Also update the reset after mint:
```typescript
      setNewTTL(90)
```
becomes:
```typescript
      setExpiryDays(90)
```

- [ ] **Step 6: Update the select dropdown JSX in `CodexTokensPanel`**

Find the existing select:
```tsx
                <select
                  value={newTTL}
                  onChange={(e) => setNewTTL(parseInt(e.target.value, 10))}
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                >
                  {TTL_OPTIONS.map(d => <option key={d} value={d}>{d} day{d === 1 ? '' : 's'}</option>)}
                </select>
```

Replace with:
```tsx
                <select
                  value={expiryDays}
                  onChange={(e) => setExpiryDays(parseInt(e.target.value, 10))}
                  className="w-full rounded-md border border-[var(--border)] bg-[var(--background)] px-3 py-2 text-sm text-[var(--foreground)] outline-none focus:border-[var(--primary)]"
                >
                  {EXPIRY_OPTIONS.map((opt) => (
                    <option key={opt.days} value={opt.days}>{opt.label}</option>
                  ))}
                </select>
```

### WorkspaceAPIKeysTab.tsx

- [ ] **Step 7: Add "Expires" column to the table**

Add a helper function at the top of the file, before the component:

```typescript
function formatExpiresAt(expiresAt: string): { label: string; className: string } {
  const now = Date.now()
  const exp = new Date(expiresAt).getTime()
  const diffMs = exp - now
  const diffDays = Math.floor(diffMs / (1000 * 60 * 60 * 24))
  if (diffMs <= 0) {
    return { label: 'expired', className: 'text-red-400' }
  }
  if (diffDays <= 7) {
    return { label: `expires in ${diffDays}d`, className: 'text-amber-400' }
  }
  return { label: `expires in ${diffDays}d`, className: 'text-[var(--muted-foreground)]' }
}
```

Add a new `<th>` after the "Created" header:

```tsx
                    <th className="w-36 px-3 py-2 text-left font-medium">Expires</th>
```

Add the corresponding `<td>` in the row map, after the "Created" cell:

```tsx
                        <td className="px-3 py-2 text-[11px]">
                          {k.expires_at ? (() => {
                            const { label, className } = formatExpiresAt(k.expires_at)
                            return <span className={className}>{label}</span>
                          })() : '—'}
                        </td>
```

Note: the DTO field `ExpiresAt` serializes as `expires_at` in JSON, so the TypeScript type from the OpenAPI schema will use `expires_at` (snake_case). Verify by checking other fields in the component: `k.created_at`, `k.last_used_at` — yes, snake_case is used throughout.

---

## Task 10: Regenerate OpenAPI spec and run frontend checks

**Files:**
- Modify: `docs/api/openapi.yaml`
- Modify: `docs/api/openapi.json`

- [ ] **Step 1: Regenerate swagger docs**

```bash
cd /root/agentserver && swag init -g cmd/agentserver/main.go -o docs/api --parseDependency 2>&1 | tail -10
```

Or check how swagger is generated in this project:

```bash
grep -n "swag\|swagger" /root/agentserver/Makefile | head -20
```

Then run the documented target. Likely:

```bash
cd /root/agentserver && make openapi 2>&1 | tail -20
```

- [ ] **Step 2: Run `make openapi-check`**

```bash
cd /root/agentserver && make openapi-check 2>&1 | tail -20
```

Expected: passes (no diff). If there's a diff, `make openapi` already updated the files — `make openapi-check` verifies no uncommitted changes. You may need to stage the openapi files if the check diffs against committed state.

- [ ] **Step 3: Run TypeScript checks**

```bash
cd /root/agentserver/web && pnpm tsc --noEmit 2>&1 | tail -20
```

Expected: clean. If there are type errors from the generated schema not matching the function signatures, investigate:
- Did `expires_at` propagate into the generated schema?
- Is the `satisfies` constraint failing?

Fix any type errors before proceeding.

- [ ] **Step 4: Run pnpm lint**

```bash
cd /root/agentserver/web && pnpm lint 2>&1 | tail -20
```

Expected: clean (0 errors). Fix any ESLint errors.

- [ ] **Step 5: Run pnpm build**

```bash
cd /root/agentserver/web && pnpm build 2>&1 | tail -10
```

Expected: `dist/` built successfully.

- [ ] **Step 6: Restore web/dist to not include it in the commit**

```bash
cd /root/agentserver && git checkout -- web/dist 2>/dev/null || true
```

- [ ] **Step 7: Commit frontend + openapi**

```bash
cd /root/agentserver
git add web/src/lib/api.ts \
        web/src/components/MintAPIKeyModal.tsx \
        web/src/components/WorkspaceAPIKeysTab.tsx \
        web/src/components/CodexTokensPanel.tsx \
        docs/api/openapi.yaml docs/api/openapi.json
# Also include swagger source files if the regeneration updates them:
git add docs/api/swagger.yaml docs/api/swagger.json 2>/dev/null || true
git commit -m "feat(frontend): unified expires_at dropdown in API key + codex token modals"
```

---

## Task 11: Final verification and single squash commit

- [ ] **Step 1: Run the complete Go test suite**

```bash
cd /root/agentserver && go test ./internal/secrets/ ./internal/db/ ./internal/server/ ./internal/auth/ -count=1 2>&1 | tail -20
```

Expected: all pass.

- [ ] **Step 2: Run the full verify script from the spec**

```bash
cd /root/agentserver
go build ./...
go test ./internal/secrets/ ./internal/db/ ./internal/server/ ./internal/auth/ -count=1
make openapi-check
cd web && pnpm tsc --noEmit && pnpm lint && pnpm build && cd ..
git checkout -- web/dist 2>/dev/null || true
git status --short | head -10
```

Expected: all green, `git status` shows only staged/committed changes.

- [ ] **Step 3: Check git log for the commits on this branch**

```bash
cd /root/agentserver && git log main..HEAD --oneline
```

Review the commits made. The spec calls for a single commit. If there are multiple, squash them:

```bash
# Count commits since main
COMMITS=$(git rev-list --count main..HEAD)
git reset --soft HEAD~$COMMITS
```

Then create the single commit:

```bash
git add internal/db/migrations/030_workspace_api_keys_expiration.sql \
        internal/db/workspace_api_keys.go internal/db/workspace_api_keys_test.go \
        internal/server/api_types.go \
        internal/server/codex_tokens.go internal/server/codex_tokens_test.go \
        internal/server/workspace_api_keys.go internal/server/workspace_api_keys_test.go \
        internal/server/expiration.go internal/server/expiration_test.go \
        web/src/lib/api.ts \
        web/src/components/MintAPIKeyModal.tsx \
        web/src/components/WorkspaceAPIKeysTab.tsx \
        web/src/components/CodexTokensPanel.tsx \
        docs/api/openapi.yaml docs/api/openapi.json
# Also add swagger source if updated:
git add docs/api/swagger.yaml docs/api/swagger.json 2>/dev/null || true

git commit -m "$(cat <<'EOF'
feat(api-keys): unified expires_at field across ask_ and ast_ tokens

Both token systems now take a single optional 'expires_at' ISO8601
string on mint (instead of 'ttl_days' int). Server defaults to NOW+90d
when omitted, caps at NOW+365d, rejects past timestamps. Shared
resolveExpiresAt helper enforces the rules.

- internal/server/expiration.go: shared helper + tests
- ask_ workspace API keys: now have expiration (migration 030 adds
  the column with a 90d default backfill)
- ast_ codex tokens: TTLDays field removed from CodexTokenMintRequest
  in favor of ExpiresAt; constants codexToken{Default,Min,Max}TTLDays
  deleted (logic lives in resolveExpiresAt)
- Frontend modals: dropdown picks N days, computes ISO timestamp
  client-side, sends as expires_at

Breaking on the wire for codex token mint API (ttl_days no longer
accepted) — SPA updated in same commit.

Co-Authored-By: Claude Sonnet 4.6 <noreply@anthropic.com>
EOF
)"
```

- [ ] **Step 4: Push and create PR**

```bash
cd /root/agentserver && git push -u github feat/api-key-expiration
```

```bash
gh pr create \
  --base main \
  --title "feat(api-keys): unified expires_at across ask_/ast_ tokens (replaces ttl_days)" \
  --body "$(cat <<'EOF'
## Summary

- **Two-field redundancy collapsed:** previously codex tokens accepted `ttl_days` (int) on mint but responded with `expires_at` (RFC3339). Now both request and response use `expires_at` exclusively.
- **Gap fixed:** workspace API keys (`ask_*`) had no expiration at all. Migration 030 adds `expires_at TIMESTAMPTZ NOT NULL` with a 90-day backfill for any pre-existing rows (table is still in-flight, so this is defensive).
- **Shared validation:** `internal/server/expiration.go` owns the rules — default 90d, cap 365d, reject past — used by both codex and workspace API key handlers.

## Wire-breaking change

`POST /api/codex/tokens` no longer accepts `ttl_days`. The SPA is updated atomically in this commit, so there's no window of incompatibility for browser clients. Out-of-band CLI users passing `ttl_days` will get a 422 response; they should switch to `expires_at`.

## Migration 030

`ALTER TABLE workspace_api_keys ADD COLUMN expires_at TIMESTAMPTZ` with `UPDATE ... SET expires_at = NOW() + INTERVAL '90 days'` backfill then `SET NOT NULL`. The workspace_api_keys table has not been deployed yet (PR #171 still in flight), so the backfill is defensive.

## Test plan

- [ ] `go test ./internal/secrets/ ./internal/db/ ./internal/server/ ./internal/auth/ -count=1` — all pass
- [ ] `make openapi-check` — no diff
- [ ] `pnpm tsc --noEmit && pnpm lint && pnpm build` — clean
- [ ] Mint a codex token via the UI → "Expires in" dropdown works, response shows `expires_at`
- [ ] Mint a workspace API key via the UI → "Expires in" dropdown works, list shows "Expires" column
- [ ] Validate an expired `ask_*` key → returns 401 (filtered by `AND expires_at > NOW()`)

🤖 Generated with [Claude Code](https://claude.com/claude-code)
EOF
)"
```

---

## Self-Review

Checking spec coverage:

| Spec requirement | Task |
|-----------------|------|
| `resolveExpiresAt` shared helper | Task 2 |
| Migration 030 `expires_at` column | Task 1 |
| DB `CreateWorkspaceAPIKey` + `expires_at` | Task 3 |
| DB `ListWorkspaceAPIKeys` + `expires_at` | Task 3 |
| DB `ValidateWorkspaceAPIKeySecret` + `AND expires_at > NOW()` | Task 3 |
| `TestWorkspaceAPIKey_ValidateExpired` DB test | Task 3 |
| `CodexTokenMintRequest` TTLDays→ExpiresAt | Task 4+5 |
| `WorkspaceAPIKeyMintRequest` + ExpiresAt | Task 4 |
| `WorkspaceAPIKeyMintResponse` + ExpiresAt | Task 4 |
| `WorkspaceAPIKey` (list DTO) + ExpiresAt | Task 4 |
| `codex_tokens.go` TTL block→resolveExpiresAt | Task 5 |
| `codex_tokens_test.go` updated | Task 5 |
| `workspace_api_keys.go` resolveExpiresAt call | Task 6 |
| `TestMint_DefaultExpiration` | Task 6 |
| `TestMint_RejectsPastExpiration` | Task 6 |
| `TestMint_RejectsTooFarFutureExpiration` | Task 6 |
| `TestMint_AcceptsValidExpiration` | Task 6 |
| `expiration_test.go` unit tests | Task 2 |
| `api.ts` mintWorkspaceAPIKey + expiresAt | Task 8 |
| `api.ts` mintCodexToken passes expires_at | Task 8+9 |
| `MintAPIKeyModal.tsx` expiry dropdown | Task 9 |
| `CodexTokensPanel.tsx` TTL→expiresAt | Task 9 |
| `WorkspaceAPIKeysTab.tsx` Expires column | Task 9 |
| OpenAPI spec regenerated | Task 10 |
| Single commit + PR | Task 11 |

All spec requirements covered. No placeholders — all code blocks are complete and executable.

Type consistency check: `resolveExpiresAt` defined in Task 2 as `func resolveExpiresAt(raw string) (time.Time, error)` — called with `req.ExpiresAt` (string) in Tasks 5 and 6. `ExpiresAt` field name used consistently throughout structs in Task 4. Frontend `expiresAt` (camelCase) → `expires_at` (JSON) used consistently in Tasks 8 and 9. `formatExpiresAt` in Task 9 matches the function defined in `WorkspaceAPIKeysTab.tsx`. All consistent.
