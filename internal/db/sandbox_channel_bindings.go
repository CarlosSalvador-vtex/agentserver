package db

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

// BindSandboxChannels adds N channel bindings to a sandbox without
// displacing other sandboxes already bound to those channels. Used by
// 'shared' routing strategy when multiple channels should converge on
// a single sandbox.
//
// Idempotent: re-binding an existing (sandbox, channel) pair is a no-op.
func (db *DB) BindSandboxChannels(sandboxID string, channelIDs []string) error {
	if len(channelIDs) == 0 {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()
	for _, channelID := range channelIDs {
		if _, err := tx.Exec(
			`INSERT INTO sandbox_channel_bindings (sandbox_id, channel_id)
			VALUES ($1, $2)
			ON CONFLICT (sandbox_id, channel_id) DO NOTHING`,
			sandboxID, channelID,
		); err != nil {
			return fmt.Errorf("bind channel %s: %w", channelID, err)
		}
	}
	return tx.Commit()
}

// UnbindSandboxChannel removes a single (sandbox, channel) pair from
// the junction. Does not touch the legacy sandboxes.im_channel_id FK.
func (db *DB) UnbindSandboxChannel(sandboxID, channelID string) error {
	_, err := db.Exec(
		`DELETE FROM sandbox_channel_bindings
		WHERE sandbox_id = $1 AND channel_id = $2`,
		sandboxID, channelID,
	)
	return err
}

// UnbindAllSandboxChannels clears every junction row for a sandbox.
// Defensive — sandbox deletion already cascades via FK.
func (db *DB) UnbindAllSandboxChannels(sandboxID string) error {
	_, err := db.Exec(
		`DELETE FROM sandbox_channel_bindings WHERE sandbox_id = $1`,
		sandboxID,
	)
	return err
}

// GetSandboxForChannelViaBinding resolves the running sandbox bound to
// a channel via the junction table. Returns sql.ErrNoRows when no
// running sandbox is bound.
//
// If multiple sandboxes are bound to the same channel (legal in hybrid
// rollouts), the most recently bound running sandbox wins.
func (db *DB) GetSandboxForChannelViaBinding(channelID string) (sandboxID, podIP, bridgeSecret, assistantName string, err error) {
	var metadataJSON []byte
	var bridgeSecretNull sql.NullString
	err = db.QueryRow(
		`SELECT s.id, s.pod_ip, s.nanoclaw_bridge_secret, s.metadata
		FROM sandbox_channel_bindings b
		JOIN sandboxes s ON s.id = b.sandbox_id
		WHERE b.channel_id = $1
		  AND s.status = 'running'
		  AND s.pod_ip != ''
		ORDER BY b.bound_at DESC
		LIMIT 1`,
		channelID,
	).Scan(&sandboxID, &podIP, &bridgeSecretNull, &metadataJSON)
	if bridgeSecretNull.Valid {
		bridgeSecret = bridgeSecretNull.String
	}
	if err == nil && len(metadataJSON) > 0 {
		var meta map[string]interface{}
		if json.Unmarshal(metadataJSON, &meta) == nil {
			if v, ok := meta["assistant_name"].(string); ok {
				assistantName = v
			}
		}
	}
	return
}

// GetPausedSandboxForChannel returns the ID of the most-recently-bound sandbox
// that is in the 'paused' state for the given channel. Used by the OpenClaw IM
// turn handler to auto-resume a sandbox that was paused by the idle watcher.
// Returns sql.ErrNoRows when no paused sandbox is found.
func (db *DB) GetPausedSandboxForChannel(channelID string) (sandboxID string, err error) {
	err = db.QueryRow(
		`SELECT s.id
		FROM sandbox_channel_bindings b
		JOIN sandboxes s ON s.id = b.sandbox_id
		WHERE b.channel_id = $1
		  AND s.status = 'paused'
		ORDER BY b.bound_at DESC
		LIMIT 1`,
		channelID,
	).Scan(&sandboxID)
	return
}

// GetChannelsForSandbox returns every channel bound to a sandbox via
// the junction. Empty slice when no bindings exist.
func (db *DB) GetChannelsForSandbox(sandboxID string) ([]IMChannel, error) {
	rows, err := db.Query(
		`SELECT c.id, c.workspace_id, c.provider, c.bot_id, c.user_id,
		        c.bot_token, c.base_url, c.cursor, c.require_mention,
		        c.routing_mode, c.bound_at
		FROM sandbox_channel_bindings b
		JOIN workspace_im_channels c ON c.id = b.channel_id
		WHERE b.sandbox_id = $1
		ORDER BY b.bound_at`,
		sandboxID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []IMChannel
	for rows.Next() {
		var c IMChannel
		var botToken, baseURL, cursor, routingMode *string
		if err := rows.Scan(
			&c.ID, &c.WorkspaceID, &c.Provider, &c.BotID, &c.UserID,
			&botToken, &baseURL, &cursor, &c.RequireMention,
			&routingMode, &c.BoundAt,
		); err != nil {
			return nil, err
		}
		if botToken != nil {
			c.BotToken = *botToken
		}
		if baseURL != nil {
			c.BaseURL = *baseURL
		}
		if cursor != nil {
			c.Cursor = *cursor
		}
		if routingMode != nil {
			c.RoutingMode = *routingMode
		}
		channels = append(channels, c)
	}
	return channels, rows.Err()
}

// GetSharedSandbox returns the sandbox most likely to act as the
// "shared" sandbox for a workspace — the running sandbox holding the
// largest number of channel bindings. Returns sql.ErrNoRows if none.
//
// Used by the per_agent/shared router (PR 2) to decide whether to
// reuse an existing sandbox vs provision a new one.
func (db *DB) GetSharedSandbox(workspaceID string) (sandboxID string, err error) {
	err = db.QueryRow(
		`SELECT s.id
		FROM sandboxes s
		JOIN sandbox_channel_bindings b ON b.sandbox_id = s.id
		WHERE s.workspace_id = $1
		  AND s.status = 'running'
		GROUP BY s.id
		ORDER BY COUNT(b.channel_id) DESC, s.created_at ASC
		LIMIT 1`,
		workspaceID,
	).Scan(&sandboxID)
	if err == sql.ErrNoRows {
		return "", err
	}
	return sandboxID, err
}
