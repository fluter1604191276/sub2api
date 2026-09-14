ALTER TABLE channel_account_stats_model_pricing
    ADD COLUMN IF NOT EXISTS time_pricing JSONB;

COMMENT ON COLUMN channel_account_stats_model_pricing.time_pricing IS
    'Account cost schedule only; evaluated at request pricing time, independent of user billing';
