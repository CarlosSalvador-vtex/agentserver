-- 002_executor_owner_sharing.sql
-- Adds owner_user_id (for cross-user invocation hard-deny) and
-- shared_to_workspace (v1.x dual-consent opt-in; v1 always FALSE).

ALTER TABLE executors
  ADD COLUMN IF NOT EXISTS owner_user_id          TEXT,
  ADD COLUMN IF NOT EXISTS shared_to_workspace    BOOLEAN NOT NULL DEFAULT FALSE;

-- Same reasoning as agent_sessions.creator_user_id: legacy executors keep
-- owner_user_id NULL. cc-broker treats empty/NULL as "owner unknown, skip
-- cross-user check" (§6.4).

CREATE INDEX IF NOT EXISTS idx_executors_owner ON executors(owner_user_id);
