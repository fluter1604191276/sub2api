package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// TestReviewWarmConfirmationDeadlineSurvivesContinuousUsage is the real
// ReconcileRealUsage → ClaimDue order. A first warm round schedules
// confirmation at last_probe_at + 5 minutes. Later successful usage of the
// same probe model must not push that deadline to each tick's now + 5 minutes.
// Once the original deadline is reached, the second round is claimed and its
// completion can mark the protocol eligible.
func TestReviewWarmConfirmationDeadlineSurvivesContinuousUsage(t *testing.T) {
	db := startReviewPostgres(t)
	ctx := context.Background()
	require.NoError(t, ApplyMigrations(ctx, db))

	suffix := time.Now().UnixNano()
	model := "deepseek-chat"
	groupName := fmt.Sprintf("warm-deadline-%d", suffix)
	var groupID, accountID, userID, apiKeyID int64
	err := db.QueryRowContext(ctx, `
		INSERT INTO groups (
			name, platform, status, recovery_probe_enabled, recovery_probe_mode,
			recovery_probe_model, recovery_probe_interval_seconds,
			recovery_probe_attempts_per_round, recovery_probe_idle_threshold_seconds,
			recovery_probe_backoff_cap_seconds
		) VALUES ($1, 'openai', 'active', TRUE, 'smart', $2, 900, 1, 3600, 1800)
		RETURNING id
	`, groupName, model).Scan(&groupID)
	require.NoError(t, err)
	err = db.QueryRowContext(ctx, `
		INSERT INTO accounts (name, platform, type, status, schedulable, credentials, extra)
		VALUES ($1, 'deepseek', 'api_key', 'active', TRUE, '{"api_protocol":"adaptive"}'::jsonb, '{}'::jsonb)
		RETURNING id
	`, fmt.Sprintf("warm-deadline-account-%d", suffix)).Scan(&accountID)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM usage_logs WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(ctx, "DELETE FROM api_keys WHERE id = $1", apiKeyID)
		_, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID)
		_, _ = db.ExecContext(ctx, "DELETE FROM group_recovery_probe_states WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(ctx, "DELETE FROM group_recovery_probe_audits WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(ctx, "DELETE FROM group_recovery_probe_physical_states WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(ctx, "DELETE FROM account_groups WHERE account_id = $1", accountID)
		_, _ = db.ExecContext(ctx, "DELETE FROM scheduler_outbox WHERE account_id = $1 OR group_id = $2", accountID, groupID)
		_, _ = db.ExecContext(ctx, "DELETE FROM accounts WHERE id = $1", accountID)
		_, _ = db.ExecContext(ctx, "DELETE FROM groups WHERE id = $1", groupID)
	})
	_, err = db.ExecContext(ctx, "INSERT INTO account_groups (account_id, group_id, priority) VALUES ($1, $2, 1)", accountID, groupID)
	require.NoError(t, err)
	err = db.QueryRowContext(ctx, `
		INSERT INTO users (email, password_hash) VALUES ($1, 'test') RETURNING id
	`, fmt.Sprintf("warm-deadline-%d@example.com", suffix)).Scan(&userID)
	require.NoError(t, err)
	err = db.QueryRowContext(ctx, `
		INSERT INTO api_keys (user_id, key, name, group_id) VALUES ($1, $2, 'warm deadline', $3) RETURNING id
	`, userID, fmt.Sprintf("sk-warm-deadline-%d", suffix), groupID).Scan(&apiKeyID)
	require.NoError(t, err)

	repo := NewGroupRecoveryProbeRepository(db)
	firstAt := time.Now().UTC().Truncate(time.Microsecond)
	firstJobs, err := repo.ClaimDue(ctx, firstAt, 4)
	require.NoError(t, err)
	require.Len(t, firstJobs, 1)
	require.Equal(t, accountID, firstJobs[0].State.AccountID)
	warmAt := firstJobs[0].ClaimedAt
	confirmationAt := warmAt.Add(5 * time.Minute)
	accepted, err := repo.Complete(ctx, service.GroupRecoveryProbeCompletion{
		StateID:              firstJobs[0].State.ID,
		PhysicalStateID:      firstJobs[0].PhysicalStateID,
		ClaimedAt:            warmAt,
		Status:               service.GroupRecoveryProbeStatusWarm,
		ConsecutiveSuccesses: 1,
		LastSuccessAt:        &warmAt,
		NextProbeAt:          confirmationAt,
		LastErrorClass:       "",
		AttemptCount:         1,
		ProtocolEligibility: map[string]service.GroupRecoveryProbeProtocolStatus{
			service.APIProtocolChatCompletions: {
				Status:               service.GroupRecoveryProbeStatusWarm,
				ConsecutiveSuccesses: 1,
			},
		},
	})
	require.NoError(t, err)
	require.True(t, accepted)
	deadline, eligibility := readWarmConfirmation(t, db, accountID)
	require.WithinDuration(t, confirmationAt, deadline, time.Second)
	require.Equal(t, service.GroupRecoveryProbeStatusWarm, eligibility[service.APIProtocolChatCompletions].Status)

	// Each tick is the runner order: new successful usage, then reconcile, then claim.
	// The stored confirmation deadline has to stay on the first probe.
	for _, offset := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		tick := warmAt.Add(offset)
		insertSuccessfulProbeUsage(t, db, userID, apiKeyID, accountID, groupID, model, tick, suffix, offset)
		reconciled, err := repo.ReconcileRealUsage(ctx, tick)
		require.NoError(t, err)
		require.Equal(t, int64(1), reconciled, "tick %s did not project the new usage", tick)
		jobs, err := repo.ClaimDue(ctx, tick, 4)
		require.NoError(t, err)
		require.Empty(t, jobs, "second round claimed at %s, before %s", tick, confirmationAt)
		got, stillWarm := readWarmConfirmation(t, db, accountID)
		require.WithinDuration(t, confirmationAt, got, time.Second, "deadline slid from %s to %s at %s", confirmationAt, got, tick)
		require.False(t, got.After(confirmationAt.Add(time.Second)))
		require.Equal(t, service.GroupRecoveryProbeStatusWarm, stillWarm[service.APIProtocolChatCompletions].Status)
	}

	dueTick := warmAt.Add(10 * time.Minute)
	insertSuccessfulProbeUsage(t, db, userID, apiKeyID, accountID, groupID, model, dueTick, suffix, 10*time.Minute)
	reconciled, err := repo.ReconcileRealUsage(ctx, dueTick)
	require.NoError(t, err)
	require.Equal(t, int64(1), reconciled)
	dueJobs, err := repo.ClaimDue(ctx, dueTick, 4)
	require.NoError(t, err)
	require.Len(t, dueJobs, 1, "second confirmation was not claimed after the original deadline")
	require.Equal(t, accountID, dueJobs[0].State.AccountID)
	require.Equal(t, service.GroupRecoveryProbeStatusProbing, dueJobs[0].State.Status)
	require.Equal(t, firstJobs[0].PhysicalStateID, dueJobs[0].PhysicalStateID)

	confirmedAt := dueJobs[0].ClaimedAt
	accepted, err = repo.Complete(ctx, service.GroupRecoveryProbeCompletion{
		StateID:              dueJobs[0].State.ID,
		PhysicalStateID:      dueJobs[0].PhysicalStateID,
		ClaimedAt:            confirmedAt,
		Status:               service.GroupRecoveryProbeStatusEligible,
		ConsecutiveSuccesses: 2,
		LastSuccessAt:        &confirmedAt,
		NextProbeAt:          confirmedAt.Add(2 * time.Hour),
		LastErrorClass:       "",
		AttemptCount:         1,
		ProtocolEligibility: map[string]service.GroupRecoveryProbeProtocolStatus{
			service.APIProtocolChatCompletions: {
				Status:               service.GroupRecoveryProbeStatusEligible,
				ConsecutiveSuccesses: 2,
			},
		},
	})
	require.NoError(t, err)
	require.True(t, accepted)
	stored, err := repo.GetProtocolEligibility(ctx, dueJobs[0].PhysicalStateID)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusEligible, stored[service.APIProtocolChatCompletions].Status)
	require.Equal(t, 2, stored[service.APIProtocolChatCompletions].ConsecutiveSuccesses)
	states, err := repo.ListStates(ctx, groupID, []int64{accountID}, model)
	require.NoError(t, err)
	require.Equal(t, service.GroupRecoveryProbeStatusEligible, states[accountID].ProtocolEligibility[service.APIProtocolChatCompletions].Status)
}

