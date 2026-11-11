CREATE TABLE IF NOT EXISTS channel_monitor_v3_settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    daily_budget_usd NUMERIC(20,10) NOT NULL DEFAULT 2 CHECK (daily_budget_usd > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS channel_monitor_v3_groups (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    visible BOOLEAN NOT NULL DEFAULT FALSE,
    probe_interval_seconds INTEGER NOT NULL DEFAULT 600
        CHECK (probe_interval_seconds=0 OR probe_interval_seconds BETWEEN 60 AND 86400),
    test_model VARCHAR(256) NOT NULL DEFAULT '',
    last_probe_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
