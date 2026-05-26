-- Multi-channel routing (N:M) — fundação para B2B multi-tenant.
-- Permite que múltiplos workspace_im_channels apontem para o mesmo sandbox
-- (modo 'shared') ou que cada channel tenha seu próprio sandbox (modo
-- 'per_agent'). A FK legada sandboxes.im_channel_id é mantida para
-- compatibilidade reversa e dual-write durante o rollout.

-- 1. Routing strategy por workspace
ALTER TABLE workspaces
    ADD COLUMN IF NOT EXISTS channel_routing_strategy TEXT NOT NULL DEFAULT 'shared';
-- Valores válidos: 'shared' | 'per_agent' | 'hybrid'
-- Validação fica na app-layer; DB permanece permissivo para forward-compat.

-- 2. Tabela junction N:M
CREATE TABLE IF NOT EXISTS sandbox_channel_bindings (
    sandbox_id TEXT NOT NULL REFERENCES sandboxes(id)             ON DELETE CASCADE,
    channel_id TEXT NOT NULL REFERENCES workspace_im_channels(id) ON DELETE CASCADE,
    bound_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (sandbox_id, channel_id)
);
CREATE INDEX IF NOT EXISTS idx_sandbox_channel_bindings_channel ON sandbox_channel_bindings(channel_id);
CREATE INDEX IF NOT EXISTS idx_sandbox_channel_bindings_sandbox ON sandbox_channel_bindings(sandbox_id);

-- 3. Backfill: copia bindings existentes (FK legada) para a junction.
-- Idempotente via ON CONFLICT — re-aplicar o migrate é seguro.
INSERT INTO sandbox_channel_bindings (sandbox_id, channel_id, bound_at)
SELECT id, im_channel_id, COALESCE(updated_at, NOW())
FROM sandboxes
WHERE im_channel_id IS NOT NULL
ON CONFLICT DO NOTHING;
