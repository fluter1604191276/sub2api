//go:build integration

package repository

import (
	"context"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAIIQClaimDuePostgresCreatesOnlyOneRunAcrossConcurrentWorkers(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 2)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	settings := openAIIQSettings(groupIDs)
	openAIIQCleanupRunsByQuestion(t, settings.Question)

	var claimed atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run, err := repo.ClaimDue(ctx, settings)
			if err != nil {
				errs <- err
				return
			}
			if run != nil {
				claimed.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, claimed.Load())

	var runCount, resultCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_iq_runs WHERE question = $1`, settings.Question).Scan(&runCount))
	require.Equal(t, 1, runCount)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM openai_iq_results r
		JOIN openai_iq_runs run ON run.id = r.run_id
		WHERE run.question = $1`, settings.Question).Scan(&resultCount))
	require.Equal(t, len(groupIDs), resultCount)
}

func TestOpenAIIQClaimDuePostgresDoesNotOverlapActiveRunOrReplayExpiredRun(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	settings := openAIIQSettings(groupIDs)
	openAIIQCleanupRunsByQuestion(t, settings.Question)

	activeRunID := openAIIQRun(t, "active-run", time.Now().Add(-2*time.Hour), time.Now().Add(time.Hour))
	run, err := repo.ClaimDue(ctx, settings)
	require.NoError(t, err)
	require.Nil(t, run, "a leased running run must block another dispatch even after the configured interval")

	staleRunID := openAIIQRun(t, "stale-run", time.Now().Add(-3*time.Hour), time.Now().Add(-time.Minute))
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO openai_iq_results (run_id, group_id, group_name, rate_multiplier, model, expected_answer, status)
		VALUES ($1, $2, 'stale-snapshot', 1, 'gpt-6-astra', '15', 'pending')`, staleRunID, groupIDs[0])
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO openai_iq_budget_reservations (budget_date, run_id, group_id, reserved_usd)
		VALUES ('2099-11-03', $1, $2, .1)`, staleRunID, groupIDs[0])
	require.NoError(t, err)

	_, err = integrationDB.ExecContext(ctx, `UPDATE openai_iq_runs SET status = 'error', finished_at = NOW() WHERE id = $1`, activeRunID)
	require.NoError(t, err)
	run, err = repo.ClaimDue(ctx, settings)
	require.NoError(t, err)
	require.NotNil(t, run, "an expired run is terminal and a new run may be claimed")
	require.NotEqual(t, staleRunID, run.ID)

	var staleStatus string
	var staleResults int
	var staleReservations int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT status FROM openai_iq_runs WHERE id = $1`, staleRunID).Scan(&staleStatus))
	require.Equal(t, "error", staleStatus)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_iq_results WHERE run_id = $1`, staleRunID).Scan(&staleResults))
	require.Equal(t, 1, staleResults, "a stale run's already-paid request snapshot must never be reused or removed")
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_iq_budget_reservations WHERE run_id = $1`, staleRunID).Scan(&staleReservations))
	require.Equal(t, 1, staleReservations, "a stale run's reservation remains charged rather than making a replay free")
}

func TestOpenAIIQManualClaimPostgresIsSingleGroupAndDoesNotDelaySchedule(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 2)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	settings := openAIIQSettings(groupIDs)
	openAIIQCleanupRunsByQuestion(t, settings.Question)
	requester := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("iq-manual-%d@example.invalid", time.Now().UnixNano()), Concurrency: 1})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_budget_reservations WHERE run_id IN (SELECT id FROM openai_iq_runs WHERE question = $1)`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_runs WHERE question = $1`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, requester.ID)
		require.NoError(t, err)
	})

	run, err := repo.ClaimManual(ctx, settings, groupIDs[0], requester.ID)
	require.NoError(t, err)
	require.NotNil(t, run)
	require.Equal(t, service.IQTriggerManual, run.Trigger)
	var trigger string
	var requestedBy int64
	var resultCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT trigger, requested_by FROM openai_iq_runs WHERE id = $1`, run.ID).Scan(&trigger, &requestedBy))
	require.Equal(t, service.IQTriggerManual, trigger)
	require.Equal(t, requester.ID, requestedBy)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_iq_results WHERE run_id = $1`, run.ID).Scan(&resultCount))
	require.Equal(t, 1, resultCount)
	diagnostics, err := repo.Diagnostics(ctx, 1, 20)
	require.NoError(t, err)
	require.NotEmpty(t, diagnostics.Runs)
	require.Equal(t, service.IQTriggerManual, diagnostics.Runs[0].Trigger)
	require.NotNil(t, diagnostics.Runs[0].RequestedBy)
	require.Equal(t, requester.ID, *diagnostics.Runs[0].RequestedBy)

	blocked, err := repo.ClaimDue(ctx, settings)
	require.NoError(t, err)
	require.Nil(t, blocked, "a running manual request must block the scheduler")
	require.NoError(t, integrationDB.QueryRowContext(ctx, `UPDATE openai_iq_runs SET status = 'complete', finished_at = NOW() WHERE id = $1`, run.ID).Err())

	scheduled, err := repo.ClaimDue(ctx, settings)
	require.NoError(t, err)
	require.NotNil(t, scheduled, "a completed manual run must not shift the scheduled interval")
	require.Equal(t, service.IQTriggerScheduled, scheduled.Trigger)

	_, err = repo.ClaimManual(ctx, settings, groupIDs[0], requester.ID)
	require.ErrorIs(t, err, service.ErrIQRunActive, "the new scheduled run remains globally exclusive")
}

func TestOpenAIIQManualClaimPostgresAppliesCooldownAfterTerminalRun(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	settings := openAIIQSettings(groupIDs)
	openAIIQCleanupRunsByQuestion(t, settings.Question)
	requester := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("iq-cooldown-%d@example.invalid", time.Now().UnixNano()), Concurrency: 1})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_budget_reservations WHERE run_id IN (SELECT id FROM openai_iq_runs WHERE question = $1)`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_runs WHERE question = $1`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, requester.ID)
		require.NoError(t, err)
	})

	run, err := repo.ClaimManual(ctx, settings, groupIDs[0], requester.ID)
	require.NoError(t, err)
	require.NoError(t, integrationDB.QueryRowContext(ctx, `UPDATE openai_iq_runs SET status = 'complete', finished_at = NOW() WHERE id = $1`, run.ID).Err())
	second, err := repo.ClaimManual(ctx, settings, groupIDs[0], requester.ID)
	require.Nil(t, second)
	require.ErrorIs(t, err, service.ErrIQManualCooldown)
}

func TestOpenAIIQManualClaimPostgresAllowsOnlyOneConcurrentRun(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	settings := openAIIQSettings(groupIDs)
	openAIIQCleanupRunsByQuestion(t, settings.Question)
	requester := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("iq-manual-lock-%d@example.invalid", time.Now().UnixNano()), Concurrency: 1})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_budget_reservations WHERE run_id IN (SELECT id FROM openai_iq_runs WHERE question = $1)`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_runs WHERE question = $1`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, requester.ID)
		require.NoError(t, err)
	})

	start := make(chan struct{})
	errs := make(chan error, 2)
	var claimed atomic.Int64
	var wg sync.WaitGroup
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			run, err := repo.ClaimManual(ctx, settings, groupIDs[0], requester.ID)
			if run != nil {
				claimed.Add(1)
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	var activeErrors int
	for err := range errs {
		if err != nil {
			require.ErrorIs(t, err, service.ErrIQRunActive)
			activeErrors++
		}
	}
	require.EqualValues(t, 1, claimed.Load())
	require.Equal(t, 1, activeErrors)
}

func TestOpenAIIQManualAndScheduledClaimsPostgresShareOneLease(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	settings := openAIIQSettings(groupIDs)
	openAIIQCleanupRunsByQuestion(t, settings.Question)
	requester := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("iq-manual-scheduled-lock-%d@example.invalid", time.Now().UnixNano()), Concurrency: 1})
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_budget_reservations WHERE run_id IN (SELECT id FROM openai_iq_runs WHERE question = $1)`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_runs WHERE question = $1`, settings.Question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, requester.ID)
		require.NoError(t, err)
	})

	type claimResult struct {
		run *service.IQRun
		err error
	}
	start := make(chan struct{})
	manualResult := make(chan claimResult, 1)
	scheduledResult := make(chan claimResult, 1)
	go func() {
		<-start
		run, err := repo.ClaimManual(ctx, settings, groupIDs[0], requester.ID)
		manualResult <- claimResult{run: run, err: err}
	}()
	go func() {
		<-start
		run, err := repo.ClaimDue(ctx, settings)
		scheduledResult <- claimResult{run: run, err: err}
	}()
	close(start)
	manual := <-manualResult
	scheduled := <-scheduledResult
	if manual.err != nil {
		require.ErrorIs(t, manual.err, service.ErrIQRunActive)
	}
	require.NoError(t, scheduled.err)
	var runCount int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_iq_runs WHERE question = $1`, settings.Question).Scan(&runCount))
	require.Equal(t, 1, runCount)
	if manual.run != nil {
		require.Nil(t, scheduled.run)
		require.Equal(t, service.IQTriggerManual, manual.run.Trigger)
	} else {
		require.NotNil(t, scheduled.run)
		require.Equal(t, service.IQTriggerScheduled, scheduled.run.Trigger)
	}
}

