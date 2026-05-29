-- Scheduled workspace automations (productized automations PR1).
CREATE TABLE IF NOT EXISTS automations (
    id            TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    workspace_id  TEXT NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    name          TEXT NOT NULL,
    skill_ref     TEXT NOT NULL DEFAULT '',
    cron          TEXT NOT NULL,
    channel_id    TEXT NOT NULL REFERENCES workspace_im_channels(id) ON DELETE CASCADE,
    config        JSONB NOT NULL DEFAULT '{}',
    enabled       BOOLEAN NOT NULL DEFAULT true,
    last_run_at   TIMESTAMPTZ,
    next_run_at   TIMESTAMPTZ,
    last_error    TEXT,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_automations_workspace ON automations(workspace_id);
CREATE INDEX IF NOT EXISTS idx_automations_due ON automations(next_run_at) WHERE enabled;
