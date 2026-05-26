package db

import (
	"database/sql"
	"encoding/json"
	"time"
)

// IMChannel represents a row in the workspace_im_channels table.
type IMChannel struct {
	ID             string
	WorkspaceID    string
	Provider       string
	BotID          string
	UserID         string
	BotToken       string
	BaseURL        string
	Cursor         string
	RequireMention bool
	RoutingMode    string
	BoundAt        time.Time
}

// CreateIMChannel inserts or updates a workspace IM channel record.
// On conflict (same workspace+provider+bot), updates bound_at.
// Returns the channel ID.
func (db *DB) CreateIMChannel(workspaceID, provider, botID, userID string) (string, error) {
	var id string
	err := db.QueryRow(
		`INSERT INTO workspace_im_channels (workspace_id, provider, bot_id, user_id)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (workspace_id, provider, bot_id)
		DO UPDATE SET bound_at = NOW()
		RETURNING id`,
		workspaceID, provider, botID, userID,
	).Scan(&id)
	return id, err
}

// SaveIMChannelCredentials stores bot credentials for a workspace IM channel.
func (db *DB) SaveIMChannelCredentials(channelID, botToken, baseURL string) error {
	_, err := db.Exec(
		`UPDATE workspace_im_channels SET bot_token = $1, base_url = $2 WHERE id = $3`,
		botToken, baseURL, channelID,
	)
	return err
}

// GetIMChannel retrieves a single workspace IM channel by ID.
func (db *DB) GetIMChannel(channelID string) (*IMChannel, error) {
	c := &IMChannel{}
	var botToken, baseURL, cursor, routingMode *string
	err := db.QueryRow(
		`SELECT id, workspace_id, provider, bot_id, user_id, bot_token, base_url, cursor, require_mention, routing_mode, bound_at
		FROM workspace_im_channels WHERE id = $1`,
		channelID,
	).Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.BotID, &c.UserID, &botToken, &baseURL, &cursor, &c.RequireMention, &routingMode, &c.BoundAt)
	if err != nil {
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
	return c, nil
}

