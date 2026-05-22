package db

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/lib/pq"
)

// WorkspaceAPIKey mirrors workspace_api_keys rows. SecretHash is only
// populated on the row INSERT path; reads via List / Get omit it.
type WorkspaceAPIKey struct {
	ID          string
	WorkspaceID string
	UserID      string
	Name        string
	Prefix      string
	SecretHash  string // populated only by the row that just inserted
	Scopes      []string
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

// CreateWorkspaceAPIKey inserts a new key row. Caller is responsible for
// generating id (= "wak_<prefix>"), prefix, secret_hash, and validating
// the scopes against the catalog (see internal/server/api_key_scopes.go).
func (db *DB) CreateWorkspaceAPIKey(ctx context.Context, k WorkspaceAPIKey) error {
	scopes := k.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO workspace_api_keys
		    (id, workspace_id, user_id, name, prefix, secret_hash, scopes)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		k.ID, k.WorkspaceID, k.UserID, k.Name, k.Prefix, k.SecretHash, pq.Array(scopes))
	if err != nil {
		return fmt.Errorf("insert workspace_api_keys: %w", err)
	}
	return nil
}

// ListWorkspaceAPIKeys returns all non-revoked AND revoked keys for a
// workspace, sorted newest-first. Secret hashes are never returned.
func (db *DB) ListWorkspaceAPIKeys(ctx context.Context, workspaceID string) ([]WorkspaceAPIKey, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT id, workspace_id, user_id, name, prefix, scopes,
		       created_at, last_used_at, revoked_at
		  FROM workspace_api_keys
		 WHERE workspace_id = $1
		 ORDER BY created_at DESC`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list workspace_api_keys: %w", err)
	}
	defer rows.Close()

	var out []WorkspaceAPIKey
	for rows.Next() {
		var k WorkspaceAPIKey
		var lastUsed, revoked sql.NullTime
		var scopes pq.StringArray
		if err := rows.Scan(&k.ID, &k.WorkspaceID, &k.UserID, &k.Name, &k.Prefix, &scopes,
			&k.CreatedAt, &lastUsed, &revoked); err != nil {
			return nil, fmt.Errorf("scan workspace_api_keys: %w", err)
		}
		k.Scopes = []string(scopes)
		if lastUsed.Valid {
			t := lastUsed.Time
			k.LastUsedAt = &t
		}
		if revoked.Valid {
			t := revoked.Time
			k.RevokedAt = &t
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// RevokeWorkspaceAPIKey soft-deletes by stamping revoked_at. Idempotent:
// re-revoking a revoked row is a no-op.
func (db *DB) RevokeWorkspaceAPIKey(ctx context.Context, workspaceID, keyID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE workspace_api_keys
		   SET revoked_at = NOW()
		 WHERE id = $1 AND workspace_id = $2 AND revoked_at IS NULL`,
		keyID, workspaceID)
	if err != nil {
		return fmt.Errorf("revoke workspace_api_keys: %w", err)
	}
	return nil
}

// ValidateWorkspaceAPIKeySecret looks up the key by prefix, constant-time
// compares the hash, and returns the active row (including scopes) on
// match. Returns sql.ErrNoRows on any mismatch (wrong prefix, wrong
// secret, revoked).
//
// On match, fires a best-effort last_used_at update — does NOT block on it.
func (db *DB) ValidateWorkspaceAPIKeySecret(ctx context.Context, prefix, secret string) (*WorkspaceAPIKey, error) {
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
		return nil, err // includes sql.ErrNoRows
	}
	sum := sha256.Sum256([]byte(secret))
	presented := hex.EncodeToString(sum[:])
	if subtle.ConstantTimeCompare([]byte(presented), []byte(k.SecretHash)) != 1 {
		return nil, sql.ErrNoRows
	}
	k.Scopes = []string(scopes)
	if lastUsed.Valid {
		t := lastUsed.Time
		k.LastUsedAt = &t
	}
	// Defensive: should never happen given WHERE clause, but if a
	// concurrent revoke landed between our query and now, treat as miss.
	if revoked.Valid {
		return nil, sql.ErrNoRows
	}
	k.SecretHash = "" // do not leak hash to callers
	return &k, nil
}

// TouchWorkspaceAPIKeyLastUsed bumps last_used_at to NOW(). Fire-and-forget;
// errors are logged by caller, not surfaced.
func (db *DB) TouchWorkspaceAPIKeyLastUsed(ctx context.Context, keyID string) error {
	_, err := db.ExecContext(ctx, `
		UPDATE workspace_api_keys SET last_used_at = NOW() WHERE id = $1`, keyID)
	if err != nil {
		return fmt.Errorf("touch workspace_api_keys last_used_at: %w", err)
	}
	return nil
}
