-- Support the high-frequency recovery reconciliation lookup without scanning
-- every usage row for each physical account/model state.
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_logs_recovery_probe_real_usage
ON usage_logs (
    account_id,
    (LOWER(BTRIM(COALESCE(NULLIF(BTRIM(requested_model), ''), model)))),
    created_at DESC
)
WHERE actual_cost > 0
  AND request_type <> 6
  AND LOWER(COALESCE(user_agent, '')) NOT LIKE '%sub2api-channel-monitor/%';