func TestOpenAIIQClaimsPostgresStartLeaseAfterAdvisoryLockAcquisition(t *testing.T) {
	ctx := context.Background()
	for _, manual := range []bool{false, true} {
		name := "scheduled"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			groupIDs := openAIIQGroups(t, 1)
			repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
			settings := openAIIQSettings(groupIDs)
			openAIIQCleanupRunsByQuestion(t, settings.Question)
			var requestedBy int64
			if manual {
				requester := mustCreateUser(t, integrationEntClient, &service.User{Email: fmt.Sprintf("iq-lock-time-%d@example.invalid", time.Now().UnixNano()), Concurrency: 1})
				requestedBy = requester.ID
				t.Cleanup(func() {
					_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_budget_reservations WHERE run_id IN (SELECT id FROM openai_iq_runs WHERE question = $1)`, settings.Question)
					require.NoError(t, err)
					_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_runs WHERE question = $1`, settings.Question)
					require.NoError(t, err)
					_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM users WHERE id = $1`, requester.ID)
					require.NoError(t, err)
				})
			}

			holder, err := integrationDB.BeginTx(ctx, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = holder.Rollback() })
			require.NoError(t, holder.QueryRowContext(ctx, `SELECT pg_advisory_xact_lock(14020261104)`).Err())
			type result struct {
				run *service.IQRun
				err error
			}
			claimed := make(chan result, 1)
			go func() {
				if manual {
					run, err := repo.ClaimManual(ctx, settings, groupIDs[0], requestedBy)
					claimed <- result{run: run, err: err}
					return
				}
				run, err := repo.ClaimDue(ctx, settings)
				claimed <- result{run: run, err: err}
			}()

			time.Sleep(150 * time.Millisecond)
			releasedAt := time.Now().UTC()
			require.NoError(t, holder.Commit())
			out := <-claimed
			require.NoError(t, out.err)
			require.NotNil(t, out.run)
			var startedAt, leaseExpiresAt time.Time
			require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT started_at, lease_expires_at FROM openai_iq_runs WHERE id=$1`, out.run.ID).Scan(&startedAt, &leaseExpiresAt))
			require.False(t, startedAt.Before(releasedAt.Add(-50*time.Millisecond)), "started_at must be captured after waiting for the advisory lock")
			require.WithinDuration(t, startedAt.Add(10*time.Minute), leaseExpiresAt, time.Millisecond)
		})
	}
}

