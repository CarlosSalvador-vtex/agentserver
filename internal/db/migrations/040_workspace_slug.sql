-- 040_workspace_slug.sql — workspace subdomain auth (Opção A)
--
-- Adds workspaces.slug (URL-safe kebab-case identifier) used by
-- subdomain-based login: <slug>.<baseDomain> routes to the workspace.

ALTER TABLE workspaces ADD COLUMN IF NOT EXISTS slug TEXT;

WITH base AS (
    SELECT id,
           regexp_replace(lower(name), '[^a-z0-9]+', '-', 'g') AS raw_slug
    FROM workspaces
    WHERE slug IS NULL
),
trimmed AS (
    SELECT id, trim(both '-' from raw_slug) AS s FROM base
),
numbered AS (
    SELECT id,
           CASE WHEN s = '' THEN 'workspace' ELSE s END AS s,
           row_number() OVER (
               PARTITION BY CASE WHEN s = '' THEN 'workspace' ELSE s END
               ORDER BY id
           ) AS rn
    FROM trimmed
)
UPDATE workspaces w
SET slug = CASE WHEN n.rn = 1 THEN n.s ELSE n.s || '-' || n.rn END
FROM numbered n
WHERE w.id = n.id;

ALTER TABLE workspaces ALTER COLUMN slug SET NOT NULL;
CREATE UNIQUE INDEX IF NOT EXISTS uniq_workspaces_slug ON workspaces(slug);
