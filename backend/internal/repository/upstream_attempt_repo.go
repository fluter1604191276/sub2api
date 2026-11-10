package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type upstreamAttemptRepository struct{ db *sql.DB }

func NewUpstreamAttemptRepository(db *sql.DB) service.UpstreamAttemptRepository {
	return &upstreamAttemptRepository{db: db}
}

func (r *upstreamAttemptRepository) Insert(ctx context.Context, attempt *service.UpstreamAttempt) error {
	if r == nil || r.db == nil {
		return fmt.Errorf("nil upstream attempt repository")
	}
	if attempt == nil {
		return fmt.Errorf("nil upstream attempt")
	}
	copy := *attempt
	if err := service.NormalizeUpstreamAttempt(&copy); err != nil {
		return err
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO upstream_attempts (attempt_id,request_id,client_request_id,upstream_request_id,created_at,user_id,group_id,account_id,model,protocol,source,service_tier,reasoning_effort,long_context_pricing_enabled,http_status,outcome,usage_present,input_tokens,cache_creation_tokens,cache_read_tokens,cache_creation_5m_tokens,cache_creation_1h_tokens,output_tokens,reasoning_tokens,account_rate_multiplier,cost_usd,cost_status) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,$24,$25,$26,$27) ON CONFLICT (attempt_id) DO NOTHING`, copy.AttemptID, copy.RequestID, copy.ClientRequestID, copy.UpstreamRequestID, copy.CreatedAt, copy.UserID, copy.GroupID, copy.AccountID, copy.Model, copy.Protocol, copy.Source, copy.ServiceTier, copy.ReasoningEffort, copy.LongContextPricingEnabled, copy.HTTPStatus, copy.Outcome, copy.UsagePresent, copy.InputTokens, copy.CacheCreationTokens, copy.CacheReadTokens, copy.CacheCreation5mTokens, copy.CacheCreation1hTokens, copy.OutputTokens, copy.ReasoningTokens, copy.AccountRateMultiplier, copy.CostUSD, copy.CostStatus)
	return err
}

func (r *upstreamAttemptRepository) List(ctx context.Context, filter *service.UpstreamAttemptFilter) (*service.UpstreamAttemptList, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil upstream attempt repository")
	}
	if filter == nil {
		filter = &service.UpstreamAttemptFilter{}
	}
	page, size := filter.Page, filter.PageSize
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 50
	}
	if size > 100 {
		size = 100
	}
	end := time.Now().UTC()
	if filter.EndTime != nil {
		end = filter.EndTime.UTC()
	}
	start := end.Add(-24 * time.Hour)
	if filter.StartTime != nil {
		start = filter.StartTime.UTC()
	}
	if start.After(end) || end.Sub(start) > 90*24*time.Hour {
		return nil, fmt.Errorf("invalid upstream attempt time range")
	}
	args := []any{start, end}
	conditions := []string{"created_at >= $1", "created_at < $2"}
	if filter.AccountID != nil {
		args = append(args, *filter.AccountID)
		conditions = append(conditions, fmt.Sprintf("account_id=$%d", len(args)))
	}
	if filter.Source != "" {
		if !service.ValidUpstreamAttemptSource(filter.Source) {
			return nil, fmt.Errorf("invalid upstream attempt source")
		}
		args = append(args, filter.Source)
		conditions = append(conditions, fmt.Sprintf("source=$%d", len(args)))
	}
	where := " WHERE " + strings.Join(conditions, " AND ")
	out := &service.UpstreamAttemptList{Items: []*service.UpstreamAttempt{}, Page: page, PageSize: size}
	if err := r.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM upstream_attempts"+where, args...).Scan(&out.Total); err != nil {
		return nil, err
	}
	query := `SELECT id,attempt_id,request_id,client_request_id,upstream_request_id,created_at,user_id,group_id,account_id,model,protocol,source,service_tier,reasoning_effort,long_context_pricing_enabled,http_status,outcome,usage_present,input_tokens,cache_creation_tokens,cache_read_tokens,cache_creation_5m_tokens,cache_creation_1h_tokens,output_tokens,reasoning_tokens,account_rate_multiplier,cost_usd,cost_status FROM upstream_attempts` + where + fmt.Sprintf(" ORDER BY created_at DESC,id DESC LIMIT $%d OFFSET $%d", len(args)+1, len(args)+2)
	args = append(args, size, (page-1)*size)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		a := &service.UpstreamAttempt{}
		if err := rows.Scan(&a.ID, &a.AttemptID, &a.RequestID, &a.ClientRequestID, &a.UpstreamRequestID, &a.CreatedAt, &a.UserID, &a.GroupID, &a.AccountID, &a.Model, &a.Protocol, &a.Source, &a.ServiceTier, &a.ReasoningEffort, &a.LongContextPricingEnabled, &a.HTTPStatus, &a.Outcome, &a.UsagePresent, &a.InputTokens, &a.CacheCreationTokens, &a.CacheReadTokens, &a.CacheCreation5mTokens, &a.CacheCreation1hTokens, &a.OutputTokens, &a.ReasoningTokens, &a.AccountRateMultiplier, &a.CostUSD, &a.CostStatus); err != nil {
			return nil, err
		}
		out.Items = append(out.Items, a)
	}
	return out, rows.Err()
}

func (r *upstreamAttemptRepository) DailyCosts(ctx context.Context, accountID *int64, start, end time.Time) ([]service.UpstreamAttemptDailyCost, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil upstream attempt repository")
	}
	if start.After(end) {
		return nil, fmt.Errorf("invalid upstream attempt cost range")
	}
	args := []any{start.UTC(), end.UTC()}
	where := " WHERE created_at >= $1 AND created_at < $2"
	if accountID != nil {
		args = append(args, *accountID)
		where += fmt.Sprintf(" AND account_id=$%d", len(args))
	}
	rows, err := r.db.QueryContext(ctx, `SELECT (created_at AT TIME ZONE 'Asia/Shanghai')::date,account_id,COUNT(*),COALESCE(SUM(cost_usd),0),COUNT(*) FILTER (WHERE cost_usd IS NULL) FROM upstream_attempts`+where+` GROUP BY 1,2 ORDER BY 1 DESC,2 ASC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.UpstreamAttemptDailyCost{}
	for rows.Next() {
		var day time.Time
		var item service.UpstreamAttemptDailyCost
		if err := rows.Scan(&day, &item.AccountID, &item.AttemptCount, &item.KnownCostUSD, &item.UnknownCount); err != nil {
			return nil, err
		}
		item.Day = day
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *upstreamAttemptRepository) SumByClientRequestID(ctx context.Context, source, clientRequestID string) (service.UpstreamAttemptCostSummary, error) {
	if r == nil || r.db == nil {
		return service.UpstreamAttemptCostSummary{}, fmt.Errorf("nil upstream attempt repository")
	}
	if !service.ValidUpstreamAttemptSource(source) || strings.TrimSpace(clientRequestID) == "" {
		return service.UpstreamAttemptCostSummary{}, fmt.Errorf("invalid upstream attempt correlation")
	}
	var out service.UpstreamAttemptCostSummary
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd),0),COUNT(*) FILTER (WHERE cost_usd IS NULL),COUNT(*) FROM upstream_attempts WHERE source=$1 AND client_request_id=$2`, source, clientRequestID).Scan(&out.KnownCostUSD, &out.UnknownCount, &out.AttemptCount)
	return out, err
}
