package db

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Invite represents a pending or accepted workspace membership invite.
// TokenHash is sha256(plaintext_token) — plaintext only lives in the
// emitted URL/email; never persisted.
type Invite struct {
	ID          string
	WorkspaceID string
	Email       string
	Role        string
	TokenHash   string
	ExpiresAt   time.Time
	AcceptedAt  sql.NullTime
	CreatedBy   string
	CreatedAt   time.Time
}

// CreateInvite inserts a new invite. Returns ErrInviteAlreadyPending when a
// pending invite for the same (workspace, email) already exists.
func (db *DB) CreateInvite(
	id, workspaceID, email, role, tokenHash, createdBy string,
	expiresAt time.Time,
) (*Invite, error) {
	if role == "" {
		role = "developer"
	}
	_, err := db.Exec(`
		INSERT INTO workspace_invites
			(id, workspace_id, email, role, token_hash, expires_at, created_by, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, NOW())
	`, id, workspaceID, email, role, tokenHash, expiresAt, createdBy)
	if err != nil {
		// uniq_workspace_invites_pending → duplicate pending
		if isPGUniqueViolation(err) {
			return nil, ErrInviteAlreadyPending
		}
		return nil, fmt.Errorf("create invite: %w", err)
	}
	return db.GetInviteByID(id)
}

// GetInviteByID fetches by primary key. Returns nil, nil if not found.
func (db *DB) GetInviteByID(id string) (*Invite, error) {
	row := db.QueryRow(`
		SELECT id, workspace_id, email, role, token_hash, expires_at,
		       accepted_at, created_by, created_at
		FROM workspace_invites
		WHERE id = $1
	`, id)
	return scanInvite(row)
}

// GetPendingInviteByTokenHash returns a pending (not accepted, not expired)
// invite matching the token hash. Returns nil, nil otherwise.
//
// IMPORTANT: callers MUST NOT distinguish between "not found", "expired",
// and "already accepted" in their HTTP response — single generic 404 to
// avoid token enumeration.
func (db *DB) GetPendingInviteByTokenHash(tokenHash string) (*Invite, error) {
	row := db.QueryRow(`
		SELECT id, workspace_id, email, role, token_hash, expires_at,
		       accepted_at, created_by, created_at
		FROM workspace_invites
		WHERE token_hash = $1
		  AND accepted_at IS NULL
		  AND expires_at > NOW()
	`, tokenHash)
	return scanInvite(row)
}

// MarkInviteAccepted sets accepted_at = NOW() iff the invite is still pending.
// Returns ErrInviteAlreadyAccepted if it was already accepted (race with another
// caller) or sql.ErrNoRows if not found.
func (db *DB) MarkInviteAccepted(id string) error {
	res, err := db.Exec(`
		UPDATE workspace_invites
		SET accepted_at = NOW()
		WHERE id = $1 AND accepted_at IS NULL
	`, id)
	if err != nil {
		return fmt.Errorf("mark invite accepted: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrInviteAlreadyAccepted
	}
	return nil
}

// ListInvitesByWorkspace returns invites (pending and accepted) for a workspace.
func (db *DB) ListInvitesByWorkspace(workspaceID string) ([]Invite, error) {
	rows, err := db.Query(`
		SELECT id, workspace_id, email, role, token_hash, expires_at,
		       accepted_at, created_by, created_at
		FROM workspace_invites
		WHERE workspace_id = $1
		ORDER BY created_at DESC
	`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list invites: %w", err)
	}
	defer rows.Close()

	var invites []Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		invites = append(invites, *inv)
	}
	return invites, rows.Err()
}

// DeleteInvite removes a pending invite. Used to revoke before acceptance.
func (db *DB) DeleteInvite(id string) error {
	res, err := db.Exec(
		`DELETE FROM workspace_invites WHERE id = $1 AND accepted_at IS NULL`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete invite: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// scanInvite reads a row (sql.Row or *sql.Rows) into Invite.
func scanInvite(s interface {
	Scan(dest ...any) error
}) (*Invite, error) {
	var inv Invite
	err := s.Scan(
		&inv.ID, &inv.WorkspaceID, &inv.Email, &inv.Role, &inv.TokenHash,
		&inv.ExpiresAt, &inv.AcceptedAt, &inv.CreatedBy, &inv.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan invite: %w", err)
	}
	return &inv, nil
}

var (
	ErrInviteAlreadyPending  = errors.New("invite already pending for this email")
	ErrInviteAlreadyAccepted = errors.New("invite already accepted")
)

// isPGUniqueViolation matches PostgreSQL unique_violation (SQLSTATE 23505)
// without coupling to the driver package.
func isPGUniqueViolation(err error) bool {
	return err != nil && contains(err.Error(), "23505")
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
