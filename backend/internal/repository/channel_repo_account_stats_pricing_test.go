//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestBatchLoadAccountStatsModelPricing_LoadsImageOperation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &channelRepository{db: db}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	maxTokens := 4096
	inputPrice := 0.000001
	outputPrice := 0.000002
	cacheWritePrice := 0.0000005
	cacheReadPrice := 0.0000001
	imageOutputPrice := 0.03
	perRequestPrice := 0.04
	intervalPrice := 0.08

	mock.ExpectQuery("SELECT id, rule_id, platform, models, billing_mode, input_price, output_price,").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "rule_id", "platform", "models", "billing_mode", "input_price", "output_price",
			"cache_write_price", "cache_read_price", "image_output_price", "per_request_price",
			"image_operation", "created_at", "updated_at",
		}).AddRow(
			int64(10), int64(7), "openai", []byte(`["gpt-image-1"]`), service.BillingModeImage,
			inputPrice, outputPrice, cacheWritePrice, cacheReadPrice, imageOutputPrice, perRequestPrice,
			service.AccountStatsImageOperationResponses, now, now,
		))
	mock.ExpectQuery("SELECT id, pricing_id, min_tokens, max_tokens, tier_label,").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pricing_id", "min_tokens", "max_tokens", "tier_label",
			"input_price", "output_price", "cache_write_price", "cache_read_price",
			"per_request_price", "sort_order", "created_at", "updated_at",
		}).AddRow(
			int64(20), int64(10), 0, maxTokens, "1024x1024",
			nil, nil, nil, nil, intervalPrice, 1, now, now,
		))

	pricing, err := repo.batchLoadAccountStatsModelPricing(context.Background(), []int64{7})
	require.NoError(t, err)
	require.Len(t, pricing[7], 1)
	require.Equal(t, service.AccountStatsImageOperationResponses, pricing[7][0].ImageOperation)
	require.Len(t, pricing[7][0].Intervals, 1)
	require.Equal(t, "1024x1024", pricing[7][0].Intervals[0].TierLabel)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestBatchLoadAccountStatsModelPricing_NullImageOperationLoadsAsAny(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &channelRepository{db: db}
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	perRequestPrice := 0.04

	mock.ExpectQuery("SELECT id, rule_id, platform, models, billing_mode, input_price, output_price,").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "rule_id", "platform", "models", "billing_mode", "input_price", "output_price",
			"cache_write_price", "cache_read_price", "image_output_price", "per_request_price",
			"image_operation", "created_at", "updated_at",
		}).AddRow(
			int64(11), int64(8), "openai", []byte(`["gpt-image-1"]`), service.BillingModeImage,
			nil, nil, nil, nil, nil, perRequestPrice,
			service.AccountStatsImageOperationAny, now, now,
		))
	mock.ExpectQuery("SELECT id, pricing_id, min_tokens, max_tokens, tier_label,").
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "pricing_id", "min_tokens", "max_tokens", "tier_label",
			"input_price", "output_price", "cache_write_price", "cache_read_price",
			"per_request_price", "sort_order", "created_at", "updated_at",
		}))

	pricing, err := repo.batchLoadAccountStatsModelPricing(context.Background(), []int64{8})
	require.NoError(t, err)
	require.Len(t, pricing[8], 1)
	require.Equal(t, service.AccountStatsImageOperationAny, pricing[8][0].ImageOperation)
	require.Empty(t, pricing[8][0].Intervals)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAccountStatsModelPricingTx_PersistsImageOperation(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	perRequestPrice := 0.04
	intervalPrice := 0.08
	pricing := &service.ChannelModelPricing{
		Platform:        "openai",
		Models:          []string{"gpt-image-1"},
		BillingMode:     service.BillingModeImage,
		PerRequestPrice: &perRequestPrice,
		ImageOperation:  service.AccountStatsImageOperationResponses,
		Intervals: []service.PricingInterval{{
			MinTokens:       0,
			TierLabel:       "1024x1024",
			PerRequestPrice: &intervalPrice,
			SortOrder:       1,
		}},
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO channel_account_stats_model_pricing").
		WithArgs(
			int64(7), "openai", []byte(`["gpt-image-1"]`), service.BillingModeImage,
			nil, nil, nil, nil, nil, &perRequestPrice, "responses",
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(10), now, now))
	mock.ExpectQuery("INSERT INTO channel_account_stats_pricing_intervals").
		WithArgs(
			int64(10), 0, nil, "1024x1024",
			nil, nil, nil, nil, &intervalPrice, 1,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(20), now, now))
	mock.ExpectCommit()

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = createAccountStatsModelPricingTx(context.Background(), tx, 7, pricing)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.Equal(t, int64(10), pricing.ID)
	require.Equal(t, int64(10), pricing.Intervals[0].PricingID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateAccountStatsModelPricingTx_AnyImageOperationPersistsNull(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	perRequestPrice := 0.04
	intervalPrice := 0.08
	pricing := &service.ChannelModelPricing{
		Platform:        "openai",
		Models:          []string{"gpt-image-1"},
		BillingMode:     service.BillingModeImage,
		PerRequestPrice: &perRequestPrice,
		ImageOperation:  service.AccountStatsImageOperationAny,
		Intervals: []service.PricingInterval{{
			MinTokens:       0,
			TierLabel:       "1024x1024",
			PerRequestPrice: &intervalPrice,
			SortOrder:       1,
		}},
	}

	mock.ExpectBegin()
	mock.ExpectQuery("INSERT INTO channel_account_stats_model_pricing").
		WithArgs(
			int64(8), "openai", []byte(`["gpt-image-1"]`), service.BillingModeImage,
			nil, nil, nil, nil, nil, &perRequestPrice, nil,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(11), now, now))
	mock.ExpectQuery("INSERT INTO channel_account_stats_pricing_intervals").
		WithArgs(
			int64(11), 0, nil, "1024x1024",
			nil, nil, nil, nil, &intervalPrice, 1,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow(int64(21), now, now))
	mock.ExpectCommit()

	tx, err := db.BeginTx(context.Background(), nil)
	require.NoError(t, err)
	err = createAccountStatsModelPricingTx(context.Background(), tx, 8, pricing)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.Equal(t, int64(11), pricing.ID)
	require.Equal(t, int64(11), pricing.Intervals[0].PricingID)
	require.NoError(t, mock.ExpectationsWereMet())
}
