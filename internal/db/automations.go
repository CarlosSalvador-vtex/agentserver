package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// Automation is a scheduled proactive run configuration for a workspace.
type Automation struct {
	ID          string
	WorkspaceID string
	Name        string
	SkillRef    string
	Cron        string
	ChannelID   string
	Config      json.RawMessage
	Enabled     bool
	LastRunAt   *time.Time
	NextRunAt   *time.Time
	LastError   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

var standardCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// ComputeNextRun returns the next fire time after from for a standard 5-field cron expression.
func ComputeNextRun(cronExpr string, from time.Time) (time.Time, error) {
	sched, err := standardCronParser.Parse(cronExpr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse cron %q: %w", cronExpr, err)
	}
	return sched.Next(from), nil
}

// ScanDueAutomations returns enabled automations with next_run_at <= now, ordered by next_run_at.
func (db *DB) ScanDueAutomations(ctx context.Context) ([]Automation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, workspace_id, name, skill_ref, cron, channel_id, config, enabled,
		        last_run_at, next_run_at, last_error, created_at, updated_at
		 FROM automations
		 WHERE enabled AND next_run_at IS NOT NULL AND next_run_at <= NOW()
		 ORDER BY next_run_at ASC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Automation
	for rows.Next() {
		var a Automation
		var config []byte
		var lastRun, nextRun sql.NullTime
		var lastErr sql.NullString
		if err := rows.Scan(
			&a.ID, &a.WorkspaceID, &a.Name, &a.SkillRef, &a.Cron, &a.ChannelID, &config,
			&a.Enabled, &lastRun, &nextRun, &lastErr, &a.CreatedAt, &a.UpdatedAt,
		); err != nil {
			return nil, err
		}
		a.Config = json.RawMessage(config)
		if lastRun.Valid {
			a.LastRunAt = &lastRun.Time
		}
		if nextRun.Valid {
			a.NextRunAt = &nextRun.Time
		}
		if lastErr.Valid {
			a.LastError = &lastErr.String
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// MarkAutomationRun records the outcome of a scheduled fire and schedules the next run.
// If nextRun is zero, next_run_at is cleared (e.g. malformed cron).
func (db *DB) MarkAutomationRun(ctx context.Context, id string, runAt time.Time, lastErr *string, nextRun time.Time) error {
	var next interface{}
	if nextRun.IsZero() {
		next = nil
	} else {
		next = nextRun
	}
	_, err := db.ExecContext(ctx,
		`UPDATE automations SET last_run_at = $2, last_error = $3, next_run_at = $4, updated_at = NOW() WHERE id = $1`,
		id, runAt, lastErr, next,
	)
	return err
}

// CreateAutomation inserts a row (used by tests and future APIs).
func (db *DB) CreateAutomation(ctx context.Context, a *Automation) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO automations (id, workspace_id, name, skill_ref, cron, channel_id, config, enabled, next_run_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		a.ID, a.WorkspaceID, a.Name, a.SkillRef, a.Cron, a.ChannelID, a.Config, a.Enabled, a.NextRunAt,
	)
	return err
}

// DeleteAutomation removes a row (test cleanup).
func (db *DB) DeleteAutomation(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM automations WHERE id = $1`, id)
	return err
}

// GetAutomation loads a single automation by id.
func (db *DB) GetAutomation(ctx context.Context, id string) (*Automation, error) {
	var a Automation
	var config []byte
	var lastRun, nextRun sql.NullTime
	var lastErr sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT id, workspace_id, name, skill_ref, cron, channel_id, config, enabled,
		        last_run_at, next_run_at, last_error, created_at, updated_at
		 FROM automations WHERE id = $1`,
		id,
	).Scan(
		&a.ID, &a.WorkspaceID, &a.Name, &a.SkillRef, &a.Cron, &a.ChannelID, &config,
		&a.Enabled, &lastRun, &nextRun, &lastErr, &a.CreatedAt, &a.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	a.Config = json.RawMessage(config)
	if lastRun.Valid {
		a.LastRunAt = &lastRun.Time
	}
	if nextRun.Valid {
		a.NextRunAt = &nextRun.Time
	}
	if lastErr.Valid {
		a.LastError = &lastErr.String
	}
	return &a, nil
}
