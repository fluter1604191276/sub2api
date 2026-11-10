package repository

import (
	"context"
	"math"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUpstreamAttemptRepositoryInsertIsIdempotentByAttemptID(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO upstream_attempts")).
		WithArgs(
			"attempt-1", "server-1", "client-1", "upstream-1", sqlmock.AnyArg(), nil, nil, int64(9), "gpt-5", "responses", "request", "", "", true, 200, "success", true,
			int64(1), nil, nil, nil, nil, int64(2), nil, 1.5, 0.25, "known",
		).
		WillReturnResult(sqlmock.NewResult(0, 0))

	repo := NewUpstreamAttemptRepository(db)
	in, out, cost := int64(1), int64(2), 0.25
	require.NoError(t, repo.Insert(context.Background(), &service.UpstreamAttempt{
		AttemptID: "attempt-1", RequestID: "server-1", ClientRequestID: "client-1", UpstreamRequestID: "upstream-1",
		CreatedAt: time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC), AccountID: 9, Model: "gpt-5", Protocol: "responses", Source: "request", Outcome: "success", HTTPStatus: 200,
		UsagePresent: true, InputTokens: &in, OutputTokens: &out, LongContextPricingEnabled: true, AccountRateMultiplier: 1.5, CostUSD: &cost,
	}))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamAttemptRepositorySumByClientRequestIDIncludesUnknownAndCount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	mock.ExpectQuery(regexp.QuoteMeta("SELECT COALESCE(SUM(cost_usd),0),COUNT(*) FILTER (WHERE cost_usd IS NULL),COUNT(*) FROM upstream_attempts WHERE source=$1 AND client_request_id=$2")).
		WithArgs("channel_monitor_v3", "client-1").
		WillReturnRows(sqlmock.NewRows([]string{"cost", "unknown", "count"}).AddRow(0.125, 1, 2))

	summary, err := NewUpstreamAttemptRepository(db).SumByClientRequestID(context.Background(), "channel_monitor_v3", "client-1")
	require.NoError(t, err)
	require.Equal(t, 0.125, summary.KnownCostUSD)
	require.Equal(t, int64(1), summary.UnknownCount)
	require.Equal(t, int64(2), summary.AttemptCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamAttemptRepositoryDailyCostsUsesShanghaiDay(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	start := time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	mock.ExpectQuery("created_at AT TIME ZONE 'Asia/Shanghai'").
		WithArgs(start, end).
		WillReturnRows(sqlmock.NewRows([]string{"day", "account_id", "attempt_count", "known_cost", "unknown_count"}).AddRow(start, 9, 3, 0.5, 1))

	items, err := NewUpstreamAttemptRepository(db).DailyCosts(context.Background(), nil, start, end)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, int64(9), items[0].AccountID)
	require.Equal(t, int64(3), items[0].AttemptCount)
	require.Equal(t, 0.5, items[0].KnownCostUSD)
	require.Equal(t, int64(1), items[0].UnknownCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestUpstreamAttemptRepositoryHugePageIsClampedAndOffsetNeverNegative(t *testing.T) {
	for _, page := range []int{math.MaxInt, math.MaxInt64, service.AdminListMaxPage + 1, -5, 0} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		start := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
		end := start.Add(time.Hour)
		wantPage := page
		if wantPage < 1 {
			wantPage = 1
		}
		if wantPage > service.AdminListMaxPage {
			wantPage = service.AdminListMaxPage
		}
		wantOffset := int64(wantPage-1) * 100
		require.GreaterOrEqual(t, wantOffset, int64(0))
		mock.ExpectQuery(`SELECT COUNT\(\*\) FROM upstream_attempts`).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
		mock.ExpectQuery(`LIMIT \$3 OFFSET \$4`).WithArgs(start, end, 100, wantOffset).WillReturnRows(sqlmock.NewRows([]string{"id"}))
		out, err := NewUpstreamAttemptRepository(db).List(context.Background(), &service.UpstreamAttemptFilter{StartTime: &start, EndTime: &end, Page: page, PageSize: math.MaxInt})
		require.NoError(t, err, page)
		require.Equal(t, wantPage, out.Page)
		require.Equal(t, 100, out.PageSize)
		require.NoError(t, mock.ExpectationsWereMet())
		db.Close()
	}
}
