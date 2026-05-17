-- v0.54.0: each workspace_executors binding gets a human-readable
-- name (unique per workspace) plus a per-binding description. The
-- name is what the LLM sees through env-mcp's list_environments
-- (the underlying exe_id stays an internal identifier).
--
-- Backfill rule for existing rows: name = 'executor-<first-8-of-exe_id>'.
-- This is unique-per-workspace by construction (exe_ids are unique).

ALTER TABLE workspace_executors
    ADD COLUMN IF NOT EXISTS name        TEXT,
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';

UPDATE workspace_executors
   SET name = 'executor-' || SUBSTRING(exe_id FROM 5 FOR 8)
 WHERE name IS NULL;

ALTER TABLE workspace_executors
    ALTER COLUMN name SET NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uniq_workspace_executors_name
    ON workspace_executors (workspace_id, name);
