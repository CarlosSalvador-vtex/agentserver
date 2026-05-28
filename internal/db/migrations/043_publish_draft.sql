-- Migration 043: add 'published' status to skill_drafts and soul_drafts
-- Replaces "Promote → PR" (git-based) with a DB-only publish flow.
-- A published draft is served directly by the sandbox instead of the git
-- system template, giving end users zero-git self-service publishing.

-- Partial unique index: at most one published draft per name+workspace.
-- Allows multiple drafts per name (only one can be published at a time).
CREATE UNIQUE INDEX IF NOT EXISTS idx_skill_drafts_published_unique
    ON skill_drafts (name, workspace_id)
    WHERE status = 'published';

CREATE UNIQUE INDEX IF NOT EXISTS idx_soul_drafts_published_unique
    ON soul_drafts (name, workspace_id)
    WHERE status = 'published';

-- Index for efficient lookup by name+workspace (sandbox resolution hot path).
CREATE INDEX IF NOT EXISTS idx_skill_drafts_name_workspace
    ON skill_drafts (name, workspace_id)
    WHERE status = 'published';

CREATE INDEX IF NOT EXISTS idx_soul_drafts_name_workspace
    ON soul_drafts (name, workspace_id)
    WHERE status = 'published';
