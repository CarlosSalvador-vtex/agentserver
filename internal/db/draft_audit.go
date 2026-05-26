package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Sprint 3 PR-3 (improvements.md #14). Append-only audit timeline for
// playground drafts. The poller (#8) writes here too in a later iteration;
// for now only the handler success paths emit events.

type DraftAuditEvent struct {
	ID          int64
	DraftKind   string
	DraftID     string
	ActorUserID sql.NullString
	Action      string
	PayloadDiff map[string]interface{}
	CreatedAt   time.Time
}

// AppendDraftAuditEvent writes one row. Best-effort: returns the error so
// the caller can log without failing the user-facing operation.
func (db *DB) AppendDraftAuditEvent(kind, draftID, actorUserID, action string, payload map[string]interface{}) error {
	var diffJSON []byte
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("marshal payload: %w", err)
		}
		diffJSON = b
	}
	_, err := db.Exec(
		`INSERT INTO draft_audit_events (draft_kind, draft_id, actor_user_id, action, payload_diff)
		 VALUES ($1, $2, $3, $4, $5)`,
		kind, draftID, nullIfEmpty(actorUserID), action, diffJSON,
	)
	return err
}

// ListDraftAuditEvents returns the most recent N events for a given draft,
// newest first. Used by the timeline UI.
func (db *DB) ListDraftAuditEvents(kind, draftID string, limit int) ([]DraftAuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := db.Query(
		`SELECT id, draft_kind, draft_id, actor_user_id, action, payload_diff, created_at
		 FROM draft_audit_events
		 WHERE draft_kind = $1 AND draft_id = $2
		 ORDER BY created_at DESC
		 LIMIT $3`,
		kind, draftID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list draft audit: %w", err)
	}
	defer rows.Close()

	var out []DraftAuditEvent
	for rows.Next() {
		var e DraftAuditEvent
		if err := rows.Scan(&e.ID, &e.DraftKind, &e.DraftID, &e.ActorUserID, &e.Action, jsonScanner(&e.PayloadDiff), &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan draft audit: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
