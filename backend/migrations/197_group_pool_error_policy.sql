-- Group-level pool mode and error policy overrides.
-- NULL intentionally means inherit from the selected account, preserving
-- existing per-account credentials and allowing different groups to share one account.
ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS pool_mode_enabled BOOLEAN,
    ADD COLUMN IF NOT EXISTS pool_mode_retry_count INTEGER,
    ADD COLUMN IF NOT EXISTS pool_mode_retry_status_codes JSONB,
    ADD COLUMN IF NOT EXISTS custom_error_codes_enabled BOOLEAN,
    ADD COLUMN IF NOT EXISTS custom_error_codes JSONB;

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'groups_pool_mode_retry_count_check') THEN
        ALTER TABLE groups ADD CONSTRAINT groups_pool_mode_retry_count_check
            CHECK (pool_mode_retry_count IS NULL OR pool_mode_retry_count BETWEEN 0 AND 10);
    END IF;
END $$;

COMMENT ON COLUMN groups.pool_mode_enabled IS 'NULL leaves account value/default unchanged; non-NULL is a group fallback when the account omits pool_mode';
COMMENT ON COLUMN groups.pool_mode_retry_count IS 'NULL leaves account value/default unchanged; non-NULL is a group fallback, bounded to 0..10';
COMMENT ON COLUMN groups.pool_mode_retry_status_codes IS 'NULL leaves account value/default unchanged; [] is an explicit group fallback that disables status-code retries';
COMMENT ON COLUMN groups.custom_error_codes_enabled IS 'NULL leaves account value/default unchanged; non-NULL is a group fallback for the account custom error-code switch';
COMMENT ON COLUMN groups.custom_error_codes IS 'NULL leaves account value/default unchanged; [] is an explicit group fallback that clears custom error codes';

CREATE INDEX IF NOT EXISTS idx_groups_pool_error_policy
    ON groups (pool_mode_enabled, custom_error_codes_enabled)
    WHERE deleted_at IS NULL;
