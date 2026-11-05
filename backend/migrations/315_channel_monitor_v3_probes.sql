CREATE TABLE IF NOT EXISTS channel_monitor_v3_probe_dispatches (
    id BIGSERIAL PRIMARY KEY,
    dispatch_id UUID NOT NULL UNIQUE,
    group_id BIGINT NOT NULL,
    model VARCHAR(256) NOT NULL,
    platform VARCHAR(64) NOT NULL,
    budget_day DATE NOT NULL,
    reserved_usd NUMERIC(20, 10) NOT NULL CHECK (reserved_usd >= 0),
    client_request_id VARCHAR(128) NOT NULL UNIQUE,
    status VARCHAR(24) NOT NULL CHECK (status IN ('claimed', 'sent', 'completed', 'settled', 'uncertain', 'undispatched')),
    claimed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    sent_at TIMESTAMPTZ,
    settled_at TIMESTAMPTZ,
    actual_cost_usd NUMERIC(20, 10),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_probe_dispatches_budget
    ON channel_monitor_v3_probe_dispatches (budget_day, status, created_at);
CREATE INDEX IF NOT EXISTS idx_channel_monitor_v3_probe_dispatches_group
    ON channel_monitor_v3_probe_dispatches (group_id, created_at DESC);
