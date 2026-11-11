//go:build integration

package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIIQLatestValidPostgresPreservesPerModelGroupHistory(t *testing.T) {
	ctx := context.Background()
	groups := openAIIQGroups(t, 2)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	when := time.Now().UTC().Truncate(time.Microsecond)
	first := openAIIQRun(t, "old question", when.Add(-time.Hour), when.Add(time.Hour))
	second := openAIIQRun(t, "new question", when, when.Add(time.Hour))
	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO openai_iq_results (run_id, group_id, group_name, rate_multiplier, model, expected_answer, model_answer, output, status, checked_at)
		VALUES ($1,$3,'g1',1,'gpt-old','15','15','old','correct',$5),
		       ($2,$3,'g1',1,'gpt-old','15','','late error','error',$5),
		       ($2,$4,'g2',1,'gpt-new','15','','only error','error',$5)`, first, second, groups[0], groups[1], when)
	require.NoError(t, err)
	items, err := repo.LatestValid(ctx, groups, []int64{groups[0]}, "gpt-new", "today question", "15")
	require.NoError(t, err)
	require.Len(t, items, 3)
	byPair := map[string]service.IQPublicResult{}
	for _, item := range items {
		byPair[fmt.Sprintf("%d/%s", item.GroupID, item.Model)] = item
	}
	old := byPair[fmt.Sprintf("%d/gpt-old", groups[0])]
	require.Equal(t, "correct", old.Status)
	require.Equal(t, "old question", old.Question)
	require.Equal(t, "old", old.Output)
	require.NotNil(t, old.CheckedAt)
	require.True(t, old.CheckedAt.Equal(when))
	invalid := byPair[fmt.Sprintf("%d/gpt-new", groups[1])]
	require.Equal(t, "no_results", invalid.Status)
	require.Empty(t, invalid.Question, "an invalid historical pair must not borrow today's puzzle")
	current := byPair[fmt.Sprintf("%d/gpt-new", groups[0])]
	require.Equal(t, "no_results", current.Status)
	require.Equal(t, "today question", current.Question, "configured current pair remains visible even with a different model history")
	filtered, err := repo.LatestValid(ctx, []int64{groups[0]}, groups, "gpt-new", "today question", "15")
	require.NoError(t, err)
	require.Len(t, filtered, 2)
	for _, item := range filtered {
		require.Equal(t, groups[0], item.GroupID, "neither history nor configured placeholders may expose a hidden group")
	}
}

func iqInsertHistoricalResult(t *testing.T, runID, groupID int64, model, status, answer string, checked *time.Time) int64 {
	t.Helper()
	var id int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		INSERT INTO openai_iq_results (run_id,group_id,group_name,rate_multiplier,model,expected_answer,model_answer,output,status,checked_at,error)
		VALUES ($1,$2,'historical',1,$3,'15',$4,$5,$6,$7,'admin-only diagnostic') RETURNING id`,
		runID, groupID, model, answer, "answer "+answer, status, checked).Scan(&id))
	return id
}

func TestOpenAIIQLatestValidPostgresKeepsIncorrectAndIndependentGroupTimes(t *testing.T) {
	ctx := context.Background()
	groups := openAIIQGroups(t, 2)
	repo := NewOpenAIIQRepository(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	firstTime, secondTime := now.Add(-time.Hour), now.Add(-30*time.Minute)
	first := openAIIQRun(t, "first puzzle", now.Add(-2*time.Hour), now.Add(time.Hour))
	second := openAIIQRun(t, "second puzzle", now.Add(-time.Hour), now.Add(time.Hour))
	iqInsertHistoricalResult(t, first, groups[0], "gpt-6-astra", "correct", "15", &firstTime)
	iqInsertHistoricalResult(t, first, groups[1], "gpt-6-astra", "correct", "15", &firstTime)
	iqInsertHistoricalResult(t, second, groups[0], "gpt-6-astra", "incorrect", "14", &secondTime)
	iqInsertHistoricalResult(t, second, groups[1], "gpt-6-astra", "error", "", &secondTime)
	before, err := repo.LatestValid(ctx, groups, groups, "gpt-6-astra", "current", "15")
	require.NoError(t, err)
	require.Len(t, before, 2)
	for _, status := range []string{"error", "skipped", "pending"} {
		runID := openAIIQRun(t, "later "+status, now, now.Add(time.Hour))
		var checked *time.Time
		if status != "pending" {
			checked = &now
		}
		for _, groupID := range groups {
			iqInsertHistoricalResult(t, runID, groupID, "gpt-6-astra", status, "", checked)
		}
	}
	after, err := repo.LatestValid(ctx, groups, groups, "gpt-6-astra", "current", "15")
	require.NoError(t, err)
	require.Equal(t, before, after, "failed/pending rounds must preserve all valid data including exact timestamps")
	for _, item := range after {
		if item.GroupID == groups[0] {
			require.Equal(t, "incorrect", item.Status)
			require.Equal(t, "second puzzle", item.Question)
			require.Equal(t, "14", item.ModelAnswer)
			require.True(t, item.CheckedAt.Equal(secondTime))
		} else {
			require.Equal(t, "first puzzle", item.Question)
			require.True(t, item.CheckedAt.Equal(firstTime))
		}
	}
}

func TestOpenAIIQLatestValidPostgresOrdersByCheckTimeThenResultID(t *testing.T) {
	ctx := context.Background()
	groups := openAIIQGroups(t, 1)
	repo := NewOpenAIIQRepository(integrationDB)
	now := time.Now().UTC().Truncate(time.Microsecond)
	checked := now.Add(-time.Minute)
	newerRun := openAIIQRun(t, "newer start", now, now.Add(time.Hour))
	olderRun := openAIIQRun(t, "older start but later completion", now.Add(-time.Hour), now.Add(time.Hour))
	iqInsertHistoricalResult(t, newerRun, groups[0], "gpt-6-astra", "correct", "15", &checked)
	iqInsertHistoricalResult(t, olderRun, groups[0], "gpt-6-astra", "incorrect", "12", &now)
	items, err := repo.LatestValid(ctx, groups, nil, "gpt-6-astra", "current", "15")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "12", items[0].ModelAnswer, "run start must not decide which completion is newest")
	tieRun := openAIIQRun(t, "same completion higher result ID", now.Add(-2*time.Hour), now.Add(time.Hour))
	iqInsertHistoricalResult(t, tieRun, groups[0], "gpt-6-astra", "correct", "15", &now)
	invalidRun := openAIIQRun(t, "missing completion time", now, now.Add(time.Hour))
	iqInsertHistoricalResult(t, invalidRun, groups[0], "gpt-6-astra", "incorrect", "8", nil)
	items, err = repo.LatestValid(ctx, groups, nil, "gpt-6-astra", "current", "15")
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "same completion higher result ID", items[0].Question)
	require.Equal(t, "15", items[0].ModelAnswer)
}

