-- 042_session_audit_events.sql — session-level audit log (B07)
--
-- Immutable record of sensitive actions per (user, active_workspace).
-- Backs SOC2 control 7.3 + debug-ability + anomaly detection.
--
-- Retention: 90 days default; purge handled by an external cron (TBD).

CREATE TABLE IF NOT EXISTS session_audit_events (
    id              BIGSERIAL PRIMARY KEY,
    user_id         TEXT REFERENCES users(id) ON DELETE SET NULL,
    workspace_id    TEXT REFERENCES workspaces(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    details         JSONB,
    request_method  TEXT,
    request_path    TEXT,
    response_status INTEGER,
    ip              TEXT,
    user_agent      TEXT,
    error_msg       TEXT,
    at              TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_audit_workspace_at
    ON session_audit_events(workspace_id, at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_user_at
    ON session_audit_events(user_id, at DESC)
    WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_audit_event_at
    ON session_audit_events(event_type, at DESC);
