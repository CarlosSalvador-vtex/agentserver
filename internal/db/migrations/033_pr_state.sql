-- 033_pr_state.sql — Sprint 2 PR-5 (improvements.md #8)
--
-- Closes the feedback loop between Promote → real production landing by
-- recording the live GitHub PR state. A background poller in
-- internal/server/playground_promote_poll.go updates these every ~5 min.
--
-- Valid values: 'open' | 'merged' | 'closed' | NULL (unknown / not yet polled)
-- App layer enforces values; DB stays permissive (forward-compat for future
-- states like 'draft_pr').

ALTER TABLE skill_drafts
    ADD COLUMN IF NOT EXISTS promoted_pr_state TEXT;

ALTER TABLE soul_drafts
    ADD COLUMN IF NOT EXISTS promoted_pr_state TEXT;

-- Index for the poller scan: list rows in 'promoted' status with a PR URL
-- whose state hasn't been polled yet (NULL) or is still 'open'.
CREATE INDEX IF NOT EXISTS idx_skill_drafts_promote_poll
    ON skill_drafts(promoted_pr_state)
    WHERE status = 'promoted' AND promoted_pr_url IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_soul_drafts_promote_poll
    ON soul_drafts(promoted_pr_state)
    WHERE status = 'promoted' AND promoted_pr_url IS NOT NULL;
