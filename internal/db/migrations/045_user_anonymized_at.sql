-- LGPD/GDPR user anonymization (soft delete). PII is scrubbed; FK references (created_by, audit) remain.
ALTER TABLE users ADD COLUMN IF NOT EXISTS anonymized_at TIMESTAMPTZ;
ALTER TABLE users ADD COLUMN IF NOT EXISTS original_email_hash TEXT;

CREATE INDEX IF NOT EXISTS idx_users_anonymized_at ON users(anonymized_at) WHERE anonymized_at IS NOT NULL;
