-- 039_token_active_workspace.sql — workspace-scoped session
--
-- Adds active_workspace_id to auth_tokens so a session cookie can carry
-- the workspace the user is currently operating on. NULL = no workspace
-- selected (e.g. fresh login, or user belongs to zero workspaces).
--
-- ON DELETE SET NULL: deleting a workspace forces existing sessions to
-- re-select rather than authenticate against a dangling reference.
-- ON DELETE CASCADE is wrong here — we want the session to survive.

ALTER TABLE auth_tokens
    ADD COLUMN IF NOT EXISTS active_workspace_id TEXT
        REFERENCES workspaces(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_auth_tokens_active_workspace
    ON auth_tokens(active_workspace_id)
    WHERE active_workspace_id IS NOT NULL;
