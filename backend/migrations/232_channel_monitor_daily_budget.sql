-- Isolated cost ledger for active channel-monitor probes. This column never
-- participates in user billing, account quota, or API-key quota.
ALTER TABLE channel_monitor_histories
    ADD COLUMN IF NOT EXISTS estimated_cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0;

ALTER TABLE channel_monitor_histories
    DROP CONSTRAINT IF EXISTS channel_monitor_histories_estimated_cost_usd_non_negative;

ALTER TABLE channel_monitor_histories
    ADD CONSTRAINT channel_monitor_histories_estimated_cost_usd_non_negative
    CHECK (estimated_cost_usd >= 0);

COMMENT ON COLUMN channel_monitor_histories.estimated_cost_usd IS
    'Standard-price estimated USD cost of this active probe; quota-only checks are zero; isolated from user billing';
