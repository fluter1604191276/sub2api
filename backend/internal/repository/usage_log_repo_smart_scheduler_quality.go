package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type smartSchedulerQualityWindowScan struct {
	sampleCount     int64
	firstTokenCount int64
	firstTokenP50   sql.NullFloat64
	firstTokenP90   sql.NullFloat64
	generationCount int64
	generationP50   sql.NullFloat64
	generationP10   sql.NullFloat64
}

func (w *smartSchedulerQualityWindowScan) destinations() []any {
	return []any{
		&w.sampleCount,
		&w.firstTokenCount,
		&w.firstTokenP50,
		&w.firstTokenP90,
		&w.generationCount,
		&w.generationP50,
		&w.generationP10,
	}
}

func (w smartSchedulerQualityWindowScan) window() service.AccountQualityWindow {
	return service.AccountQualityWindow{
		SampleCount:                  w.sampleCount,
		FirstTokenSampleCount:        w.firstTokenCount,
		P50FirstTokenMs:              nullableFloat64(w.firstTokenP50),
		P90FirstTokenMs:              nullableFloat64(w.firstTokenP90),
		GenerationSampleCount:        w.generationCount,
		P50GenerationTokensPerSecond: nullableFloat64(w.generationP50),
		P10GenerationTokensPerSecond: nullableFloat64(w.generationP10),
	}
}

