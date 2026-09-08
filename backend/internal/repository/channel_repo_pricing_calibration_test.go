//go:build unit

package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestApplyModelCalibrationUpdatesRowsInOneTransaction(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &channelRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE channel_model_pricing").
		WithArgs(`["claude-sonnet-4-6","claude-opus-5"]`, int64(11), int64(7), `["claude-sonnet-4-6"]`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("UPDATE channel_model_pricing").
		WithArgs(`["gpt-5.4","gpt-5.5"]`, int64(12), int64(8), `["gpt-5.4"]`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err = repo.ApplyModelCalibration(context.Background(), []service.ChannelPricingModelsUpdate{
		{ChannelID: 7, PricingID: 11, PreviousModels: []string{"claude-sonnet-4-6"}, Models: []string{"claude-sonnet-4-6", "claude-opus-5"}},
		{ChannelID: 8, PricingID: 12, PreviousModels: []string{"gpt-5.4"}, Models: []string{"gpt-5.4", "gpt-5.5"}},
	})

	require.NoError(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestApplyModelCalibrationRollsBackWhenAnyRowIsMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()

	repo := &channelRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec("UPDATE channel_model_pricing").
		WithArgs(`["claude-opus-5"]`, int64(99), int64(7), `["claude-old"]`).
		WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectRollback()

	err = repo.ApplyModelCalibration(context.Background(), []service.ChannelPricingModelsUpdate{
		{ChannelID: 7, PricingID: 99, PreviousModels: []string{"claude-old"}, Models: []string{"claude-opus-5"}},
	})

	require.ErrorContains(t, err, "pricing entry changed or not found during calibration")
	require.NoError(t, mock.ExpectationsWereMet())
}
