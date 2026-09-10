-- Keep immutable cost evidence after monitor/history/usage cleanup. No balance
-- or quota writes: the gateway has already billed this request.
ALTER TABLE channel_monitor_histories ADD COLUMN IF NOT EXISTS billing_request_id TEXT;
ALTER TABLE channel_monitor_histories ADD COLUMN IF NOT EXISTS billing_api_key_id BIGINT;
CREATE TABLE IF NOT EXISTS channel_monitor_cost_records (
    history_id BIGINT PRIMARY KEY,
    checked_at TIMESTAMPTZ NOT NULL,
    request_id TEXT,
    api_key_id BIGINT,
    usage_log_id BIGINT UNIQUE,
    account_id BIGINT,
    account_base_cost_usd NUMERIC,
    account_rate_multiplier NUMERIC,
    account_cost_usd NUMERIC,
    reconciled_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS channel_monitor_cost_pending
    ON channel_monitor_cost_records (checked_at) WHERE reconciled_at IS NULL;
CREATE INDEX IF NOT EXISTS channel_monitor_cost_date ON channel_monitor_cost_records (checked_at);

CREATE OR REPLACE FUNCTION capture_channel_monitor_cost_record() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    INSERT INTO channel_monitor_cost_records(history_id, checked_at, request_id, api_key_id)
    VALUES(NEW.id, NEW.checked_at, NEW.billing_request_id, NEW.billing_api_key_id)
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS channel_monitor_cost_insert ON channel_monitor_histories;
CREATE TRIGGER channel_monitor_cost_insert AFTER INSERT ON channel_monitor_histories
FOR EACH ROW EXECUTE FUNCTION capture_channel_monitor_cost_record();
