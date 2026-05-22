-- ast_* codex remote tokens stored prior to the secrets module migration
-- (PR #175) used base36 + bcrypt. The new algorithm (base62 + CRC32 +
-- HMAC-SHA256) cannot validate those hashes. Purging here means existing
-- tokens fail cleanly on next use; users re-mint via CodexTokensPanel.
--
-- codex_browser_sessions rows are cascade-deleted via the FK defined in
-- migration 026 (codex_browser_sessions.token_id REFERENCES codex_remote_tokens).
DELETE FROM codex_remote_tokens;