func TestOpenAIIQManualResultLatestValidUpdatesOnlyItsGroup(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 2)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	base := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	scheduledRun := openAIIQRun(t, "scheduled fixture", base, base.Add(time.Hour))
	iqInsertHistoricalResult(t, scheduledRun, groupIDs[0], "gpt-6-astra", "correct", "15", &base)
	iqInsertHistoricalResult(t, scheduledRun, groupIDs[1], "gpt-6-astra", "correct", "15", &base)
	manualCorrectAt := base.Add(10 * time.Minute)
	manualCorrectRun := openAIIQRun(t, "manual correct fixture", manualCorrectAt, manualCorrectAt.Add(time.Hour))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `UPDATE openai_iq_runs SET trigger=$2 WHERE id=$1`, manualCorrectRun, service.IQTriggerManual).Err())
	iqInsertHistoricalResult(t, manualCorrectRun, groupIDs[0], "gpt-6-astra", "correct", "15", &manualCorrectAt)

	items, err := repo.LatestValid(ctx, groupIDs, groupIDs, "gpt-6-astra", "current", "15")
	require.NoError(t, err)
	require.Len(t, items, 2)
	byGroup := map[int64]service.IQPublicResult{}
	for _, item := range items {
		byGroup[item.GroupID] = item
	}
	require.Equal(t, "manual correct fixture", byGroup[groupIDs[0]].Question)
	require.Equal(t, "correct", byGroup[groupIDs[0]].Status)
	require.Equal(t, "15", byGroup[groupIDs[0]].ModelAnswer)
	require.Equal(t, "scheduled fixture", byGroup[groupIDs[1]].Question)
	require.Equal(t, "correct", byGroup[groupIDs[1]].Status)

	manualIncorrectAt := manualCorrectAt.Add(10 * time.Minute)
	manualIncorrectRun := openAIIQRun(t, "manual incorrect fixture", manualIncorrectAt, manualIncorrectAt.Add(time.Hour))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `UPDATE openai_iq_runs SET trigger=$2 WHERE id=$1`, manualIncorrectRun, service.IQTriggerManual).Err())
	iqInsertHistoricalResult(t, manualIncorrectRun, groupIDs[0], "gpt-6-astra", "incorrect", "14", &manualIncorrectAt)
	items, err = repo.LatestValid(ctx, groupIDs, groupIDs, "gpt-6-astra", "current", "15")
	require.NoError(t, err)
	for _, item := range items {
		if item.GroupID == groupIDs[0] {
			require.Equal(t, "manual incorrect fixture", item.Question)
			require.Equal(t, "incorrect", item.Status)
			require.Equal(t, "14", item.ModelAnswer)
		}
	}

	failedAt := manualIncorrectAt.Add(10 * time.Minute)
	failedManualRun := openAIIQRun(t, "failed manual fixture", failedAt, failedAt.Add(time.Hour))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `UPDATE openai_iq_runs SET trigger=$2 WHERE id=$1`, failedManualRun, service.IQTriggerManual).Err())
	iqInsertHistoricalResult(t, failedManualRun, groupIDs[0], "gpt-6-astra", "error", "", &failedAt)
	items, err = repo.LatestValid(ctx, groupIDs, groupIDs, "gpt-6-astra", "current", "15")
	require.NoError(t, err)
	for _, item := range items {
		if item.GroupID == groupIDs[0] {
			require.Equal(t, "manual incorrect fixture", item.Question, "a failed manual result must not hide the prior valid manual result")
			require.Equal(t, "incorrect", item.Status)
		}
	}
}

