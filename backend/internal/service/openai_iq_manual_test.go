package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/stretchr/testify/require"
)

type iqManualRepoStub struct {
	*iqRunnerRepoStub
	claimErr     error
	manualClaims int
	requestedBy  int64
	groupID      int64
	claimStart   chan struct{}
	claimGate    <-chan struct{}
	finishStart  chan struct{}
	finishGate   <-chan struct{}
}

func (r *iqManualRepoStub) ClaimManual(_ context.Context, settings IQSettings, groupID, requestedBy int64) (*IQRun, error) {
	r.manualClaims++
	r.requestedBy, r.groupID = requestedBy, groupID
	if r.claimStart != nil {
		close(r.claimStart)
	}
	if r.claimGate != nil {
		<-r.claimGate
	}
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	return &IQRun{ID: 123, Trigger: IQTriggerManual, Question: settings.Question, Answer: settings.ExpectedAnswer, Model: settings.Model}, nil
}

func (r *iqManualRepoStub) FinishRun(ctx context.Context, runID int64) error {
	if r.finishStart != nil {
		close(r.finishStart)
	}
	if r.finishGate != nil {
		select {
		case <-r.finishGate:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.iqRunnerRepoStub.FinishRun(ctx, runID)
}

func newIQManualFixture(settings IQSettings) (*OpenAIIQService, *iqManualRepoStub, *iqBlockingTransport) {
	repo := &iqManualRepoStub{iqRunnerRepoStub: &iqRunnerRepoStub{
		identity: &IQIdentity{UserID: 12, Balance: 1},
		results:  []IQResult{{GroupID: 7, Status: "pending"}},
		reserve:  true,
	}}
	transport := &iqBlockingTransport{entered: make(chan struct{}, 1), release: make(chan struct{})}
	return iqSchedulingService(repo, settings, transport), repo, transport
}

func TestOpenAIIQManualValidatesBeforeClaim(t *testing.T) {
	for _, tc := range []struct {
		name       string
		group      int64
		limit      float64
		identity   *IQIdentity
		claimErr   error
		wantStatus int
	}{
		{name: "invalid group id", group: 0, limit: 1, identity: &IQIdentity{UserID: 12, Balance: 1}, wantStatus: http.StatusBadRequest},
		{name: "not configured", group: 8, limit: 1, identity: &IQIdentity{UserID: 12, Balance: 1}, wantStatus: http.StatusBadRequest},
		{name: "missing confirmation limit", group: 7, limit: 0, identity: &IQIdentity{UserID: 12, Balance: 1}, wantStatus: http.StatusBadRequest},
		{name: "allowance increased", group: 7, limit: .5, identity: &IQIdentity{UserID: 12, Balance: 1}, wantStatus: http.StatusConflict},
		{name: "missing identity", group: 7, limit: 1, wantStatus: http.StatusConflict},
		{name: "unfunded identity", group: 7, limit: 1, identity: &IQIdentity{UserID: 12}, wantStatus: http.StatusConflict},
		{name: "active run", group: 7, limit: 1, identity: &IQIdentity{UserID: 12, Balance: 1}, claimErr: ErrIQRunActive, wantStatus: http.StatusConflict},
		{name: "cooldown", group: 7, limit: 1, identity: &IQIdentity{UserID: 12, Balance: 1}, claimErr: ErrIQManualCooldown, wantStatus: http.StatusTooManyRequests},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, _ := newIQManualFixture(iqRunnerSettings())
			repo.identity, repo.claimErr = tc.identity, tc.claimErr
			_, err := svc.Recheck(context.Background(), tc.group, 99, tc.limit)
			require.Equal(t, tc.wantStatus, infraerrors.Code(err))
			if tc.claimErr == nil {
				require.Zero(t, repo.manualClaims)
			}
			require.Zero(t, repo.reserveCalls)
			svc.Stop()
		})
	}
}

