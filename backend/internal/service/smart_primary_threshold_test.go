package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type primaryThresholdAdmin struct {
	smartSchedulerAdminServiceStub
	minimum float64
}

func (s *primaryThresholdAdmin) GetSmartStickyPolicy(context.Context, int64) (SmartStickyPolicy, error) {
	p := RecommendedSmartStickyPolicy()
	p.PrimaryMinScore = s.minimum
	return p, nil
}

func TestPrimaryThresholdPreviewRoutingAndCacheAgree(t *testing.T) {
	group := Group{ID: 7, Platform: PlatformOpenAI, SmartSchedulerEnabled: true}
	accounts := []*Account{smartSchedulerTestAccount(1, 1), smartSchedulerTestAccount(2, 100)}
	admin := &primaryThresholdAdmin{smartSchedulerAdminServiceStub: smartSchedulerAdminServiceStub{group: group, accounts: []Account{*accounts[0], *accounts[1]}}}
	stats := &smartSchedulerStatsStub{quality: map[int64]AccountQualityStats{1: smartSchedulerTestQuality(35), 2: smartSchedulerTestQuality(95)}}
	svc := &SmartSchedulerPreviewService{adminService: admin, dashboardService: stats}
	ctx := withSmartSchedulerStableOrdering(context.Background())
	now := time.Now()
	first, err := svc.OrderCandidates(ctx, &group, "", "any", accounts, now)
	require.NoError(t, err)
	require.Equal(t, "primary", first.ItemByAccountID[1].Pool)
	admin.minimum = 100
	next, err := svc.OrderCandidates(ctx, &group, "", "any", accounts, now)
	require.NoError(t, err)
	require.Equal(t, "warm", next.ItemByAccountID[1].Pool)
	require.Len(t, applySmartSchedulerOrderingToAccounts(accounts, next), 2)
	preview, err := svc.Preview(ctx, group.ID, "", "any", now)
	require.NoError(t, err)
	for _, item := range preview.Items {
		require.Equal(t, next.ItemByAccountID[item.AccountID].Pool, item.Pool)
	}
}

func TestPrimaryThresholdUsesAdjustedScoreAndPreservesFallback(t *testing.T) {
	items := []SmartSchedulerPreviewItem{
		{AccountID: 1, Pool: "primary", RawScore: previewFloat64Ptr(60), Confidence: 1},
		{AccountID: 2, Pool: "primary", RawScore: previewFloat64Ptr(80), Confidence: 1},
		{AccountID: 3, Pool: "isolated"},
	}
	applySmartSchedulerConfidenceAdjustment(items)
	applySmartSchedulerPrimaryMinScore(items, 80)
	require.Equal(t, "warm", items[0].Pool)
	require.Equal(t, "primary", items[1].Pool)
	require.Equal(t, "isolated", items[2].Pool)
	applySmartSchedulerPrimaryMinScore(items, 90)
	require.Equal(t, "warm", items[1].Pool)
	sortSmartSchedulerItems(items)
	require.Equal(t, int64(2), items[0].AccountID)
}

func TestPrimaryThresholdDisabledAndValidation(t *testing.T) {
	items := []SmartSchedulerPreviewItem{{Pool: "primary", Score: previewFloat64Ptr(10)}}
	applySmartSchedulerPrimaryMinScore(items, 0)
	require.Equal(t, "primary", items[0].Pool)
	for _, value := range []float64{-1, 101, math.NaN(), math.Inf(1)} {
		policy := RecommendedSmartStickyPolicy()
		policy.PrimaryMinScore = value
		_, err := policy.Normalize()
		require.Error(t, err)
	}
}

func TestPrimaryThresholdStickyMovesToQualifiedCandidate(t *testing.T) {
	policy := RecommendedSmartStickyPolicy()
	policy.PrimaryMinScore = 80
	ordering := &SmartSchedulerOrdering{Active: true, OrderedAccountIDs: []int64{2, 1}, ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
		1: {AccountID: 1, Pool: "warm", Score: previewFloat64Ptr(79)},
		2: {AccountID: 2, Pool: "primary", Score: previewFloat64Ptr(80)},
	}}
	decision := decideOpenAISmartStickyReviewWithPolicy(ordering, 1, policy)
	require.True(t, decision.Switch)
	require.Equal(t, "primary_over_warm", decision.Reason)
}
