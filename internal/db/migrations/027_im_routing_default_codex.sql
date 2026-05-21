-- Switch new workspace_im_channels to default routing_mode='codex'
-- instead of 'nanoclaw'. The nanoclaw sandbox path still works for
-- existing rows but is no longer the recommended path — every new
-- channel should land on the codex-app-gateway flow.
--
-- Existing rows are intentionally NOT updated: a workspace already
-- bound to nanoclaw shouldn't have its routing silently flipped at
-- migration time. Operators can toggle per-channel via the workspace
-- detail UI or PATCH /api/workspaces/{id}/im/channels/{channelId}.
ALTER TABLE workspace_im_channels
    ALTER COLUMN routing_mode SET DEFAULT 'codex';
