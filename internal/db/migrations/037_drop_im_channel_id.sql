-- 037_drop_im_channel_id.sql — Sprint 5 PR-3 (improvements.md #20)
--
-- Drops the legacy sandboxes.im_channel_id FK column. Sandbox ↔ channel
-- routing has been exclusively via sandbox_channel_bindings since the
-- junction migration. The dual-write in BindSandboxToChannel /
-- UnbindSandboxFromChannel and the FK-fallback reads in GetSandboxForChannel
-- / GetIMChannelForSandbox have been removed in the same commit.

ALTER TABLE sandboxes DROP COLUMN IF EXISTS im_channel_id;