func TestOpenAIIQManualRunsWhileSchedulerDisabledAndUsesBudget(t *testing.T) {
	settings := iqRunnerSettings()
	settings.Enabled = false
	svc, repo, transport := newIQManualFixture(settings)
	ctx, cancel := context.WithCancel(context.Background())
	runID, err := svc.Recheck(ctx, 7, 99, settings.PerRequestLimitUSD)
	require.NoError(t, err)
	require.Equal(t, int64(123), runID)
	cancel() // The accepted run must not depend on the HTTP request lifetime.
	waitIQScheduling(t, transport.entered, "manual request")
	require.Equal(t, int64(7), repo.groupID)
	require.Equal(t, int64(99), repo.requestedBy)
	repo.mu.Lock()
	require.Equal(t, 1, repo.reserveCalls)
	require.Len(t, repo.boundRequests, 1)
	repo.mu.Unlock()
	_, err = svc.Recheck(context.Background(), 7, 99, settings.PerRequestLimitUSD)
	require.Equal(t, http.StatusConflict, infraerrors.Code(err))
	close(transport.release)
	svc.Stop()
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, 1, repo.finishCalls)
	require.Len(t, repo.completed, 1)
}

func TestOpenAIIQManualStopWaitsForFinalization(t *testing.T) {
	settings := iqRunnerSettings()
	svc, repo, transport := newIQManualFixture(settings)
	finishGate := make(chan struct{})
	repo.finishStart, repo.finishGate = make(chan struct{}), finishGate
	_, err := svc.Recheck(context.Background(), 7, 99, settings.PerRequestLimitUSD)
	require.NoError(t, err)
	waitIQScheduling(t, transport.entered, "manual request")
	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	waitIQScheduling(t, repo.finishStart, "manual finalization")
	select {
	case <-stopped:
		t.Fatal("Stop returned before the manual result was finalized")
	case <-time.After(50 * time.Millisecond):
	}
	close(finishGate)
	waitIQScheduling(t, stopped, "service shutdown")
	_, err = svc.Recheck(context.Background(), 7, 99, settings.PerRequestLimitUSD)
	require.Equal(t, http.StatusServiceUnavailable, infraerrors.Code(err))
}

func TestOpenAIIQManualStopWaitsForAdmissionAndFinishesClaim(t *testing.T) {
	settings := iqRunnerSettings()
	svc, repo, _ := newIQManualFixture(settings)
	claimGate := make(chan struct{})
	repo.claimStart, repo.claimGate = make(chan struct{}), claimGate
	result := make(chan error, 1)
	go func() {
		_, err := svc.Recheck(context.Background(), 7, 99, settings.PerRequestLimitUSD)
		result <- err
	}()
	waitIQScheduling(t, repo.claimStart, "manual claim")
	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("Stop returned while a manual claim was in flight")
	case <-time.After(50 * time.Millisecond):
	}
	close(claimGate)
	err := <-result
	require.Equal(t, http.StatusServiceUnavailable, infraerrors.Code(err))
	waitIQScheduling(t, stopped, "service shutdown")
	repo.mu.Lock()
	defer repo.mu.Unlock()
	require.Equal(t, 1, repo.finishCalls)
	require.Zero(t, repo.reserveCalls)
}

func TestIQManualClaimErrorPreservesUnexpectedErrors(t *testing.T) {
	err := errors.New("unexpected")
	require.ErrorIs(t, iqManualClaimError(err), err)
}

func TestOpenAIIQExpiredClaimBudgetDoesNotDispatch(t *testing.T) {
	settings := iqRunnerSettings()
	svc, repo, transport := newIQManualFixture(settings)
	run := &IQRun{ID: 123, Trigger: IQTriggerManual, Question: settings.Question, Answer: settings.ExpectedAnswer, Model: settings.Model}
	err := svc.executeIQRun(context.Background(), run, settings, 12, time.Now().Add(-time.Second))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	repo.mu.Lock()
	require.Equal(t, 1, repo.finishCalls)
	require.Zero(t, repo.reserveCalls)
	repo.mu.Unlock()
	select {
	case <-transport.entered:
		t.Fatal("an expired claim must not dispatch a request")
	default:
	}
	svc.Stop()
}
