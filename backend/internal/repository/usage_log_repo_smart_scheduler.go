package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// GetSmartSchedulerErrorStatsBatch returns classified, distinct streaming
// failures for the last 24 hours. Classification is deliberately based on
// structured ops columns; error text is not used as a billing or routing rule.
func (r *usageLogRepository) GetSmartSchedulerErrorStatsBatch(ctx context.Context, accountIDs []int64, startTime, endTime time.Time) (map[int64]service.SmartSchedulerErrorStats, error) {
	result := make(map[int64]service.SmartSchedulerErrorStats, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	query := `
		WITH successful AS (
			SELECT
				ul.account_id,
				COUNT(DISTINCT COALESCE(NULLIF(ul.request_id, ''), ul.id::text)) AS successful_request_count
			FROM usage_logs ul
			WHERE ul.account_id = ANY($1)
				AND ul.created_at >= $2
				AND ul.created_at < $3
				AND ul.actual_cost > 0
				AND ul.stream = TRUE
			GROUP BY ul.account_id
		), classified AS (
			SELECT
				oe.account_id,
				COALESCE(NULLIF(oe.request_id, ''), oe.id::text) AS request_key,
				oe.created_at >= ($3 - INTERVAL '1 hour') AS is_recent,
				CASE
					WHEN oe.is_business_limited = TRUE OR LOWER(COALESCE(oe.error_owner, '')) = 'client' THEN 'client_excluded'
					WHEN COALESCE(oe.upstream_status_code, oe.status_code) IN (429, 529) THEN 'rate_limit'
					WHEN LOWER(COALESCE(oe.error_owner, '')) = 'provider' OR LOWER(COALESCE(oe.error_phase, '')) IN ('upstream', 'account_auth') THEN
						CASE WHEN COALESCE(oe.upstream_status_code, oe.status_code) >= 500 OR COALESCE(oe.upstream_status_code, oe.status_code) = 0 THEN 'provider_transient' ELSE 'provider_failure' END
					WHEN LOWER(COALESCE(oe.error_owner, '')) = 'platform' OR LOWER(COALESCE(oe.error_phase, '')) IN ('routing', 'internal') THEN 'platform_failure'
					ELSE 'uncertain'
				END AS category
			FROM ops_error_logs oe
			WHERE oe.account_id = ANY($1)
				AND oe.created_at >= $2
				AND oe.created_at < $3
				AND oe.stream = TRUE
		), deduplicated AS (
			SELECT DISTINCT account_id, request_key, category, is_recent FROM classified
		), failures AS (
			SELECT
				account_id,
				COUNT(*) FILTER (WHERE category = 'provider_failure') AS provider_failure_count,
				COUNT(*) FILTER (WHERE category = 'provider_transient') AS provider_transient_count,
				COUNT(*) FILTER (WHERE category = 'rate_limit') AS rate_limit_count,
				COUNT(*) FILTER (WHERE category = 'client_excluded') AS client_excluded_count,
				COUNT(*) FILTER (WHERE category = 'platform_failure') AS platform_failure_count,
				COUNT(*) FILTER (WHERE category = 'uncertain') AS uncertain_count,
				COUNT(*) FILTER (WHERE category = 'provider_failure' AND is_recent) AS recent_provider_failure_count,
				COUNT(*) FILTER (WHERE category = 'provider_transient' AND is_recent) AS recent_provider_transient_count,
				COUNT(*) FILTER (WHERE category = 'rate_limit' AND is_recent) AS recent_rate_limit_count
			FROM deduplicated
			GROUP BY account_id
		)
		SELECT
			COALESCE(s.account_id, f.account_id),
			COALESCE(s.successful_request_count, 0),
			COALESCE(f.provider_failure_count, 0),
			COALESCE(f.provider_transient_count, 0),
			COALESCE(f.rate_limit_count, 0),
			COALESCE(f.client_excluded_count, 0),
			COALESCE(f.platform_failure_count, 0),
			COALESCE(f.uncertain_count, 0),
			COALESCE(f.recent_provider_failure_count, 0),
			COALESCE(f.recent_provider_transient_count, 0),
			COALESCE(f.recent_rate_limit_count, 0)
		FROM successful s
		FULL OUTER JOIN failures f ON f.account_id = s.account_id
	`
	rows, err := r.sql.QueryContext(ctx, query, pq.Array(accountIDs), startTime, endTime)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var accountID int64
		var stats service.SmartSchedulerErrorStats
		var counts [10]sql.NullInt64
		values := make([]any, 0, len(counts)+1)
		values = append(values, &accountID)
		for i := range counts {
			values = append(values, &counts[i])
		}
		if err := rows.Scan(values...); err != nil {
			return nil, err
		}
		stats.SuccessfulRequestCount = counts[0].Int64
		stats.ProviderFailureCount = counts[1].Int64
		stats.ProviderTransientFailureCount = counts[2].Int64
		stats.RateLimitCount = counts[3].Int64
		stats.ClientExcludedCount = counts[4].Int64
		stats.PlatformFailureCount = counts[5].Int64
		stats.UncertainFailureCount = counts[6].Int64
		stats.RecentProviderFailureCount = counts[7].Int64
		stats.RecentProviderTransientCount = counts[8].Int64
		stats.RecentRateLimitCount = counts[9].Int64
		result[accountID] = stats
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
