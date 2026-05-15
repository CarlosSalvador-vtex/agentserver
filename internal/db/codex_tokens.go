package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CodexToken is one row from codex_remote_tokens.
type CodexToken struct {
	ID          string
	UserID      string
	WorkspaceID string
	Name        string
	TokenHash   string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
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
