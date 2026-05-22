-- workspace_api_keys: long-lived workspace-scoped developer API keys.
-- Used by external integrators (bots, IM bridges, webhooks) to call
-- codex-app-gateway /api/turns via Authorization: Bearer wak_...
--
-- Key format on the wire: wak_<8-char prefix>_<40-char secret>
-- DB stores:
--   - id    = "wak_<prefix>" (also indexed for O(1) bearer lookup)
--   - secret_hash = hex(sha256(presented_secret))
--   - scopes = action-based scope strings, e.g. {'turns:submit'}
-- Secrets are CSPRNG (25 bytes → ~200 bits entropy); SHA-256 is sufficient.
CREATE TABLE workspace_api_keys (
    id            TEXT        PRIMARY KEY,
    workspace_id  TEXT        NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    user_id       TEXT        NOT NULL REFERENCES users(id)       ON DELETE CASCADE,
    name          TEXT        NOT NULL,
    prefix        TEXT        NOT NULL,
    secret_hash   TEXT        NOT NULL,
    scopes        TEXT[]      NOT NULL DEFAULT '{}',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at  TIMESTAMPTZ,
    revoked_at    TIMESTAMPTZ
);

-- Lookup index for bearer validation. Partial index keeps it small
-- by excluding revoked keys (the common path is "key still active").
CREATE INDEX idx_workspace_api_keys_workspace_active
    ON workspace_api_keys (workspace_id)
    WHERE revoked_at IS NULL;
