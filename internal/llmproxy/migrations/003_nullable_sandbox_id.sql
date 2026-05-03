-- Workspace-scoped proxy tokens (used by cc-broker turn workers) have no
-- sandbox identity. Allow sandbox_id NULL so usage / trace rows for those
-- tokens can record only workspace_id without a synthetic sandbox value.
ALTER TABLE traces ALTER COLUMN sandbox_id DROP NOT NULL;
ALTER TABLE usage  ALTER COLUMN sandbox_id DROP NOT NULL;
