-- IM conversation history. Stores inbound messages from end-users and outbound
-- replies from agents. No LGPD retention policy yet — add TTL/purge later.
CREATE TABLE IF NOT EXISTS im_messages (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    channel_id  TEXT NOT NULL,
    from_user_id TEXT NOT NULL,
    direction   TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    text        TEXT NOT NULL,
    session_id  TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS im_messages_channel_user
    ON im_messages (channel_id, from_user_id, created_at DESC);
