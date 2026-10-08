package repository

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestMonitorCostHistoryRollback(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	})
	repo := &channelMonitorRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO channel_monitor_histories`).WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()
	err = repo.InsertHistoryBatch(context.Background(), []*service.ChannelMonitorHistoryRow{{MonitorID: 1, CheckedAt: time.Now()}})
	require.ErrorContains(t, err, "write failed")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestMonitorCostHistoryPreservesWriteAndRollbackErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() {
		mock.ExpectClose()
		require.NoError(t, db.Close())
	})
	writeErr := errors.New("write failed")
	rollbackErr := errors.New("rollback failed")
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO channel_monitor_histories`).WillReturnError(writeErr)
	mock.ExpectRollback().WillReturnError(rollbackErr)
	repo := &channelMonitorRepository{db: db}
	err = repo.InsertHistoryBatch(context.Background(), []*service.ChannelMonitorHistoryRow{{MonitorID: 1, CheckedAt: time.Now()}})
	require.ErrorIs(t, err, writeErr)
	require.ErrorIs(t, err, rollbackErr)
	require.NoError(t, mock.ExpectationsWereMet())
}
