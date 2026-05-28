package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
)

// ErrUserAlreadyAnonymized is returned when anonymize is called on an already-anonymized user.
var ErrUserAlreadyAnonymized = errors.New("user already anonymized")

// LastOwnerWorkspace identifies a workspace where the user is the sole owner.
type LastOwnerWorkspace struct {
	ID   string
	Name string
}

// WorkspacesWhereUserIsLastOwner returns workspaces owned by userID that have no other owner.
func (db *DB) WorkspacesWhereUserIsLastOwner(ctx context.Context, userID string) ([]LastOwnerWorkspace, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT w.id, w.name
		FROM workspaces w
		WHERE w.owner_id = $1
		  AND NOT EXISTS (
		    SELECT 1 FROM workspace_members wm
		    WHERE wm.workspace_id = w.id AND wm.role = 'owner' AND wm.user_id <> $1
		  )`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []LastOwnerWorkspace
	for rows.Next() {
		var w LastOwnerWorkspace
		if err := rows.Scan(&w.ID, &w.Name); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// IsUserAnonymized reports whether the user row has anonymized_at set.
func (db *DB) IsUserAnonymized(ctx context.Context, userID string) (bool, error) {
	var ts *string
	err := db.QueryRowContext(ctx, `
		SELECT anonymized_at::text FROM users WHERE id = $1`, userID).Scan(&ts)
	if err == sql.ErrNoRows {
		return false, sql.ErrNoRows
	}
	if err != nil {
		return false, err
	}
	return ts != nil, nil
}

func hashEmailForAnonymization(email string) string {
	sum := sha256.Sum256([]byte(email))
	return hex.EncodeToString(sum[:])
}

func anonymizedPlaceholderEmail(userID string) string {
	return fmt.Sprintf("deleted-%s@anonymized.local", userID)
}

// AnonymizeUser scrubs PII and removes credentials, memberships, and sessions. Idempotent only before first success.
func (db *DB) AnonymizeUser(ctx context.Context, userID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var email string
	var already bool
	err = tx.QueryRowContext(ctx, `
		SELECT email, (anonymized_at IS NOT NULL) FROM users WHERE id = $1`, userID).
		Scan(&email, &already)
	if err == sql.ErrNoRows {
		return sql.ErrNoRows
	}
	if err != nil {
		return err
	}
	if already {
		return ErrUserAlreadyAnonymized
	}

	emailHash := hashEmailForAnonymization(email)
	placeholder := anonymizedPlaceholderEmail(userID)

	res, err := tx.ExecContext(ctx, `
		UPDATE users SET
		  email = $2,
		  name = NULL,
		  picture = NULL,
		  original_email_hash = $3,
		  anonymized_at = NOW()
		WHERE id = $1 AND anonymized_at IS NULL`,
		userID, placeholder, emailHash)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrUserAlreadyAnonymized
	}

	for _, q := range []struct {
		sql  string
		args []interface{}
	}{
		{`DELETE FROM user_credentials WHERE user_id = $1`, []interface{}{userID}},
		{`DELETE FROM auth_tokens WHERE user_id = $1`, []interface{}{userID}},
		{`DELETE FROM workspace_members WHERE user_id = $1`, []interface{}{userID}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// LastOwnerConflictError carries workspace names for HTTP 409 responses.
type LastOwnerConflictError struct {
	Workspaces []LastOwnerWorkspace
}

func (e *LastOwnerConflictError) Error() string {
	return "user is the sole owner of one or more workspaces"
}

// IsLastOwnerConflict reports whether err is a sole-owner guard failure.
func IsLastOwnerConflict(err error) (*LastOwnerConflictError, bool) {
	var c *LastOwnerConflictError
	if errors.As(err, &c) {
		return c, true
	}
	return nil, false
}

// WrapLastOwnerConflict returns an error suitable for handlers when workspaces block delete.
func WrapLastOwnerConflict(workspaces []LastOwnerWorkspace) error {
	return &LastOwnerConflictError{Workspaces: workspaces}
}
