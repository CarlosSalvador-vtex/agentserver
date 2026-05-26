-- 034_draft_audit.sql — Sprint 3 PR-3 (improvements.md #14)
--
-- Append-only timeline of who did what to each draft. Powers per-draft audit
-- timelines in the playground UI and answers "who broke production?" when a
-- promote lands a bad change.
--
-- draft_kind is "skill" | "soul" (validated at the app boundary).
-- action is one of: created | patched | archived | promoted | dry-run |
-- test-sandbox (also validated app-side; DB stays permissive for forward-
-- compat with #18 marketplace fork etc.).

CREATE TABLE IF NOT EXISTS draft_audit_events (
    id              BIGSERIAL PRIMARY KEY,
    draft_kind      TEXT NOT NULL,
    draft_id        TEXT NOT NULL,
    actor_user_id   TEXT REFERENCES users(id) ON DELETE SET NULL,
    action          TEXT NOT NULL,
    payload_diff    JSONB,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_draft_audit_kind_id
    ON draft_audit_events(draft_kind, draft_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_draft_audit_actor
    ON draft_audit_events(actor_user_id, created_at DESC);
