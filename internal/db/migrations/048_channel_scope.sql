-- Per-channel scope description for IM content guardrails (WhatsApp opt-in).
ALTER TABLE workspace_im_channels
    ADD COLUMN IF NOT EXISTS scope_description text;
