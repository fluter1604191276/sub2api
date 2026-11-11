//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func channelMonitorV3ProfitGroup(id int64, platform string) *Group {
	return &Group{ID: id, Platform: platform, Status: StatusActive, Hydrated: true, RateMultiplier: 0.5,
		ProfitControlEnabled: true, SubscriptionType: SubscriptionTypeStandard}
}

func trustedChannelMonitorV3Context(group *Group) context.Context {
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	return context.WithValue(ctx, channelMonitorV3RequestContextKey{}, true)
}

func profitGate(ctx context.Context) *openAIProfitControlGate {
	gate, _ := ctx.Value(openAIProfitControlGateCtxKey{}).(*openAIProfitControlGate)
	return gate
}

func TestChannelMonitorV3ProfitControlBypassesOnlyTrustedProbeContext(t *testing.T) {
	for _, tc := range []struct {
		name    string
		openai  bool
		gateway bool
	}{
		{"openai", true, false},
		{"gateway", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := channelMonitorV3ProfitGroup(71, PlatformOpenAI)
			groupID := group.ID
			ordinary := context.WithValue(context.Background(), ctxkey.Group, group)
			trusted := trustedChannelMonitorV3Context(group)
			var normal, bypassed context.Context
			if tc.openai {
				svc := &OpenAIGatewayService{}
				normal = svc.withOpenAIProfitControlGate(ordinary, &groupID)
				bypassed = svc.withOpenAIProfitControlGate(trusted, &groupID)
			} else {
				svc := &GatewayService{}
				ordinary, _ = WithGatewayTokenRequestPricing(ordinary)
				trusted, _ = WithGatewayTokenRequestPricing(trusted)
				normal = svc.withGatewayProfitControlGate(ordinary, &groupID)
				bypassed = svc.withGatewayProfitControlGate(trusted, &groupID)
			}
			require.NotNil(t, profitGate(normal), "ordinary user request must retain the revenue gate")
			require.Nil(t, profitGate(bypassed), "trusted V3 probe must bypass the revenue gate")
		})
	}
}

func TestChannelMonitorV3ProfitControlClearsExistingGateAndDoesNotTrustHeaders(t *testing.T) {
	group := channelMonitorV3ProfitGroup(72, PlatformOpenAI)
	groupID := group.ID
	existing := context.WithValue(trustedChannelMonitorV3Context(group), openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: groupID})
	cleared := (&OpenAIGatewayService{}).withOpenAIProfitControlGate(existing, &groupID)
	require.Nil(t, profitGate(cleared))

	// A client-supplied header never creates the private context marker.
	spoofed := context.WithValue(context.WithValue(context.Background(), ctxkey.Group, group), channelMonitorV3RequestHeader, "trusted-monitor-token")
	require.False(t, IsChannelMonitorV3Request(spoofed))
	kept := (&OpenAIGatewayService{}).withOpenAIProfitControlGate(spoofed, &groupID)
	require.NotNil(t, profitGate(kept))
}

func TestChannelMonitorV3ProfitControlDoesNotReattachAtTurnOrSelectionBoundary(t *testing.T) {
	group := channelMonitorV3ProfitGroup(73, PlatformOpenAI)
	groupID := group.ID
	trusted := trustedChannelMonitorV3Context(group)
	trusted = context.WithValue(trusted, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: groupID})
	turnCtx, _ := (&OpenAIGatewayService{}).WithOpenAITurnPricingContext(trusted, &groupID)
	require.Nil(t, profitGate(turnCtx))
	selection := &AccountSelectionResult{profitGate: &openAIProfitControlGate{groupID: groupID}}
	require.Nil(t, profitGate(ContextWithSelectionProfitGate(turnCtx, selection)))
}

func TestChannelMonitorV3SelectionRestoresStickyTraceWhileClearingProfitGate(t *testing.T) {
	group := channelMonitorV3ProfitGroup(74, PlatformOpenAI)
	groupID := group.ID
	trace := &smartStickySwitchTrace{pending: &smartStickySwitchPending{}}
	selection := &AccountSelectionResult{
		Account:           &Account{ID: 901},
		profitGate:        &openAIProfitControlGate{groupID: groupID},
		stickySwitchTrace: trace,
	}

	ctx := ContextWithSelectionProfitGate(trustedChannelMonitorV3Context(group), selection)
	require.Nil(t, profitGate(ctx), "trusted V3 monitor requests must not regain a profit gate")
	require.Same(t, trace, ctx.Value(smartStickySwitchTraceContextKey{}), "the selection trace must survive the V3 gate bypass")

	trace.mu.Lock()
	defer trace.mu.Unlock()
	require.Equal(t, int64(901), trace.pending.selectedAccountID, "the restored trace must bind to the selected account")
}
