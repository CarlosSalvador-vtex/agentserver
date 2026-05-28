package db

import (
	"database/sql"
	"fmt"
	"time"
)

func (db *DB) CreateToken(token, userID string, expiresAt time.Time) error {
	_, err := db.Exec(
		"INSERT INTO auth_tokens (token, user_id, expires_at) VALUES ($1, $2, $3)",
		token, userID, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create token: %w", err)
	}
	return nil
}

func (db *DB) ValidateToken(token string) (string, error) {
	userID, _, err := db.ValidateTokenWithWorkspace(token)
	return userID, err
}

// ValidateTokenWithWorkspace returns (userID, activeWorkspaceID, err).
// activeWorkspaceID is "" when the session has no workspace selected
// (NULL in DB). Migration 039 added the column.
func (db *DB) ValidateTokenWithWorkspace(token string) (string, string, error) {
	var userID string
	var activeWS sql.NullString
	err := db.QueryRow(
		"SELECT user_id, active_workspace_id FROM auth_tokens WHERE token = $1 AND expires_at > NOW()",
		token,
	).Scan(&userID, &activeWS)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("validate token: %w", err)
	}
	return userID, activeWS.String, nil
}

// SetTokenActiveWorkspace sets the active workspace for a session.
// Caller must verify membership before calling. Pass empty workspaceID
// to clear (NULL).
func (db *DB) SetTokenActiveWorkspace(token, workspaceID string) error {
	var arg interface{}
	if workspaceID == "" {
		arg = nil
	} else {
		arg = workspaceID
	}
	res, err := db.Exec(
		"UPDATE auth_tokens SET active_workspace_id = $2 WHERE token = $1 AND expires_at > NOW()",
		token, arg,
	)
	if err != nil {
		return fmt.Errorf("set token active workspace: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("set token active workspace rows: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (db *DB) DeleteToken(token string) error {
	_, err := db.Exec("DELETE FROM auth_tokens WHERE token = $1", token)
	if err != nil {
		return fmt.Errorf("delete token: %w", err)
	}
	return nil
}

func (db *DB) DeleteExpiredTokens() error {
	_, err := db.Exec("DELETE FROM auth_tokens WHERE expires_at < NOW()")
	if err != nil {
		return fmt.Errorf("delete expired tokens: %w", err)
	}
	return nil
}

// ClearActiveWorkspace sets active_workspace_id to NULL for all sessions pointing at the workspace.
func (db *DB) ClearActiveWorkspace(workspaceID string) error {
	_, err := db.Exec(
		`UPDATE auth_tokens SET active_workspace_id = NULL WHERE active_workspace_id = $1`,
		workspaceID,
	)
	if err != nil {
		return fmt.Errorf("clear active workspace: %w", err)
	}
	return nil
}

