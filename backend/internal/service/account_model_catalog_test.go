//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type catalogAccountRepositoryStub struct {
	AccountRepository
	accounts []Account
	calls    int
}

func (s *catalogAccountRepositoryStub) ListModelAvailabilityCandidates(_ context.Context, groupID *int64, _ []string, includeGrouped bool) ([]Account, error) {
	s.calls++
	if groupID != nil || !includeGrouped {
		return nil, nil
	}
	return s.accounts, nil
}

func catalogAccount(id int64, platform string, groups []int64, mapping map[string]any, snapshot []string) Account {
	return Account{
		ID:          id,
		Platform:    platform,
		Status:      StatusActive,
		Schedulable: true,
		GroupIDs:    groups,
		Credentials: map[string]any{"model_mapping": mapping},
		Extra:       map[string]any{AccountAvailableModelsExtraKey: snapshot},
	}
}

func TestAccountModelCatalogLoadPersistsConfiguredAvailability(t *testing.T) {
	until := time.Now().Add(time.Hour)
	available := catalogAccount(1, PlatformOpenAI, []int64{10}, map[string]any{"gpt-6.1-sol": "gpt-6.1-sol"}, []string{"gpt-6.1-sol"})
	available.RateLimitResetAt = &until // temporary availability must not hide the configured catalogue
	disabled := catalogAccount(2, PlatformOpenAI, []int64{10}, map[string]any{"old": "old"}, []string{"old"})
	disabled.Status = "disabled"
	stub := &catalogAccountRepositoryStub{accounts: []Account{available, disabled}}
	catalog := NewAccountModelCatalog(stub)

	loaded, err := catalog.Load(context.Background(), []Group{{ID: 10, Platform: PlatformOpenAI}})
	require.NoError(t, err)
	require.Equal(t, 1, stub.calls)
	require.Len(t, loaded[10], 1)
	require.Equal(t, int64(1), loaded[10][0].ID)
	require.Equal(t, []SupportedModel{{Name: "gpt-6.1-sol", Platform: PlatformOpenAI}}, catalog.Project([]Group{{ID: 10, Platform: PlatformOpenAI}}, loaded)[10])
}

func TestAccountModelCatalogUsesFiniteEvidenceAndPassthroughSnapshot(t *testing.T) {
	wildcard := catalogAccount(1, PlatformOpenAI, []int64{10}, map[string]any{"gpt-*": "gpt-*"}, []string{"gpt-6.1-sol"})
	passthrough := catalogAccount(2, PlatformOpenAI, []int64{10}, map[string]any{"stale-alias": "gpt-old"}, []string{"gpt-6.1-codex"})
	passthrough.Extra["openai_passthrough"] = true
	empty := catalogAccount(3, PlatformOpenAI, []int64{10}, nil, nil)
	catalog := NewAccountModelCatalog(nil)
	models := catalog.Project([]Group{{ID: 10, Platform: PlatformOpenAI}}, map[int64][]Account{10: {wildcard, passthrough, empty}})[10]
	require.Equal(t, []SupportedModel{
		{Name: "gpt-6.1-codex", Platform: PlatformOpenAI},
		{Name: "gpt-6.1-sol", Platform: PlatformOpenAI},
	}, models)
}

func TestAccountModelCatalogCompositeExactRoutesAndOwnership(t *testing.T) {
	group := &Group{ID: 10, Platform: PlatformComposite}
	channel := &Channel{
		GroupIDs:           []int64{10},
		RestrictModels:     true,
		BillingModelSource: BillingModelSourceRequested,
		ModelPricing:       []ChannelModelPricing{{Platform: PlatformDeepseek, Models: []string{"deepseek-upstream"}}},
	}
	deepseek := catalogAccount(1, PlatformDeepseek, []int64{10}, map[string]any{"deepseek-upstream": "deepseek-v4-pro"}, nil)
	openai := catalogAccount(2, PlatformOpenAI, []int64{10}, map[string]any{"ambiguous": "gpt-6.1-sol"}, nil)
	other := catalogAccount(3, PlatformDeepseek, []int64{10}, map[string]any{"ambiguous": "deepseek-v4-pro"}, nil)
	routes := compositeRouteRepoStub{routes: []CompositeModelRoute{{
		ID: 1, GroupID: 10, Enabled: true, MatchType: CompositeRouteMatchExact,
		Endpoint: CompositeRouteEndpointAny, PublicModel: "route-only", TargetPlatform: PlatformDeepseek, UpstreamModel: "deepseek-upstream",
	}}}
	catalog := NewAccountModelCatalog(nil)
	catalog.SetCompositeRouteResolver(NewCompositeRouteResolver(routes))
	models, err := catalog.ProjectChannel(context.Background(), channel, group, []Account{deepseek, openai, other})
	require.NoError(t, err)
	var alias *SupportedModel
	for i := range models {
		require.NotEqual(t, "ambiguous", models[i].Name)
		if models[i].Name == "route-only" {
			alias = &models[i]
		}
	}
	require.NotNil(t, alias)
	require.Equal(t, PlatformDeepseek, alias.Platform)
	require.Nil(t, alias.Pricing, "a target rule must not invent an unknown public alias price")
}

