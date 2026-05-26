-- Playground — soul + skill drafts (DB) + composition refs on sandboxes.
-- Production templates still live in git under deploy/helm/agentserver/
-- {souls,skills}/<name>/. Drafts are mutable Postgres rows the playground
-- API exposes for iteration; promote turns a draft into a git PR.
-- See docs/playground-design.md for the full design.

CREATE TABLE IF NOT EXISTS skill_drafts (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    author_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    -- files is a flat map: {"index.mjs": "...", "prompt.md": "...",
    -- "references/leads.json": "..."}. Nested paths use "/" verbatim
    -- (only the in-pod ConfigMap mount needs the "__" encoding; the DB
    -- stores the human-readable path).
    files           JSONB NOT NULL DEFAULT '{}'::jsonb,
    status          TEXT NOT NULL DEFAULT 'draft',
    -- draft | promoting | promoted | archived
    promoted_pr_url TEXT,
    promoted_commit TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (author_user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_skill_drafts_status ON skill_drafts(status);
CREATE INDEX IF NOT EXISTS idx_skill_drafts_author ON skill_drafts(author_user_id);

CREATE TABLE IF NOT EXISTS soul_drafts (
    id              TEXT PRIMARY KEY DEFAULT gen_random_uuid()::text,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    author_user_id  TEXT REFERENCES users(id) ON DELETE SET NULL,
    -- Frontmatter held as structured JSONB so the API can validate
    -- against the soul.md schema (see docs/playground-design.md §4.2)
    -- without re-parsing YAML on every read.
    frontmatter     JSONB NOT NULL DEFAULT '{}'::jsonb,
    body            TEXT NOT NULL DEFAULT '',
    schema_version  TEXT NOT NULL DEFAULT 'v1',
    status          TEXT NOT NULL DEFAULT 'draft',
    promoted_pr_url TEXT,
    promoted_commit TEXT,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (author_user_id, name)
);
CREATE INDEX IF NOT EXISTS idx_soul_drafts_status ON soul_drafts(status);
CREATE INDEX IF NOT EXISTS idx_soul_drafts_author ON soul_drafts(author_user_id);

-- 1 row per sandbox carrying its composition refs. Cascade-deleted with
-- the sandbox. Refs follow the grammar:
--   git:<name>@<sha>     production (chart-mounted ConfigMap)
--   draft:<uuid>         in-progress (ephemeral per-sandbox ConfigMap)
CREATE TABLE IF NOT EXISTS sandbox_compositions (
    sandbox_id      TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    soul_ref        TEXT,
    skill_refs      TEXT[] NOT NULL DEFAULT '{}',
    skill_config    JSONB NOT NULL DEFAULT '{}'::jsonb,
    track_upstream  BOOLEAN NOT NULL DEFAULT FALSE,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Quota table for ephemeral test sandboxes the playground spawns. A
-- background reaper deletes pods past expires_at; the per-user count
-- enforces the 3-concurrent quota at POST /test-sandbox time.
CREATE TABLE IF NOT EXISTS playground_test_sandboxes (
    sandbox_id      TEXT PRIMARY KEY REFERENCES sandboxes(id) ON DELETE CASCADE,
    author_user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_playground_test_expires ON playground_test_sandboxes(expires_at);
CREATE INDEX IF NOT EXISTS idx_playground_test_author ON playground_test_sandboxes(author_user_id);
