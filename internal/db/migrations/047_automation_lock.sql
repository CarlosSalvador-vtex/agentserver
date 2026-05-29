-- Multi-replica scheduler safety: lease column for SKIP LOCKED claims.
ALTER TABLE automations ADD COLUMN IF NOT EXISTS locked_until TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_automations_locked_until
    ON automations (locked_until)
    WHERE locked_until IS NOT NULL;
