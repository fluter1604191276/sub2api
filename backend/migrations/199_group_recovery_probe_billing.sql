-- Persist recovery-probe settlement metadata and allow dedicated probe usage rows.

ALTER TABLE usage_logs
    DROP CONSTRAINT IF EXISTS usage_logs_request_type_check;

ALTER TABLE usage_logs
    ADD CONSTRAINT usage_logs_request_type_check
    CHECK (request_type >= 0 AND request_type <= 6);

ALTER TABLE group_recovery_probe_audits
    ADD COLUMN IF NOT EXISTS input_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS output_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_creation_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_read_tokens INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS settlement_status VARCHAR(24) NOT NULL DEFAULT 'pending',
    ADD COLUMN IF NOT EXISTS settled_cost NUMERIC(20, 10),
    ADD COLUMN IF NOT EXISTS usage_log_id BIGINT REFERENCES usage_logs(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS billing_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS billing_api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS settlement_error TEXT NOT NULL DEFAULT '';

ALTER TABLE group_recovery_probe_audits
    DROP CONSTRAINT IF EXISTS group_recovery_probe_audits_settlement_status_check;

ALTER TABLE group_recovery_probe_audits
    ADD CONSTRAINT group_recovery_probe_audits_settlement_status_check
    CHECK (settlement_status IN ('pending', 'settled', 'unavailable', 'budget_blocked', 'failed'));

CREATE INDEX IF NOT EXISTS idx_group_recovery_probe_audits_settlement
    ON group_recovery_probe_audits (settlement_status, created_at DESC);
