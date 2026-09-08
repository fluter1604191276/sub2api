-- Align synthetic channel probes with the API modes and models used by real traffic.
-- Apply immediately before recreating the sub2api container so the new runner loads
-- these values at startup. This script does not touch API keys, groups, accounts, or pricing.

BEGIN;

DO $$
BEGIN
    IF (
        SELECT count(*)
        FROM channel_monitors
        WHERE (id, name) IN (
            (4,  'codex 高性价比(倍率0.1)'),
            (5,  'codex 低价pro(倍率0.15)'),
            (9,  'claude 低价低缓存(倍率0.15)'),
            (10, 'codex 低价渠道(倍率0.08)'),
            (12, 'claude 低价高缓存(倍率0.3)'),
            (13, 'claude 高性价比(倍率0.45)'),
            (17, 'claude 低价中缓存(倍率0.2)')
        )
    ) <> 7 THEN
        RAISE EXCEPTION 'channel monitor precondition failed; aborting without changes';
    END IF;
END $$;

-- The low-price and low-price Pro groups primarily serve gpt-5.6-sol over Responses.
-- Keep the next two most relevant models visible as independent secondary probes.
UPDATE channel_monitors
SET api_mode = 'responses',
    primary_model = 'gpt-5.6-sol',
    extra_models = '["gpt-5.5","gpt-5.6-terra"]'::jsonb,
    jitter_seconds = 150,
    updated_at = now()
WHERE id IN (5, 10);

-- High-value traffic is currently led by gpt-5.5; sol and terra remain visible
-- separately so one weak model cannot misrepresent the whole group.
UPDATE channel_monitors
SET api_mode = 'responses',
    primary_model = 'gpt-5.5',
    extra_models = '["gpt-5.6-sol","gpt-5.6-terra"]'::jsonb,
    jitter_seconds = 150,
    updated_at = now()
WHERE id = 4;

-- Claude continues to use the native Messages probe. Randomize its schedule so
-- all synthetic probes no longer hit the gateway in the same few seconds.
UPDATE channel_monitors
SET jitter_seconds = 120,
    updated_at = now()
WHERE id IN (9, 12, 13, 17);

COMMIT;

SELECT id, name, provider, api_mode, primary_model, extra_models,
       interval_seconds, jitter_seconds
FROM channel_monitors
WHERE id IN (4, 5, 9, 10, 12, 13, 17)
ORDER BY id;