// GetSmartSchedulerQualityStatsBatch returns robust, model/endpoint-scoped
// streaming evidence for the read-only routing preview.
func (r *usageLogRepository) GetSmartSchedulerQualityStatsBatch(
	ctx context.Context,
	accountIDs []int64,
	startTime, realtimeStartTime, endTime time.Time,
	requestedModel, endpoint string,
) (map[int64]service.AccountQualitySamples, error) {
	result := make(map[int64]service.AccountQualitySamples, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}

	query := `
		WITH successful AS MATERIALIZED (
			SELECT
				ul.account_id,
				ul.created_at,
				ul.duration_ms,
				ul.first_token_ms,
				ul.id,
				CASE
					WHEN ul.output_tokens > 0 AND ul.duration_ms > ul.first_token_ms
					THEN ul.output_tokens * 1000.0 / NULLIF(ul.duration_ms - ul.first_token_ms, 0)
				END AS generation_tokens_per_second
			FROM usage_logs ul
			WHERE ul.account_id = ANY($1)
				AND ul.created_at >= $2
				AND ul.created_at < $4
				AND ul.actual_cost > 0
				AND ul.request_type <> 6
				AND ul.stream = TRUE
				AND LOWER(COALESCE(ul.user_agent, '')) NOT LIKE '%sub2api-channel-monitor/%'
				AND ($5 = '' OR LOWER(COALESCE(NULLIF(ul.requested_model, ''), ul.model)) = LOWER($5))
				AND ($6 = 'any' OR LOWER(COALESCE(NULLIF(ul.inbound_endpoint, ''), '')) = LOWER($6))
		), ranked AS (
			SELECT
				*,
				ROW_NUMBER() OVER (
					PARTITION BY account_id
					ORDER BY created_at DESC, id DESC
				) AS request_rank
			FROM successful
			WHERE duration_ms IS NOT NULL
		), quality AS (
			SELECT
				account_id,
				COUNT(*) FILTER (WHERE created_at >= $3 AND request_rank <= 10) AS realtime_last_10_count,
				COUNT(first_token_ms) FILTER (WHERE created_at >= $3 AND request_rank <= 10) AS realtime_last_10_first_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE created_at >= $3 AND request_rank <= 10 AND first_token_ms IS NOT NULL) AS realtime_last_10_first_p50,
				PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE created_at >= $3 AND request_rank <= 10 AND first_token_ms IS NOT NULL) AS realtime_last_10_first_p90,
				COUNT(generation_tokens_per_second) FILTER (WHERE created_at >= $3 AND request_rank <= 10) AS realtime_last_10_generation_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE created_at >= $3 AND request_rank <= 10 AND generation_tokens_per_second IS NOT NULL) AS realtime_last_10_generation_p50,
				PERCENTILE_CONT(0.1) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE created_at >= $3 AND request_rank <= 10 AND generation_tokens_per_second IS NOT NULL) AS realtime_last_10_generation_p10,
				COUNT(*) FILTER (WHERE created_at >= $3 AND request_rank <= 100) AS realtime_last_100_count,
				COUNT(first_token_ms) FILTER (WHERE created_at >= $3 AND request_rank <= 100) AS realtime_last_100_first_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE created_at >= $3 AND request_rank <= 100 AND first_token_ms IS NOT NULL) AS realtime_last_100_first_p50,
				PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE created_at >= $3 AND request_rank <= 100 AND first_token_ms IS NOT NULL) AS realtime_last_100_first_p90,
				COUNT(generation_tokens_per_second) FILTER (WHERE created_at >= $3 AND request_rank <= 100) AS realtime_last_100_generation_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE created_at >= $3 AND request_rank <= 100 AND generation_tokens_per_second IS NOT NULL) AS realtime_last_100_generation_p50,
				PERCENTILE_CONT(0.1) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE created_at >= $3 AND request_rank <= 100 AND generation_tokens_per_second IS NOT NULL) AS realtime_last_100_generation_p10,
				COUNT(*) FILTER (WHERE request_rank <= 10) AS last_10_count,
				COUNT(first_token_ms) FILTER (WHERE request_rank <= 10) AS last_10_first_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 10 AND first_token_ms IS NOT NULL) AS last_10_first_p50,
				PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 10 AND first_token_ms IS NOT NULL) AS last_10_first_p90,
				COUNT(generation_tokens_per_second) FILTER (WHERE request_rank <= 10) AS last_10_generation_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 10 AND generation_tokens_per_second IS NOT NULL) AS last_10_generation_p50,
				PERCENTILE_CONT(0.1) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 10 AND generation_tokens_per_second IS NOT NULL) AS last_10_generation_p10,
				COUNT(*) FILTER (WHERE request_rank <= 100) AS last_100_count,
				COUNT(first_token_ms) FILTER (WHERE request_rank <= 100) AS last_100_first_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 100 AND first_token_ms IS NOT NULL) AS last_100_first_p50,
				PERCENTILE_CONT(0.9) WITHIN GROUP (ORDER BY first_token_ms) FILTER (WHERE request_rank <= 100 AND first_token_ms IS NOT NULL) AS last_100_first_p90,
				COUNT(generation_tokens_per_second) FILTER (WHERE request_rank <= 100) AS last_100_generation_count,
				PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 100 AND generation_tokens_per_second IS NOT NULL) AS last_100_generation_p50,
				PERCENTILE_CONT(0.1) WITHIN GROUP (ORDER BY generation_tokens_per_second) FILTER (WHERE request_rank <= 100 AND generation_tokens_per_second IS NOT NULL) AS last_100_generation_p10
			FROM ranked
			GROUP BY account_id
		), activity AS (
			SELECT
				account_id,
				COUNT(*) FILTER (WHERE created_at >= $3) AS successful_requests_1h,
				MAX(created_at) AS last_success_at
			FROM successful
			GROUP BY account_id
		)
		SELECT
			COALESCE(q.account_id, a.account_id),
			COALESCE(q.realtime_last_10_count, 0), COALESCE(q.realtime_last_10_first_count, 0), q.realtime_last_10_first_p50, q.realtime_last_10_first_p90, COALESCE(q.realtime_last_10_generation_count, 0), q.realtime_last_10_generation_p50, q.realtime_last_10_generation_p10,
			COALESCE(q.realtime_last_100_count, 0), COALESCE(q.realtime_last_100_first_count, 0), q.realtime_last_100_first_p50, q.realtime_last_100_first_p90, COALESCE(q.realtime_last_100_generation_count, 0), q.realtime_last_100_generation_p50, q.realtime_last_100_generation_p10,
			COALESCE(q.last_10_count, 0), COALESCE(q.last_10_first_count, 0), q.last_10_first_p50, q.last_10_first_p90, COALESCE(q.last_10_generation_count, 0), q.last_10_generation_p50, q.last_10_generation_p10,
			COALESCE(q.last_100_count, 0), COALESCE(q.last_100_first_count, 0), q.last_100_first_p50, q.last_100_first_p90, COALESCE(q.last_100_generation_count, 0), q.last_100_generation_p50, q.last_100_generation_p10,
			COALESCE(a.successful_requests_1h, 0), a.last_success_at
		FROM quality q
		FULL OUTER JOIN activity a ON a.account_id = q.account_id
	`
	rows, err := r.sql.QueryContext(ctx, query, pq.Array(accountIDs), startTime, realtimeStartTime, endTime, requestedModel, endpoint)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var accountID int64
		var recent10, recent100, stable10, stable100 smartSchedulerQualityWindowScan
		var successfulRequests1h int64
		var lastSuccessAt sql.NullTime
		destinations := []any{&accountID}
		destinations = append(destinations, recent10.destinations()...)
		destinations = append(destinations, recent100.destinations()...)
		destinations = append(destinations, stable10.destinations()...)
		destinations = append(destinations, stable100.destinations()...)
		destinations = append(destinations, &successfulRequests1h, &lastSuccessAt)
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		result[accountID] = service.AccountQualitySamples{
			Recent1h:             service.AccountQualityPeriodSamples{Last10: recent10.window(), Last100: recent100.window()},
			Last24h:              service.AccountQualityPeriodSamples{Last10: stable10.window(), Last100: stable100.window()},
			SuccessfulRequests1h: successfulRequests1h,
			LastSuccessAt:        nullableTime(lastSuccessAt),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
