-- 041_workspace_invites.sql — workspace invites by email (B01)
--
-- Backs POST /api/workspaces/{wid}/invites + /accept-invite flow.
-- token_hash = sha256(token); plaintext token only lives in the
-- emitted URL/email — never stored.

CREATE TABLE IF NOT EXISTS workspace_invites (
    id             TEXT PRIMARY KEY,
    workspace_id   TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    email          TEXT NOT NULL,
    role           TEXT NOT NULL DEFAULT 'developer',
    token_hash     TEXT NOT NULL,
    expires_at     TIMESTAMPTZ NOT NULL,
    accepted_at    TIMESTAMPTZ,
    created_by     TEXT NOT NULL REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One pending invite per (workspace, email).
CREATE UNIQUE INDEX IF NOT EXISTS uniq_workspace_invites_pending
    ON workspace_invites(workspace_id, email)
    WHERE accepted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_workspace_invites_token_hash
    ON workspace_invites(token_hash);