func TestOpenAIIQDiagnosticsPostgresRetainsHistoricalStatesAndReservation(t *testing.T) {
	f := newIQSettlementFixture(t)
	ctx := context.Background()
	require.NoError(t, f.repo.CompleteResult(ctx, f.runID, service.IQResult{GroupID: f.groupIDs[0], Status: "error", Error: "admin failure"}))
	for i, status := range []string{"correct", "incorrect", "skipped"} {
		oldRun := openAIIQRun(t, "older "+status, time.Now().Add(-time.Duration(i+1)*time.Hour), time.Now().Add(time.Hour))
		checked := time.Now().Add(-time.Duration(i+1) * time.Hour)
		iqInsertHistoricalResult(t, oldRun, f.groupIDs[0], "gpt-6-astra", status, "15", &checked)
	}
	page, err := f.repo.Diagnostics(ctx, 1, 1)
	require.NoError(t, err)
	require.Equal(t, 4, page.Total)
	require.Len(t, page.Runs, 1)
	require.Equal(t, f.runID, page.Runs[0].ID)
	require.Len(t, page.Runs[0].Results, 2)
	for _, result := range page.Runs[0].Results {
		if result.GroupID == f.groupIDs[0] {
			require.Equal(t, "error", result.Status)
			require.Equal(t, "admin failure", result.Error)
			require.NotNil(t, result.Reservation)
			require.Equal(t, f.requestID, *result.Reservation.RequestID)
			require.Nil(t, result.Reservation.ActualCostUSD)
		} else {
			require.Equal(t, "pending", result.Status)
			require.Nil(t, result.Reservation)
		}
	}
	for i, status := range []string{"correct", "incorrect", "skipped"} {
		page, err = f.repo.Diagnostics(ctx, i+2, 1)
		require.NoError(t, err)
		require.Len(t, page.Runs, 1)
		require.Len(t, page.Runs[0].Results, 1)
		require.Equal(t, status, page.Runs[0].Results[0].Status)
	}
	page, err = f.repo.Diagnostics(ctx, 5, 1)
	require.NoError(t, err)
	require.Empty(t, page.Runs)
	require.Equal(t, 4, page.Total)
}

func TestOpenAIIQDiagnosticsPostgresComputesLeaseActiveWithoutMutatingRuns(t *testing.T) {
	ctx := context.Background()
	repo := NewOpenAIIQRepository(integrationDB)
	now := time.Now().UTC()
	activeRun := openAIIQRun(t, "diagnostics lease active", now.Add(3*time.Minute), now.Add(5*time.Minute))
	expiredRun := openAIIQRun(t, "diagnostics lease expired", now.Add(2*time.Minute), now.Add(-5*time.Minute))
	terminalRun := openAIIQRun(t, "diagnostics lease terminal", now.Add(time.Minute), now.Add(5*time.Minute))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		UPDATE openai_iq_runs SET status='complete', finished_at=NOW() WHERE id=$1`, terminalRun).Err())

	page, err := repo.Diagnostics(ctx, 1, 50)
	require.NoError(t, err)
	leaseActive := map[int64]bool{}
	for _, run := range page.Runs {
		leaseActive[run.ID] = run.LeaseActive
	}
	require.True(t, leaseActive[activeRun])
	require.False(t, leaseActive[expiredRun])
	require.False(t, leaseActive[terminalRun])

	var activeStatus, expiredStatus, terminalStatus string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM openai_iq_runs WHERE id=$1`, activeRun).Scan(&activeStatus))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM openai_iq_runs WHERE id=$1`, expiredRun).Scan(&expiredStatus))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM openai_iq_runs WHERE id=$1`, terminalRun).Scan(&terminalStatus))
	require.Equal(t, "running", activeStatus)
	require.Equal(t, "running", expiredStatus, "diagnostics must not clean up expired runs")
	require.Equal(t, "complete", terminalStatus)
}

func TestOpenAIIQDiagnosticsPostgresMaximumPageDoesNotOverflow(t *testing.T) {
	repo := NewOpenAIIQRepository(integrationDB)
	page, err := repo.Diagnostics(context.Background(), 1000000, 50)
	require.NoError(t, err)
	require.Empty(t, page.Runs)
	require.Equal(t, 1000000, page.Page)
	require.Equal(t, 50, page.PageSize)
}
