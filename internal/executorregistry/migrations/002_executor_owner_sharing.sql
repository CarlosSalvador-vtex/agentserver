-- 002_executor_owner_sharing.sql
-- Adds owner_user_id (for cross-user invocation hard-deny) and
-- shared_to_workspace (v1.x dual-consent opt-in; v1 always FALSE).

ALTER TABLE executors
  ADD COLUMN IF NOT EXISTS owner_user_id          TEXT,
  ADD COLUMN IF NOT EXISTS shared_to_workspace    BOOLEAN NOT NULL DEFAULT FALSE;

-- Restrictive cross-user policy. Legacy executors keep owner_user_id NULL.
-- GetExecutor (Task 4) projects NULL → 'unknown' via COALESCE; gate.Check
-- (Task 6) compares 'unknown' against the session creator's real user id,
-- which never matches → cross_user_denied. Legacy executors must be
-- re-registered to be invocable. See spec §4.11.

CREATE INDEX IF NOT EXISTS idx_executors_owner ON executors(owner_user_id);
