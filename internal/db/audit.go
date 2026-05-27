package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// AuditEvent is a single immutable audit row. UserID / WorkspaceID may be
// empty for unauthenticated paths (e.g., a failed login before identity
// is known); the row is still recorded with NULL FKs to preserve forensics.
type AuditEvent struct {
	ID             int64
	UserID         string
	WorkspaceID    string
	EventType      string
	Details        map[string]any
	RequestMethod  string
	RequestPath    string
	ResponseStatus int
	IP             string
	UserAgent      string
	ErrorMsg       string
	At             time.Time
}

// InsertAuditEvent appends a row. Errors are returned but callers (the
// audit worker) typically just log them — audit is best-effort, not
// transactional with the user action.
func (db *DB) InsertAuditEvent(e AuditEvent) error {
	var (
		userID, workspaceID, method, path, ip, ua, errMsg sql.NullString
		status                                            sql.NullInt32
		details                                           sql.NullString
	)
	if e.UserID != "" {
		userID = sql.NullString{String: e.UserID, Valid: true}
	}
	if e.WorkspaceID != "" {
		workspaceID = sql.NullString{String: e.WorkspaceID, Valid: true}
	}
	if e.RequestMethod != "" {
		method = sql.NullString{String: e.RequestMethod, Valid: true}
	}
	if e.RequestPath != "" {
		path = sql.NullString{String: e.RequestPath, Valid: true}
	}
	if e.IP != "" {
		ip = sql.NullString{String: e.IP, Valid: true}
	}
	if e.UserAgent != "" {
		ua = sql.NullString{String: e.UserAgent, Valid: true}
	}
	if e.ErrorMsg != "" {
		errMsg = sql.NullString{String: e.ErrorMsg, Valid: true}
	}
	if e.ResponseStatus != 0 {
		status = sql.NullInt32{Int32: int32(e.ResponseStatus), Valid: true}
	}
	if e.Details != nil {
		b, err := json.Marshal(e.Details)
		if err != nil {
			return fmt.Errorf("marshal audit details: %w", err)
		}
		details = sql.NullString{String: string(b), Valid: true}
	}

	_, err := db.Exec(`
		INSERT INTO session_audit_events
			(user_id, workspace_id, event_type, details, request_method, request_path,
			 response_status, ip, user_agent, error_msg)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, userID, workspaceID, e.EventType, details, method, path, status, ip, ua, errMsg)
	if err != nil {
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

// AuditQuery filters for ListAuditEvents.
type AuditQuery struct {
	WorkspaceID string
	EventType   string
	From        time.Time
	To          time.Time
	Limit       int
	Offset      int
}

// ListAuditEvents returns events filtered by query, ordered by at DESC.
// WorkspaceID is REQUIRED (the endpoint is workspace-scoped).
func (db *DB) ListAuditEvents(q AuditQuery) ([]AuditEvent, error) {
	if q.WorkspaceID == "" {
		return nil, fmt.Errorf("ListAuditEvents requires WorkspaceID")
	}
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}

	args := []any{q.WorkspaceID}
	where := "workspace_id = $1"
	if q.EventType != "" {
		args = append(args, q.EventType)
		where += fmt.Sprintf(" AND event_type = $%d", len(args))
	}
	if !q.From.IsZero() {
		args = append(args, q.From)
		where += fmt.Sprintf(" AND at >= $%d", len(args))
	}
	if !q.To.IsZero() {
		args = append(args, q.To)
		where += fmt.Sprintf(" AND at <= $%d", len(args))
	}
	args = append(args, q.Limit, q.Offset)
	query := fmt.Sprintf(`
		SELECT id, user_id, workspace_id, event_type, details, request_method,
		       request_path, response_status, ip, user_agent, error_msg, at
		FROM session_audit_events
		WHERE %s
		ORDER BY at DESC
		LIMIT $%d OFFSET $%d
	`, where, len(args)-1, len(args))

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query audit: %w", err)
	}
	defer rows.Close()

	var out []AuditEvent
	for rows.Next() {
		ev, err := scanAuditRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

func scanAuditRow(rows *sql.Rows) (AuditEvent, error) {
	var (
		e                                                AuditEvent
		userID, workspaceID, method, path, ip, ua, errM  sql.NullString
		detailsJSON                                      sql.NullString
		status                                           sql.NullInt32
	)
	err := rows.Scan(
		&e.ID, &userID, &workspaceID, &e.EventType, &detailsJSON, &method, &path,
		&status, &ip, &ua, &errM, &e.At,
	)
	if err != nil {
		return e, fmt.Errorf("scan audit: %w", err)
	}
	e.UserID = userID.String
	e.WorkspaceID = workspaceID.String
	e.RequestMethod = method.String
	e.RequestPath = path.String
	e.ResponseStatus = int(status.Int32)
	e.IP = ip.String
	e.UserAgent = ua.String
	e.ErrorMsg = errM.String
	if detailsJSON.Valid && detailsJSON.String != "" {
		if err := json.Unmarshal([]byte(detailsJSON.String), &e.Details); err != nil {
			// Don't fail the whole listing — bad detail JSON shouldn't hide events.
			e.Details = map[string]any{"_raw": detailsJSON.String, "_err": err.Error()}
		}
	}
	return e, nil
}

// PurgeAuditEventsOlderThan deletes rows older than the cutoff. Returns
// number of rows removed. Intended for a daily cron job.
func (db *DB) PurgeAuditEventsOlderThan(cutoff time.Time) (int64, error) {
	res, err := db.Exec(`DELETE FROM session_audit_events WHERE at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("purge audit: %w", err)
	}
	n, _ := res.RowsAffected()
	return n, nil
}
