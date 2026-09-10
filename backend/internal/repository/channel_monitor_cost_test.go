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
	defer db.Close()
	repo := &channelMonitorRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectExec(`INSERT INTO channel_monitor_histories`).WillReturnError(errors.New("write failed"))
	mock.ExpectRollback()
	err = repo.InsertHistoryBatch(context.Background(), []*service.ChannelMonitorHistoryRow{{MonitorID: 1, CheckedAt: time.Now()}})
	require.ErrorContains(t, err, "write failed")
	require.NoError(t, mock.ExpectationsWereMet())
}