func TestOpenAIIQReserveBudgetPostgresCapsConcurrentDistinctGroups(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 6)
	runID := openAIIQRun(t, "budget-cap", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	openAIIQPendingResults(t, runID, groupIDs)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	day := time.Date(2099, 11, 4, 0, 0, 0, 0, time.UTC)

	var admitted atomic.Int64
	var wg sync.WaitGroup
	start := make(chan struct{})
	errs := make(chan error, len(groupIDs))
	for _, groupID := range groupIDs {
		groupID := groupID
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			ok, err := repo.ReserveBudget(ctx, runID, groupID, day, .1, .3)
			if err != nil {
				errs <- err
			} else if ok {
				admitted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.EqualValues(t, 3, admitted.Load())

	var total float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COALESCE(SUM(reserved_usd), 0) FROM openai_iq_budget_reservations WHERE run_id = $1`, runID).Scan(&total))
	require.InDelta(t, .3, total, .000001)
}

func TestOpenAIIQReserveBudgetPostgresRejectsDuplicateAndSurvivesRepositoryRecreation(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	runID := openAIIQRun(t, "budget-durable", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	openAIIQPendingResults(t, runID, groupIDs)
	day := time.Date(2099, 11, 5, 0, 0, 0, 0, time.UTC)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)

	ok, err := repo.ReserveBudget(ctx, runID, groupIDs[0], day, .1, 1)
	require.NoError(t, err)
	require.True(t, ok)

	recreated := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	ok, err = recreated.ReserveBudget(ctx, runID, groupIDs[0], day, .1, 1)
	require.NoError(t, err)
	require.False(t, ok, "the same run/group must not be dispatched twice after process recreation")

	var count int
	var total float64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(reserved_usd), 0) FROM openai_iq_budget_reservations WHERE run_id = $1 AND group_id = $2`, runID, groupIDs[0]).Scan(&count, &total))
	require.Equal(t, 1, count)
	require.InDelta(t, .1, total, .000001)
}