func TestAccountModelCatalogRestrictionUsesBillingModelWithoutGuessingAliasPrice(t *testing.T) {
	account := catalogAccount(1, PlatformOpenAI, []int64{10}, map[string]any{"mapped": "private-upstream"}, nil)
	group := &Group{ID: 10, Platform: PlatformOpenAI, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"public-alias"}}}
	for _, tc := range []struct {
		source, priced string
		visible        bool
	}{
		{BillingModelSourceChannelMapped, "mapped", true},
		{BillingModelSourceChannelMapped, "public-alias", false},
		{BillingModelSourceRequested, "public-alias", true},
		{BillingModelSourceRequested, "mapped", false},
		{BillingModelSourceUpstream, "private-upstream", true},
		{BillingModelSourceResponse, "mapped", true},
	} {
		t.Run(tc.source+"/"+tc.priced, func(t *testing.T) {
			channel := &Channel{GroupIDs: []int64{10}, RestrictModels: true, BillingModelSource: tc.source,
				ModelMapping: map[string]map[string]string{PlatformOpenAI: {"public-alias": "mapped"}},
				ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{tc.priced}, InputPrice: testPtrFloat64(1e-6)}}}
			models, err := NewAccountModelCatalog(nil).ProjectChannel(context.Background(), channel, group, []Account{account})
			require.NoError(t, err)
			if !tc.visible {
				require.Empty(t, models)
				return
			}
			require.Len(t, models, 1)
			require.Equal(t, "public-alias", models[0].Name)
			if tc.priced != "public-alias" {
				require.Nil(t, models[0].Pricing)
			}
		})
	}
}

func TestAccountModelCatalogCompositeRoutePrecedenceAndEndpoints(t *testing.T) {
	group := &Group{ID: 10, Platform: PlatformComposite, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"routed", "image-alias", "family-model", "gpt-6.1-sol"}}}
	channel := &Channel{GroupIDs: []int64{10}}
	accounts := []Account{
		catalogAccount(1, PlatformOpenAI, []int64{10}, map[string]any{"routed": "gpt-6.1-sol", "family-model": "gpt-6.1-sol"}, []string{"gpt-6.1-sol"}),
		catalogAccount(2, PlatformDeepseek, []int64{10}, map[string]any{"deepseek-target": "deepseek-v4-pro"}, nil),
		catalogAccount(3, PlatformOpenAI, []int64{10}, nil, []string{"gpt-6.1-sol"}),
	}
	routes := []CompositeModelRoute{
		{GroupID: 10, Enabled: true, MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointAny, PublicModel: "routed", TargetPlatform: PlatformDeepseek, UpstreamModel: "deepseek-target"},
		{GroupID: 10, Enabled: true, MatchType: CompositeRouteMatchExact, Endpoint: CompositeRouteEndpointImages, PublicModel: "image-alias", TargetPlatform: PlatformOpenAI, UpstreamModel: "routed"},
		{GroupID: 10, Enabled: true, MatchType: CompositeRouteMatchPrefix, Endpoint: CompositeRouteEndpointAny, PublicModel: "family-", TargetPlatform: PlatformDeepseek, UpstreamModel: "deepseek-target"},
	}
	catalog := NewAccountModelCatalog(nil)
	catalog.SetCompositeRouteResolver(NewCompositeRouteResolver(compositeRouteRepoStub{routes: routes}))
	models, err := catalog.ProjectChannel(context.Background(), channel, group, accounts)
	require.NoError(t, err)
	want := []SupportedModel{{Name: "gpt-6.1-sol", Platform: PlatformOpenAI}, {Name: "image-alias", Platform: PlatformOpenAI}, {Name: "family-model", Platform: PlatformDeepseek}, {Name: "routed", Platform: PlatformDeepseek}}
	require.ElementsMatch(t, want, models, "explicit routing overrides ownership; all configured endpoints and finite prefix candidates survive")
}
