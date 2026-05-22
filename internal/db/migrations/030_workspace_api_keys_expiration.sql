-- Add expiration to workspace_api_keys.
-- Backfill any pre-existing rows with NOW() + 90 days so they don't lose
-- access on deploy. New rows always set this explicitly at mint time.
ALTER TABLE workspace_api_keys
    ADD COLUMN expires_at TIMESTAMPTZ;

UPDATE workspace_api_keys
   SET expires_at = NOW() + INTERVAL '90 days'
 WHERE expires_at IS NULL;

ALTER TABLE workspace_api_keys
    ALTER COLUMN expires_at SET NOT NULL;