func TestOpenAIIQReserveBudgetPostgresRejectsInvalidAmountsWithoutWriting(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	runID := openAIIQRun(t, "budget-invalid", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	day := time.Date(2099, 11, 6, 0, 0, 0, 0, time.UTC)

	for _, amount := range []float64{0, -0.01, math.NaN(), math.Inf(1), math.Inf(-1)} {
		ok, err := repo.ReserveBudget(ctx, runID, groupIDs[0], day, amount, 1)
		require.Error(t, err)
		require.False(t, ok)
	}

	var count int
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT COUNT(*) FROM openai_iq_budget_reservations WHERE run_id = $1`, runID).Scan(&count))
	require.Zero(t, count)
}

func TestOpenAIIQReserveBudgetPostgresRoundsBeforeAdmission(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	runID := openAIIQRun(t, "budget-rounding", time.Now(), time.Now().Add(time.Hour))
	openAIIQPendingResults(t, runID, groupIDs)
	day := time.Date(2099, 11, 7, 0, 0, 0, 0, time.UTC)
	repo := NewOpenAIIQRepository(integrationDB)

	for _, limit := range []float64{0.00000000001, 0.0999999999999} {
		ok, err := repo.ReserveBudget(ctx, runID, groupIDs[0], day, limit, limit)
		require.NoError(t, err)
		require.False(t, ok, "rounding to the stored monetary precision must happen before the budget check")
	}
	ok, err := repo.ReserveBudget(ctx, runID, groupIDs[0], day, 0.1, 0.1)
	require.NoError(t, err)
	require.True(t, ok)
	var total string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `
		SELECT SUM(reserved_usd)::text FROM openai_iq_budget_reservations WHERE run_id = $1`, runID).Scan(&total))
	require.Equal(t, "0.1000000000", total)
}

func TestOpenAIIQLatestPostgresKeepsVisibleRunSnapshotWhenNewerRunIsHidden(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 2)
	visibleRunID := openAIIQRun(t, "visible-question", time.Now().Add(-2*time.Hour), time.Now().Add(time.Hour))
	hiddenRunID := openAIIQRun(t, "hidden-question", time.Now().Add(-time.Hour), time.Now().Add(time.Hour))

	_, err := integrationDB.ExecContext(ctx, `
		INSERT INTO openai_iq_results (run_id, group_id, group_name, rate_multiplier, model, response_model, expected_answer, model_answer, output, status, checked_at)
		VALUES ($1, $2, 'visible-group', 1.25, 'gpt-6-astra', 'actual-model', '15', '15', 'FINAL_ANSWER: 15', 'correct', NOW())`, visibleRunID, groupIDs[0])
	require.NoError(t, err)
	_, err = integrationDB.ExecContext(ctx, `
		INSERT INTO openai_iq_results (run_id, group_id, group_name, rate_multiplier, model, expected_answer, status)
		VALUES ($1, $2, 'hidden-group', 1, 'newer-model', '15', 'pending')`, hiddenRunID, groupIDs[1])
	require.NoError(t, err)

	view, err := NewOpenAIIQRepository(integrationDB).Latest(ctx, []int64{groupIDs[0]})
	require.NoError(t, err)
	require.NotNil(t, view.RoundStartedAt)
	require.Equal(t, "visible-question", view.Question)
	require.Equal(t, "gpt-6-astra", view.Model)
	require.Len(t, view.Results, 1)
	require.Equal(t, "actual-model", view.Results[0].ResponseModel)
	require.Equal(t, "15", view.Results[0].ExpectedAnswer)
}

func TestOpenAIIQBudgetPostgresSettlesAuthoritativeUsageAndReleasesHold(t *testing.T) {
	ctx := context.Background()
	groupIDs := openAIIQGroups(t, 1)
	repo := NewOpenAIIQRepository(integrationDB).(*openAIIQRepository)
	identity, err := repo.EnsureIdentity(ctx, groupIDs)
	require.NoError(t, err)
	key, err := repo.GroupKey(ctx, identity.UserID, groupIDs[0])
	require.NoError(t, err)
	runID := openAIIQRun(t, "settlement-release", time.Now(), time.Now().Add(time.Hour))
	openAIIQPendingResults(t, runID, groupIDs)
	day := service.IQBudgetDate()
	ok, err := repo.ReserveBudget(ctx, runID, groupIDs[0], day, .1, 1)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, repo.BindBudgetRequest(ctx, runID, groupIDs[0], key.ID, "client:settle-release"))

	var accountID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO accounts (name, platform, type) VALUES ($1, 'openai', 'apikey') RETURNING id`, fmt.Sprintf("iq-settlement-%d", runID)).Scan(&accountID))
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM usage_logs WHERE api_key_id=$1`, key.ID)
		_, _ = integrationDB.ExecContext(context.Background(), `DELETE FROM accounts WHERE id=$1`, accountID)
	})
	var usageID int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `INSERT INTO usage_logs (user_id, api_key_id, account_id, group_id, request_id, model, total_cost, account_stats_cost, account_rate_multiplier, user_agent)
		VALUES ($1,$2,$3,$4,'client:settle-release','gpt-6-astra',.9,.009,2,'sub2api-openai-iq') RETURNING id`, identity.UserID, key.ID, accountID, groupIDs[0]).Scan(&usageID))
	var boundKey, boundUser int64
	var boundRequest string
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT key_id, user_id, request_id FROM openai_iq_budget_reservations WHERE run_id=$1`, runID).Scan(&boundKey, &boundUser, &boundRequest))
	require.Equal(t, key.ID, boundKey)
	require.Equal(t, identity.UserID, boundUser)
	require.Equal(t, "client:settle-release", boundRequest)
	require.NoError(t, repo.ReconcileBudget(ctx))

	var actual, base, rate float64
	var evidenceUsage int64
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT actual_cost_usd, account_base_cost_usd, account_rate_multiplier, usage_log_id FROM openai_iq_budget_reservations WHERE run_id=$1`, runID).Scan(&actual, &base, &rate, &evidenceUsage))
	require.InDelta(t, .018, actual, .000000001)
	require.InDelta(t, .009, base, .000000001)
	require.InDelta(t, 2, rate, .000000001)
	require.Equal(t, usageID, evidenceUsage)
	budget, err := repo.Budget(ctx, 1)
	require.NoError(t, err)
	require.InDelta(t, .018, budget.SpentUSD, .000000001)
	require.Zero(t, budget.ReservedUSD)
	require.InDelta(t, .982, budget.RemainingUSD, .000000001)
	_, err = integrationDB.ExecContext(ctx, `DELETE FROM usage_logs WHERE id=$1`, usageID)
	require.NoError(t, err)
	require.NoError(t, NewOpenAIIQRepository(integrationDB).(*openAIIQRepository).ReconcileBudget(ctx))
	require.NoError(t, integrationDB.QueryRowContext(ctx, `SELECT actual_cost_usd FROM openai_iq_budget_reservations WHERE run_id=$1`, runID).Scan(&actual))
	require.InDelta(t, .018, actual, .000000001)
}

func openAIIQSettings(groupIDs []int64) service.IQSettings {
	return service.IQSettings{
		Enabled:         true,
		Question:        fmt.Sprintf("iq-claim-%d", time.Now().UnixNano()),
		ExpectedAnswer:  "15",
		GroupIDs:        groupIDs,
		Model:           "gpt-6-astra",
		IntervalSeconds: 3600,
	}
}

func openAIIQGroups(t *testing.T, count int) []int64 {
	t.Helper()
	ctx := context.Background()
	groupIDs := make([]int64, 0, count)
	for index := range count {
		var groupID int64
		require.NoError(t, integrationDB.QueryRowContext(ctx, `
			INSERT INTO groups (name, platform, rate_multiplier, status, subscription_type)
			VALUES ($1, $2, 1, 'active', 'standard') RETURNING id`,
			fmt.Sprintf("openai-iq-%d-%d", time.Now().UnixNano(), index), service.PlatformOpenAI).Scan(&groupID))
		groupIDs = append(groupIDs, groupID)
	}
	t.Cleanup(func() {
		for _, groupID := range groupIDs {
			_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM groups WHERE id = $1`, groupID)
			require.NoError(t, err)
		}
	})
	return groupIDs
}

