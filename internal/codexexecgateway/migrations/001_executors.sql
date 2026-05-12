CREATE TABLE IF NOT EXISTS executors (
    exe_id                  TEXT PRIMARY KEY,
    user_id                 TEXT NOT NULL,
    display_name            TEXT,
    description             TEXT,
    default_cwd             TEXT,
    registration_token_hash TEXT NOT NULL,
    registered_at           TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_seen_at            TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_executors_user ON executors(user_id);

CREATE TABLE IF NOT EXISTS workspace_executors (
    workspace_id TEXT NOT NULL,
    exe_id       TEXT NOT NULL REFERENCES executors(exe_id) ON DELETE CASCADE,
    is_default   BOOLEAN NOT NULL DEFAULT false,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, exe_id)
);

CREATE INDEX IF NOT EXISTS idx_workspace_executors_workspace ON workspace_executors(workspace_id);
