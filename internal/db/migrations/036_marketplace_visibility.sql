-- 036_marketplace_visibility.sql — Sprint 5 PR-2 (improvements.md #18)
--
-- Adds visibility column to skill/soul drafts for marketplace sharing.
-- 'private' = visible only to author's workspace (default, backward-compat).
-- 'shared'  = listed in the marketplace, forkable by any authenticated user.
--
-- Moderation: only users with role='admin' can set visibility='shared'.
-- Forking creates a new 'private' draft in the caller's workspace.

ALTER TABLE skill_drafts
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private', 'shared'));

ALTER TABLE soul_drafts
    ADD COLUMN IF NOT EXISTS visibility TEXT NOT NULL DEFAULT 'private'
        CHECK (visibility IN ('private', 'shared'));

-- Marketplace listing hits visibility='shared'; index speeds up the scan.
CREATE INDEX IF NOT EXISTS idx_skill_drafts_visibility
    ON skill_drafts(visibility) WHERE visibility = 'shared';

CREATE INDEX IF NOT EXISTS idx_soul_drafts_visibility
    ON soul_drafts(visibility) WHERE visibility = 'shared';
