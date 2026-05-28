ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_workspaces_deleted_at ON workspaces (deleted_at) WHERE deleted_at IS NULL;