func openAIIQRun(t *testing.T, question string, startedAt, leaseExpiresAt time.Time) int64 {
	t.Helper()
	var runID int64
	require.NoError(t, integrationDB.QueryRowContext(context.Background(), `
		INSERT INTO openai_iq_runs (started_at, question, expected_answer, model, lease_expires_at)
		VALUES ($1, $2, '15', 'gpt-6-astra', $3) RETURNING id`, startedAt, question, leaseExpiresAt).Scan(&runID))
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_budget_reservations WHERE run_id = $1`, runID)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_runs WHERE id = $1`, runID)
		require.NoError(t, err)
	})
	return runID
}

func openAIIQPendingResults(t *testing.T, runID int64, groupIDs []int64) {
	t.Helper()
	for _, groupID := range groupIDs {
		_, err := integrationDB.ExecContext(context.Background(), `
			INSERT INTO openai_iq_results (run_id, group_id, group_name, rate_multiplier, model, expected_answer, status)
			VALUES ($1, $2, $3, 1, 'gpt-6-astra', '15', 'pending')`,
			runID, groupID, fmt.Sprintf("budget-group-%d", groupID))
		require.NoError(t, err)
	}
}

func openAIIQCleanupRunsByQuestion(t *testing.T, question string) {
	t.Helper()
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(context.Background(), `
			DELETE FROM openai_iq_budget_reservations
			WHERE run_id IN (SELECT id FROM openai_iq_runs WHERE question = $1)`, question)
		require.NoError(t, err)
		_, err = integrationDB.ExecContext(context.Background(), `DELETE FROM openai_iq_runs WHERE question = $1`, question)
		require.NoError(t, err)
	})
}