func readWarmConfirmation(t *testing.T, db *sql.DB, accountID int64) (time.Time, map[string]service.GroupRecoveryProbeProtocolStatus) {
	t.Helper()
	var next time.Time
	var raw []byte
	err := db.QueryRowContext(context.Background(), `
		SELECT next_probe_at, protocol_eligibility
		FROM group_recovery_probe_states
		WHERE account_id = $1
	`, accountID).Scan(&next, &raw)
	require.NoError(t, err)
	return next.UTC(), service.DecodeGroupRecoveryProbeProtocolEligibility(raw)
}

func insertSuccessfulProbeUsage(t *testing.T, db *sql.DB, userID, apiKeyID, accountID, groupID int64, model string, at time.Time, suffix int64, offset time.Duration) {
	t.Helper()
	_, err := db.ExecContext(context.Background(), `
		INSERT INTO usage_logs (
			user_id, api_key_id, account_id, group_id, request_id, model, requested_model,
			actual_cost, stream, request_type, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $6, 0.01, TRUE, 1, $7)
	`, userID, apiKeyID, accountID, groupID, fmt.Sprintf("warm-usage-%d-%d", suffix, offset), model, at)
	require.NoError(t, err)
}

func startReviewPostgres(t *testing.T) *sql.DB {
	t.Helper()
	initdb := reviewPostgresBin("initdb")
	pgCtl := reviewPostgresBin("pg_ctl")
	if initdb == "" || pgCtl == "" {
		t.Skip("PostgreSQL server binaries are not installed; this regression executes ReconcileRealUsage and ClaimDue")
	}
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	logPath := filepath.Join(root, "postgres.log")
	require.NoError(t, os.MkdirAll(dataDir, 0o700))
	init := exec.Command(initdb, "-D", dataDir, "--username=postgres", "--auth=trust", "--locale=C.UTF-8", "--no-sync")
	init.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	out, err := init.CombinedOutput()
	require.NoError(t, err, "initdb: %s", out)

	port := freeLocalPort(t)
	// The test connects over TCP. A Unix socket in t.TempDir() exceeds the
	// 103-byte sockaddr limit once the default Mac temp prefix and the full
	// test name are included, and postgres then refuses to start.
	start := exec.Command(pgCtl, "-D", dataDir, "-w", "-t", "60", "-l", logPath, "start",
		"-o", fmt.Sprintf("-p %d -c unix_socket_directories= -c listen_addresses=127.0.0.1 -c fsync=off", port))
	start.Env = append(os.Environ(), "LC_ALL=C.UTF-8")
	out, err = start.CombinedOutput()
	if err != nil {
		log, _ := os.ReadFile(logPath)
		t.Fatalf("pg_ctl start: %v\n%s\n%s", err, out, log)
	}
	t.Cleanup(func() {
		stop := exec.Command(pgCtl, "-D", dataDir, "-m", "immediate", "-w", "stop")
		_ = stop.Run()
	})

	dsn := fmt.Sprintf("postgres://postgres@127.0.0.1:%d/postgres?sslmode=disable", port)
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	pingCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.NoError(t, pingReady(pingCtx, db))
	return db
}

func reviewPostgresBin(name string) string {
	if path, err := exec.LookPath(name); err == nil {
		return path
	}
	matches, _ := filepath.Glob("/usr/lib/postgresql/*/bin/" + name)
	if len(matches) == 0 {
		return ""
	}
	return matches[len(matches)-1]
}

func freeLocalPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := listener.Addr().(*net.TCPAddr).Port
	require.NoError(t, listener.Close())
	return port
}

func pingReady(ctx context.Context, db *sql.DB) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(30 * time.Second)
	}
	var last error
	for time.Now().Before(deadline) {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		last = db.PingContext(pingCtx)
		cancel()
		if last == nil {
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return last
}
