//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV3QualityHistoryAndClientErrors(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	var userID, keyID, accountID, groupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users(email,password_hash) VALUES('cmv3-quality@example.invalid','test') RETURNING id`).Scan(&userID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type) VALUES('cmv3-quality','openai','apikey') RETURNING id`).Scan(&accountID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups(name,platform,status) VALUES('cmv3-quality','openai','active') RETURNING id`).Scan(&groupID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO api_keys(user_id,key,name) VALUES($1,'cmv3-quality-fixture','cmv3-quality') RETURNING id`, userID).Scan(&keyID))
	now := time.Now().UTC()
	insertUsage := func(requestID string, duration int, at time.Time) {
		t.Helper()
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_logs(user_id,api_key_id,account_id,group_id,request_id,model,input_tokens,output_tokens,actual_cost,duration_ms,request_type,user_agent,created_at)
			VALUES($1,$2,$3,$4,$5,'fixture-model',1,1,0,$6,6,'sub2api-channel-monitor/v3',$7)`, userID, keyID, accountID, groupID, requestID, duration, at)
		require.NoError(t, err)
	}
	for i := 0; i < 101; i++ {
		insertUsage(fmt.Sprintf("cmv3-quality-%d", i), 100+i, now.Add(-48*time.Hour).Add(time.Duration(i)*time.Second))
	}
	insertUsage("cmv3-quality-client", 90000, now)
	insertUsage("cmv3-quality-policy", 90000, now)
	_, err := tx.ExecContext(ctx, `UPDATE usage_logs SET request_type=4 WHERE group_id=$1 AND request_id='cmv3-quality-policy'`, groupID)
	require.NoError(t, err)
	insertUsage("cmv3-quality-provider", 10000, now.Add(-time.Minute))
	_, err = tx.ExecContext(ctx, `INSERT INTO ops_error_logs(group_id,request_id,error_phase,error_type,status_code,error_owner)
		VALUES($1,'cmv3-quality-client','request','invalid_request',400,'client'),
		($1,'cmv3-quality-provider','upstream','upstream_error',503,'provider')`, groupID)
	require.NoError(t, err)

	repo := newUsageLogRepositoryWithSQL(nil, tx)
	windows, err := repo.GetChannelMonitorV3GroupQualityWindows(ctx, []int64{groupID})
	require.NoError(t, err)
	window := windows[groupID]
	require.Equal(t, int64(10), window.Last10.SampleCount)
	require.Equal(t, int64(100), window.Last100.SampleCount)
	require.InDelta(t, 1176.4, *window.Last10.AverageDurationMs, 0.001)
	require.InDelta(t, 249.49, *window.Last100.AverageDurationMs, 0.001)
}
