-- Per-protocol recovery eligibility. Adaptive probes used to store one status for
-- Chat, Anthropic and Responses together, so an Anthropic failure marked the
-- account failed for Responses as well. Existing rows keep an empty map and are
-- not treated as qualified for a specific adaptive protocol until a business
-- probe records that protocol.

ALTER TABLE group_recovery_probe_states
    ADD COLUMN IF NOT EXISTS protocol_eligibility JSONB NOT NULL DEFAULT '{}'::jsonb;

ALTER TABLE group_recovery_probe_physical_states
    ADD COLUMN IF NOT EXISTS protocol_eligibility JSONB NOT NULL DEFAULT '{}'::jsonb;

COMMENT ON COLUMN group_recovery_probe_states.protocol_eligibility IS
    'Business-protocol eligibility. Keys are chat_completions, anthropic, and responses.';
COMMENT ON COLUMN group_recovery_probe_physical_states.protocol_eligibility IS
    'Business-protocol eligibility shared by the account/model physical probe state.';
