package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// GetChannelMonitorV3GroupQualityWindows returns the most recent valid
// observed request rows with no age floor. Unlike scheduler/account quality,
// monitor V3 intentionally includes probes, monitor traffic and zero-charge
// requests when they carry observable latency. Client-attributed failures
// are excluded using the existing Ops ownership classification.
func (r *usageLogRepository) GetChannelMonitorV3GroupQualityWindows(ctx context.Context, groupIDs []int64) (map[int64]service.ChannelMonitorV3GroupQualityWindows, error) {
	result := make(map[int64]service.ChannelMonitorV3GroupQualityWindows, len(groupIDs))
	if len(groupIDs) == 0 {
		return result, nil
	}
	query := `
		WITH requested AS (
			SELECT DISTINCT group_id
			FROM unnest($1::bigint[]) AS requested_groups(group_id)
		), observed AS MATERIALIZED (
			SELECT
				ul.group_id,
				ul.id,
				ul.created_at,
				ul.duration_ms,
				ul.first_token_ms,
				CASE
					WHEN ul.output_tokens >= 32
						AND ul.first_token_ms >= 0
						AND ul.duration_ms - ul.first_token_ms >= 1000
					THEN ul.output_tokens * 1000.0 / NULLIF(ul.duration_ms - ul.first_token_ms, 0)
				END AS generation_tokens_per_second
			FROM requested wanted
			CROSS JOIN LATERAL (
				SELECT *
				FROM usage_logs ul
				WHERE ul.group_id = wanted.group_id
				AND (
					ul.first_token_ms >= 0
					OR ul.duration_ms >= 0
				)
				AND NOT EXISTS (
					SELECT 1
					FROM ops_error_logs oe
					WHERE oe.group_id = ul.group_id
						AND NULLIF(oe.request_id, '') IS NOT NULL
						AND oe.request_id = NULLIF(ul.request_id, '')
						AND (
							oe.error_owner = 'client'
							OR oe.error_source = 'client_request'
							OR (COALESCE(oe.error_owner, '') = '' AND oe.status_code IN (400, 401, 403, 404, 413, 422, 499))
							OR oe.error_type = 'cyber_policy'
						)
				)
				ORDER BY ul.created_at DESC, ul.id DESC
				LIMIT 100
			) ul
		), ranked AS (
			SELECT *, ROW_NUMBER() OVER (PARTITION BY group_id ORDER BY created_at DESC, id DESC) AS request_rank
			FROM observed
			WHERE duration_ms IS NOT NULL OR first_token_ms IS NOT NULL
		)
		SELECT group_id,
			COUNT(*) FILTER (WHERE request_rank <= 10),
			COUNT(first_token_ms) FILTER (WHERE request_rank <= 10 AND first_token_ms >= 0),
			AVG(first_token_ms) FILTER (WHERE request_rank <= 10 AND first_token_ms >= 0),
			AVG(duration_ms) FILTER (WHERE request_rank <= 10 AND duration_ms >= 0),
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 10 AND first_token_ms >= 0),
			PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 10 AND first_token_ms >= 0),
			COUNT(generation_tokens_per_second) FILTER (WHERE request_rank <= 10),
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 10 AND generation_tokens_per_second IS NOT NULL),
			PERCENTILE_CONT(0.1) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 10 AND generation_tokens_per_second IS NOT NULL),
			COUNT(*) FILTER (WHERE request_rank <= 100),
			COUNT(first_token_ms) FILTER (WHERE request_rank <= 100 AND first_token_ms >= 0),
			AVG(first_token_ms) FILTER (WHERE request_rank <= 100 AND first_token_ms >= 0),
			AVG(duration_ms) FILTER (WHERE request_rank <= 100 AND duration_ms >= 0),
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 100 AND first_token_ms >= 0),
			PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 100 AND first_token_ms >= 0),
			COUNT(generation_tokens_per_second) FILTER (WHERE request_rank <= 100),
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 100 AND generation_tokens_per_second IS NOT NULL),
			PERCENTILE_CONT(0.1) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 100 AND generation_tokens_per_second IS NOT NULL)
		FROM ranked
		WHERE request_rank <= 100
		GROUP BY group_id
	`
	rows, err := r.sql.QueryContext(ctx, query, pq.Array(groupIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var groupID int64
		var last10, last100 accountQualityWindowScan
		destinations := []any{&groupID}
		destinations = append(destinations, last10.destinations()...)
		destinations = append(destinations, last100.destinations()...)
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		result[groupID] = service.ChannelMonitorV3GroupQualityWindows{Last10: last10.window(), Last100: last100.window()}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
