package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestNormalizeUpstreamAttemptKeepsNoUsageRowsWithoutFreeformMetadata(t *testing.T) {
	attempt := &UpstreamAttempt{
		AttemptID:       "attempt-1",
		AccountID:       1,
		Protocol:        "responses",
		Source:          "request",
		Outcome:         "failed",
		Model:           "untrusted model with spaces",
		ServiceTier:     "tier\nwith-body-like-content",
		ReasoningEffort: "high\nignored",
	}

	require.NoError(t, NormalizeUpstreamAttempt(attempt))
	require.Equal(t, "", attempt.Model)
	require.Equal(t, "", attempt.ServiceTier)
	require.Equal(t, "", attempt.ReasoningEffort)
	require.Equal(t, "no_usage", attempt.CostStatus)
	require.Nil(t, attempt.CostUSD)
	require.False(t, attempt.CreatedAt.IsZero())
}

func TestNormalizeUpstreamAttemptPreservesKnownZeroCost(t *testing.T) {
	zero := 0.0
	attempt := &UpstreamAttempt{
		AttemptID:    "attempt-2",
		CreatedAt:    time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC),
		AccountID:    1,
		Protocol:     "responses",
		Source:       "request",
		Outcome:      "success",
		UsagePresent: true,
		CostUSD:      &zero,
	}

	require.NoError(t, NormalizeUpstreamAttempt(attempt))
	require.Equal(t, "known", attempt.CostStatus)
	require.Equal(t, 0.0, *attempt.CostUSD)
}

func TestNormalizeUpstreamAttemptRejectsInvalidIdentity(t *testing.T) {
	attempt := &UpstreamAttempt{
		AttemptID: "attempt with spaces",
		AccountID: 1,
		Protocol:  "responses",
		Source:    "request",
		Outcome:   "unknown",
	}

	require.Error(t, NormalizeUpstreamAttempt(attempt))
}

func TestUpstreamAttemptPriceWithoutSnapshotIsUnknownNotFree(t *testing.T) {
	in := int64(12)
	attempt := &UpstreamAttempt{
		AttemptID:    "attempt-3",
		AccountID:    1,
		Protocol:     "responses",
		Source:       "request",
		Outcome:      "success",
		Model:        "gpt-5",
		UsagePresent: true,
		InputTokens:  &in,
	}

	require.NoError(t, NewUpstreamAttemptCostCalculator(nil, nil, nil).Price(context.Background(), attempt))
	require.Nil(t, attempt.CostUSD)
	require.Equal(t, "unknown_pricing", attempt.CostStatus)
}

func TestUpstreamAttemptPriceWithoutRecognizedUsageIsUnknownNotFree(t *testing.T) {
	attempt := &UpstreamAttempt{
		AttemptID:             "attempt-4",
		AccountID:             1,
		Protocol:              "responses",
		Source:                "request",
		Outcome:               "success",
		Model:                 "gpt-5",
		UsagePresent:          true,
		CostSnapshotAvailable: true,
	}

	require.NoError(t, NewUpstreamAttemptCostCalculator(nil, nil, nil).Price(context.Background(), attempt))
	require.Nil(t, attempt.CostUSD)
	require.Equal(t, "unknown_pricing", attempt.CostStatus)
}

func TestUpstreamAttemptPriceRecognizesCacheOnlyUsage(t *testing.T) {
	cacheRead := int64(1000)
	billing := NewBillingService(&config.Config{}, nil)
	attempt := &UpstreamAttempt{
		AttemptID:             "attempt-5",
		AccountID:             1,
		Protocol:              "responses",
		Source:                "request",
		Outcome:               "success",
		Model:                 "claude-sonnet-4",
		UsagePresent:          true,
		CacheReadTokens:       &cacheRead,
		AccountRateMultiplier: 1,
		CostSnapshotAvailable: true,
		pricingSnapshot:       NewModelPricingResolver(nil, billing).Resolve(context.Background(), PricingInput{Model: "claude-sonnet-4"}),
	}

	calculator := NewUpstreamAttemptCostCalculator(nil, nil, billing)
	require.NoError(t, calculator.Price(context.Background(), attempt))
	require.NotNil(t, attempt.CostUSD)
	require.Greater(t, *attempt.CostUSD, 0.0)
	require.Equal(t, "known", attempt.CostStatus)
}

func TestUpstreamAttemptPriceUnknownModelFailsClosed(t *testing.T) {
	in := int64(1)
	attempt := &UpstreamAttempt{
		AttemptID:             "attempt-6",
		AccountID:             1,
		Protocol:              "responses",
		Source:                "request",
		Outcome:               "success",
		Model:                 "unpriced-model-000000",
		UsagePresent:          true,
		InputTokens:           &in,
		AccountRateMultiplier: 1,
		CostSnapshotAvailable: true,
	}

	calculator := NewUpstreamAttemptCostCalculator(nil, nil, NewBillingService(&config.Config{}, nil))
	require.NoError(t, calculator.Price(context.Background(), attempt))
	require.Nil(t, attempt.CostUSD)
	require.Equal(t, "unknown_pricing", attempt.CostStatus)
}
