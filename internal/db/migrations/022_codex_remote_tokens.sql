CREATE TABLE IF NOT EXISTS codex_remote_tokens (
    id              TEXT PRIMARY KEY,
    user_id         TEXT NOT NULL,
    workspace_id    TEXT NOT NULL,
    name            TEXT NOT NULL,
    token_hash      TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at      TIMESTAMPTZ NOT NULL,
    last_used_at    TIMESTAMPTZ,
    revoked_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_codex_tokens_user_workspace
    ON codex_remote_tokens(user_id, workspace_id);
CREATE INDEX IF NOT EXISTS idx_codex_tokens_workspace
    ON codex_remote_tokens(workspace_id);
