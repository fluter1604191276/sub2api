-- Durable daily operational budget ledger for active channel-monitor probes.
-- It is intentionally independent from deletable monitor history and all user
-- billing/quota tables. budget_date is the application's local calendar date.
CREATE TABLE IF NOT EXISTS channel_monitor_daily_budget_ledger (
    budget_date DATE PRIMARY KEY,
    estimated_cost_usd DOUBLE PRECISION NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT channel_monitor_daily_budget_ledger_cost_non_negative
        CHECK (estimated_cost_usd >= 0)
);

COMMENT ON TABLE channel_monitor_daily_budget_ledger IS
    'Conservative standard-price operational estimates and in-flight reservations for channel monitoring; never user billing';