// ListIMChannels returns all IM channels for a workspace.
func (db *DB) ListIMChannels(workspaceID string) ([]IMChannel, error) {
	rows, err := db.Query(
		`SELECT id, workspace_id, provider, bot_id, user_id, bot_token, base_url, cursor, require_mention, routing_mode, bound_at
		FROM workspace_im_channels WHERE workspace_id = $1 ORDER BY bound_at`,
		workspaceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []IMChannel
	for rows.Next() {
		var c IMChannel
		var botToken, baseURL, cursor, routingMode *string
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.BotID, &c.UserID, &botToken, &baseURL, &cursor, &c.RequireMention, &routingMode, &c.BoundAt); err != nil {
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

// ListAllActiveChannels returns all IM channels with credentials for a given provider.
// Used by RestoreIMBridgePollers.
func (db *DB) ListAllActiveChannels(provider string) ([]IMChannel, error) {
	rows, err := db.Query(
		`SELECT id, workspace_id, provider, bot_id, user_id, bot_token, base_url, cursor, require_mention, routing_mode, bound_at
		FROM workspace_im_channels
		WHERE provider = $1 AND bot_token IS NOT NULL`,
		provider,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var channels []IMChannel
	for rows.Next() {
		var c IMChannel
		var botToken, baseURL, cursor, routingMode *string
		if err := rows.Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.BotID, &c.UserID, &botToken, &baseURL, &cursor, &c.RequireMention, &routingMode, &c.BoundAt); err != nil {
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

// DispatchInboundChannel returns the fields needed to build a
// BridgeBinding for push-based providers (e.g. WhatsApp webhook).
// Returns sql.ErrNoRows if the channel doesn't exist.
func (db *DB) DispatchInboundChannel(channelID string) (workspaceID, provider, botID, botToken, baseURL, routingMode string, err error) {
	var token, base, mode *string
	err = db.QueryRow(
		`SELECT workspace_id, provider, bot_id, bot_token, base_url, routing_mode
		FROM workspace_im_channels WHERE id = $1`,
		channelID,
	).Scan(&workspaceID, &provider, &botID, &token, &base, &mode)
	if err != nil {
		return
	}
	if token != nil {
		botToken = *token
	}
	if base != nil {
		baseURL = *base
	}
	if mode != nil {
		routingMode = *mode
	}
	return
}

// FindIMChannelByProviderBot looks up a channel by (provider, bot_id).
// Used by push-based webhooks (e.g. WhatsApp Cloud) where the inbound
// payload identifies the receiving account via its provider-specific ID
// (phone_number_id for WhatsApp) rather than the channel UUID.
//
// If the same bot_id appears in multiple workspaces (legal under the
// current UNIQUE(workspace_id, provider, bot_id) constraint), the
// earliest-bound row wins.
func (db *DB) FindIMChannelByProviderBot(provider, botID string) (*IMChannel, error) {
	c := &IMChannel{}
	var botToken, baseURL, cursor, routingMode *string
	err := db.QueryRow(
		`SELECT id, workspace_id, provider, bot_id, user_id, bot_token, base_url, cursor, require_mention, routing_mode, bound_at
		FROM workspace_im_channels
		WHERE provider = $1 AND bot_id = $2
		ORDER BY bound_at ASC
		LIMIT 1`,
		provider, botID,
	).Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.BotID, &c.UserID, &botToken, &baseURL, &cursor, &c.RequireMention, &routingMode, &c.BoundAt)
	if err != nil {
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
	return c, nil
}

// DeleteIMChannel deletes a workspace IM channel by ID.
func (db *DB) DeleteIMChannel(channelID string) error {
	_, err := db.Exec(
		`DELETE FROM workspace_im_channels WHERE id = $1`,
		channelID,
	)
	return err
}

// UpdateIMChannelCursor persists the long-poll cursor for an IM channel.
func (db *DB) UpdateIMChannelCursor(channelID, cursor string) error {
	_, err := db.Exec(
		`UPDATE workspace_im_channels SET cursor = $1 WHERE id = $2`,
		cursor, channelID,
	)
	return err
}

// GetChannelRequireMention returns the require_mention flag for a channel.
func (db *DB) GetChannelRequireMention(channelID string) (bool, error) {
	var v bool
	err := db.QueryRow(`SELECT require_mention FROM workspace_im_channels WHERE id = $1`, channelID).Scan(&v)
	return v, err
}

// UpdateIMChannelSettings updates channel settings.
func (db *DB) UpdateIMChannelSettings(channelID string, requireMention bool) error {
	_, err := db.Exec(
		`UPDATE workspace_im_channels SET require_mention = $1 WHERE id = $2`,
		requireMention, channelID,
	)
	return err
}

// UpdateIMChannelRoutingMode updates the routing_mode column for a channel.
// Caller is expected to validate `mode` before calling (valid values:
// "nanoclaw", "codex"). Unknown values are accepted by the DB but will
// cause forwardMessage to fall through to the default nanoclaw branch
// (also captures legacy "stateless_cc" rows pre-#151 cleanup).
func (db *DB) UpdateIMChannelRoutingMode(channelID, mode string) error {
	_, err := db.Exec(
		`UPDATE workspace_im_channels SET routing_mode = $1 WHERE id = $2`,
		mode, channelID,
	)
	return err
}

// UpsertChannelMeta inserts or updates a channel-specific metadata entry.
func (db *DB) UpsertChannelMeta(channelID, userID, key, value string) error {
	_, err := db.Exec(
		`INSERT INTO workspace_im_channel_meta (channel_id, user_id, meta_key, meta_value, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (channel_id, user_id, meta_key)
		DO UPDATE SET meta_value = $4, updated_at = NOW()`,
		channelID, userID, key, value,
	)
	return err
}

// GetChannelMeta retrieves a channel-specific metadata value.
func (db *DB) GetChannelMeta(channelID, userID, key string) (string, error) {
	var value string
	err := db.QueryRow(
		`SELECT meta_value FROM workspace_im_channel_meta WHERE channel_id = $1 AND user_id = $2 AND meta_key = $3`,
		channelID, userID, key,
	).Scan(&value)
	return value, err
}

// GetAllChannelMeta retrieves all metadata entries for a user on a channel.
func (db *DB) GetAllChannelMeta(channelID, userID string) (map[string]string, error) {
	rows, err := db.Query(
		`SELECT meta_key, meta_value FROM workspace_im_channel_meta WHERE channel_id = $1 AND user_id = $2`,
		channelID, userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	meta := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		meta[k] = v
	}
	return meta, rows.Err()
}

// BindSandboxToChannel binds a sandbox to a workspace IM channel with
// 1:1 semantics: any other sandbox previously bound to this channel is
// unbound first. Writes to both the legacy sandboxes.im_channel_id FK
// AND the sandbox_channel_bindings junction (dual-write) so that the
// junction-first readers see the same view.
//
// For N:1 semantics (shared routing), use BindSandboxChannels instead —
// it does not displace other sandboxes already bound to those channels.
func (db *DB) BindSandboxToChannel(sandboxID, channelID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Legacy FK: clear any other sandbox holding this channel, then set ours.
	if _, err := tx.Exec(
		`UPDATE sandboxes SET im_channel_id = NULL WHERE im_channel_id = $1`,
		channelID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`UPDATE sandboxes SET im_channel_id = $1 WHERE id = $2`,
		channelID, sandboxID,
	); err != nil {
		return err
	}

	// Junction: same 1:1 semantics — drop any existing rows for this
	// channel, then insert ours.
	if _, err := tx.Exec(
		`DELETE FROM sandbox_channel_bindings WHERE channel_id = $1`,
		channelID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO sandbox_channel_bindings (sandbox_id, channel_id)
		VALUES ($1, $2)
		ON CONFLICT (sandbox_id, channel_id) DO NOTHING`,
		sandboxID, channelID,
	); err != nil {
		return err
	}

	return tx.Commit()
}

// UnbindSandboxFromChannel removes every IM channel binding from a sandbox.
// Clears both the legacy FK and the junction rows.
func (db *DB) UnbindSandboxFromChannel(sandboxID string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`UPDATE sandboxes SET im_channel_id = NULL WHERE id = $1`,
		sandboxID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`DELETE FROM sandbox_channel_bindings WHERE sandbox_id = $1`,
		sandboxID,
	); err != nil {
		return err
	}
	return tx.Commit()
}

// GetSandboxForChannel returns the running sandbox bound to a channel.
// Resolution order: junction table first (multi-channel-aware), then
// fallback to the legacy sandboxes.im_channel_id FK for data not yet
// dual-written. Returns sql.ErrNoRows if no sandbox is bound or none is
// running.
func (db *DB) GetSandboxForChannel(channelID string) (sandboxID, podIP, bridgeSecret, assistantName string, err error) {
	sandboxID, podIP, bridgeSecret, assistantName, err = db.GetSandboxForChannelViaBinding(channelID)
	if err == nil {
		return
	}
	if err != sql.ErrNoRows {
		return
	}
	var metadataJSON []byte
	err = db.QueryRow(
		`SELECT id, pod_ip, nanoclaw_bridge_secret, metadata FROM sandboxes
		WHERE im_channel_id = $1 AND status = 'running' AND pod_ip != ''`,
		channelID,
	).Scan(&sandboxID, &podIP, &bridgeSecret, &metadataJSON)
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

// GetIMChannelForSandbox returns the IM channel bound to a sandbox.
// Junction first, FK fallback. If multiple channels are bound (shared
// mode), the most recently bound one wins — callers needing the full
// list should use GetChannelsForSandbox.
//
// Returns sql.ErrNoRows when no channel is bound.
func (db *DB) GetIMChannelForSandbox(sandboxID string) (*IMChannel, error) {
	c := &IMChannel{}
	var botToken, baseURL, cursor, routingMode *string

	err := db.QueryRow(
		`SELECT c.id, c.workspace_id, c.provider, c.bot_id, c.user_id, c.bot_token, c.base_url, c.cursor, c.require_mention, c.routing_mode, c.bound_at
		FROM sandbox_channel_bindings b
		JOIN workspace_im_channels c ON c.id = b.channel_id
		WHERE b.sandbox_id = $1
		ORDER BY b.bound_at DESC
		LIMIT 1`,
		sandboxID,
	).Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.BotID, &c.UserID, &botToken, &baseURL, &cursor, &c.RequireMention, &routingMode, &c.BoundAt)

	if err == sql.ErrNoRows {
		err = db.QueryRow(
			`SELECT c.id, c.workspace_id, c.provider, c.bot_id, c.user_id, c.bot_token, c.base_url, c.cursor, c.require_mention, c.routing_mode, c.bound_at
			FROM workspace_im_channels c
			JOIN sandboxes s ON s.im_channel_id = c.id
			WHERE s.id = $1`,
			sandboxID,
		).Scan(&c.ID, &c.WorkspaceID, &c.Provider, &c.BotID, &c.UserID, &botToken, &baseURL, &cursor, &c.RequireMention, &routingMode, &c.BoundAt)
	}
	if err != nil {
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
	return c, nil
}
