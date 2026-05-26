-- 035_tenant_scope.sql — Sprint 4 PR-3 (improvements.md #17)
--
-- Adds per-tenant scope to playground drafts. workspace_id NULL means
-- "system template visible to all tenants"; non-null restricts visibility
-- to members of that workspace.
--
-- Existing rows pre-migration keep NULL (treated as system-wide) so we
-- don't lock anyone out of their own drafts during the rollout. App-layer
-- backfill (a follow-up admin one-shot) can flip rows to the author's
-- workspace once the UI surface is in place.

ALTER TABLE skill_drafts
    ADD COLUMN IF NOT EXISTS workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;

ALTER TABLE soul_drafts
    ADD COLUMN IF NOT EXISTS workspace_id TEXT REFERENCES workspaces(id) ON DELETE CASCADE;

-- Read patterns: list-by-author hits "author = ?", list-by-workspace hits
-- "workspace_id = ? OR workspace_id IS NULL". Index for the scope path.
CREATE INDEX IF NOT EXISTS idx_skill_drafts_workspace
    ON skill_drafts(workspace_id);

CREATE INDEX IF NOT EXISTS idx_soul_drafts_workspace
    ON soul_drafts(workspace_id);
