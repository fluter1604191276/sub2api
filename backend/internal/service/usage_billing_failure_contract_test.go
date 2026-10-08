//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// These are synthetic repository failures, not real transactions. The contract
// deliberately preserves error propagation and does not introduce billing retry.
func TestUsageBillingFailureContract(t *testing.T) {
	failures := []struct {
		name string
		err  error
	}{
		{"context_canceled", context.Canceled},
		{"deadline_exceeded", context.DeadlineExceeded},
		{"postgres_query_canceled", &pq.Error{Code: "57014", Message: "canceling statement due to user request"}},
	}
	for _, gateway := range []string{"gateway", "openai"} {
		for _, failure := range failures {
			for _, logFailure := range []bool{false, true} {
				name := gateway + "/" + failure.name + "/log_saved"
				if logFailure {
					name = gateway + "/" + failure.name + "/log_fallback_failed"
				}
				t.Run(name, func(t *testing.T) {
					billingRepo := &openAIRecordUsageBillingRepoStub{err: failure.err}
					usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
					if logFailure {
						usageRepo.bestEffortErr = context.DeadlineExceeded
						usageRepo.createErr = context.DeadlineExceeded
					}
					userRepo := &openAIRecordUsageUserRepoStub{}
					subRepo := &openAIRecordUsageSubRepoStub{}
					ctx, cancel := context.WithCancel(context.Background())
					cancel() // Request cancellation must not poison the detached billing context.
					var err error
					if gateway == "gateway" {
						svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo)
						err = svc.RecordUsage(ctx, &RecordUsageInput{
							Result: &ForwardResult{RequestID: "synthetic-billing-failure", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 10, OutputTokens: 6}, Duration: time.Second},
							APIKey: &APIKey{ID: 1}, User: &User{ID: 1}, Account: &Account{ID: 1},
						})
					} else {
						svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
						err = svc.RecordUsage(ctx, &OpenAIRecordUsageInput{
							Result: &OpenAIForwardResult{RequestID: "synthetic-billing-failure", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 6}, Duration: time.Second},
							APIKey: &APIKey{ID: 1}, User: &User{ID: 1}, Account: &Account{ID: 1},
						})
					}
					require.ErrorIs(t, err, failure.err, "logging failure must not hide the original billing failure")
					require.Equal(t, 1, billingRepo.calls, "no automatic repeated Apply or legacy deduction")
					require.NoError(t, billingRepo.lastCtxErr)
					require.Zero(t, userRepo.deductCalls)
					require.Zero(t, subRepo.incrementCalls)
					require.Equal(t, 1, usageRepo.bestEffortCalls)
					if logFailure {
						require.Equal(t, 1, usageRepo.createCalls, "exactly one synchronous log fallback")
					} else {
						require.Zero(t, usageRepo.createCalls)
					}
					require.NoError(t, usageRepo.lastCtxErr)
					require.NotNil(t, usageRepo.lastLog)
					require.Equal(t, 10, usageRepo.lastLog.InputTokens)
					require.Equal(t, 6, usageRepo.lastLog.OutputTokens)
					require.Positive(t, usageRepo.lastLog.TotalCost)
					require.Zero(t, usageRepo.lastLog.ActualCost, "unsettled log does not claim successful billing")
				})
			}
		}
	}
}

func TestUsageBillingFailureWorkerExecutesOnce(t *testing.T) {
	pool := &UsageRecordWorkerPool{taskTimeout: time.Second}
	billingRepo := &openAIRecordUsageBillingRepoStub{err: context.DeadlineExceeded}
	usageRepo := &openAIRecordUsageBestEffortLogRepoStub{}
	svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, &openAIRecordUsageUserRepoStub{}, &openAIRecordUsageSubRepoStub{})
	calls := 0
	var observed error
	pool.execute(func(ctx context.Context) {
		calls++
		require.NoError(t, ctx.Err())
		// Worker tasks return no error; handlers observe and log the failure.
		observed = svc.RecordUsage(ctx, &RecordUsageInput{
			Result: &ForwardResult{RequestID: "synthetic-worker-failure", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 10, OutputTokens: 6}},
			APIKey: &APIKey{ID: 1}, User: &User{ID: 1}, Account: &Account{ID: 1},
		})
	})
	require.ErrorIs(t, observed, context.DeadlineExceeded)
	require.Equal(t, 1, calls)
	require.Equal(t, 1, billingRepo.calls)
	require.Zero(t, usageRepo.lastLog.ActualCost)
}

func TestUsageBillingFailureLogFallbackDoesNotRepeatCommittedBilling(t *testing.T) {
	for _, gateway := range []string{"gateway", "openai"} {
		t.Run(gateway, func(t *testing.T) {
			billingRepo := &openAIRecordUsageBillingRepoStub{result: &UsageBillingApplyResult{Applied: true}}
			usageRepo := &openAIRecordUsageBestEffortLogRepoStub{bestEffortErr: context.DeadlineExceeded, createErr: context.DeadlineExceeded}
			userRepo := &openAIRecordUsageUserRepoStub{}
			subRepo := &openAIRecordUsageSubRepoStub{}
			var err error
			if gateway == "gateway" {
				svc := newGatewayRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo)
				err = svc.RecordUsage(context.Background(), &RecordUsageInput{
					Result: &ForwardResult{RequestID: "synthetic-committed-billing", Model: "claude-sonnet-4", Usage: ClaudeUsage{InputTokens: 10, OutputTokens: 6}},
					APIKey: &APIKey{ID: 1}, User: &User{ID: 1}, Account: &Account{ID: 1},
				})
			} else {
				svc := newOpenAIRecordUsageServiceWithBillingRepoForTest(usageRepo, billingRepo, userRepo, subRepo, nil)
				err = svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
					Result: &OpenAIForwardResult{RequestID: "synthetic-committed-billing", Model: "gpt-5.1", Usage: OpenAIUsage{InputTokens: 10, OutputTokens: 6}},
					APIKey: &APIKey{ID: 1}, User: &User{ID: 1}, Account: &Account{ID: 1},
				})
			}
			require.NoError(t, err, "current contract treats usage log persistence as best effort after billing")
			require.Equal(t, 1, billingRepo.calls)
			require.Zero(t, userRepo.deductCalls)
			require.Zero(t, subRepo.incrementCalls)
			require.Equal(t, 1, usageRepo.bestEffortCalls)
			require.Equal(t, 1, usageRepo.createCalls)
			require.Positive(t, usageRepo.lastLog.ActualCost)
		})
	}
}
