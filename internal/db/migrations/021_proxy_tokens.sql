-- Unified proxy token table.
--
-- llmproxy validation now goes through this single table regardless of
-- whether the token is sandbox-scoped (issued at sandbox creation) or
-- workspace-scoped (issued by cc-broker for its turn workers, which are
-- not per-sandbox identities).
--
-- A token is opaque to the transport layer; it can be sent as either
-- `x-api-key` or `Authorization: Bearer <token>`. The token_type
-- discriminator tells llmproxy whether to apply sandbox status checks
-- ('running' / 'creating') or workspace-only validation.
--
-- For now sandboxes.proxy_token is left in place as a denormalized
-- convenience for the many call sites that read Sandbox.ProxyToken
-- directly. CreateSandbox writes both. A future migration can drop the
-- column once those readers move to GetProxyTokenForSandbox.

CREATE TABLE IF NOT EXISTS proxy_tokens (
    token        TEXT PRIMARY KEY,
    token_type   TEXT NOT NULL CHECK (token_type IN ('sandbox', 'workspace')),
    sandbox_id   TEXT REFERENCES sandboxes(id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    CHECK ((token_type = 'sandbox'   AND sandbox_id IS NOT NULL) OR
           (token_type = 'workspace' AND sandbox_id IS NULL))
);

-- Workspace tokens are at most one per workspace.
CREATE UNIQUE INDEX IF NOT EXISTS idx_proxy_tokens_workspace_unique
    ON proxy_tokens (workspace_id) WHERE token_type = 'workspace';

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_sandbox
    ON proxy_tokens (sandbox_id) WHERE sandbox_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_proxy_tokens_workspace
    ON proxy_tokens (workspace_id);

-- Backfill existing sandbox tokens.
INSERT INTO proxy_tokens (token, token_type, sandbox_id, workspace_id, created_at)
SELECT proxy_token, 'sandbox', id, workspace_id, created_at
  FROM sandboxes
 WHERE proxy_token IS NOT NULL AND proxy_token != ''
ON CONFLICT (token) DO NOTHING;
