-- Financial estimates are separate from admission reservations. Historical
-- usage cannot prove supplier debits; never backfill reservations as spending.
CREATE TABLE IF NOT EXISTS channel_monitor_daily_bills (
    bill_date DATE PRIMARY KEY,
    base_cost_usd NUMERIC NOT NULL DEFAULT 0 CHECK (base_cost_usd >= 0),
    checks BIGINT NOT NULL DEFAULT 0,
    unknown_cost_checks BIGINT NOT NULL DEFAULT 0,
    failed_checks BIGINT NOT NULL DEFAULT 0,
    historical_partial BOOLEAN NOT NULL DEFAULT FALSE,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Serialize the backfill and trigger installation with history inserts.
LOCK TABLE channel_monitor_histories IN SHARE ROW EXCLUSIVE MODE;
INSERT INTO channel_monitor_daily_bills
    (bill_date, base_cost_usd, checks, unknown_cost_checks, failed_checks, historical_partial)
SELECT (checked_at AT TIME ZONE 'Asia/Shanghai')::date,
       SUM(CASE WHEN estimated_cost_usd > 0 AND estimated_cost_usd < 'Infinity'::float8
           THEN estimated_cost_usd::numeric ELSE 0 END), COUNT(*),
       COUNT(*) FILTER (WHERE NOT (estimated_cost_usd > 0 AND estimated_cost_usd < 'Infinity'::float8)),
       COUNT(*) FILTER (WHERE status IN ('failed', 'error')), TRUE
FROM channel_monitor_histories
GROUP BY 1 ON CONFLICT (bill_date) DO NOTHING;

CREATE OR REPLACE FUNCTION record_channel_monitor_daily_bill() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    cost NUMERIC := 0;
    unknown_count BIGINT := 1;
BEGIN
    IF NEW.estimated_cost_usd > 0 AND NEW.estimated_cost_usd < 'Infinity'::float8 THEN
        cost := NEW.estimated_cost_usd::numeric;
        unknown_count := 0;
    ELSIF EXISTS (SELECT 1 FROM channel_monitors WHERE id = NEW.monitor_id AND check_mode = 'quota') THEN
        unknown_count := 0;
    END IF;
    INSERT INTO channel_monitor_daily_bills AS b
        (bill_date, base_cost_usd, checks, unknown_cost_checks, failed_checks)
    VALUES ((NEW.checked_at AT TIME ZONE 'Asia/Shanghai')::date, cost, 1, unknown_count,
        CASE WHEN NEW.status IN ('failed', 'error') THEN 1 ELSE 0 END)
    ON CONFLICT (bill_date) DO UPDATE SET
        base_cost_usd = b.base_cost_usd + EXCLUDED.base_cost_usd,
        checks = b.checks + 1,
        unknown_cost_checks = b.unknown_cost_checks + EXCLUDED.unknown_cost_checks,
        failed_checks = b.failed_checks + EXCLUDED.failed_checks,
        updated_at = NOW();
    RETURN NEW;
END $$;

DROP TRIGGER IF EXISTS channel_monitor_daily_bill_insert ON channel_monitor_histories;
CREATE TRIGGER channel_monitor_daily_bill_insert
AFTER INSERT ON channel_monitor_histories
FOR EACH ROW EXECUTE FUNCTION record_channel_monitor_daily_bill();
