//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountCatalogUnknownPricesDoNotBecomeFreeCards(t *testing.T) {
	const model = "private-unpriced-fixture"
	for _, tc := range []struct {
		name      string
		cards     []ChannelModelPricing
		wantPrice bool
	}{
		{name: "no rule"},
		{name: "empty wildcard rule", cards: []ChannelModelPricing{{Models: []string{"*"}, BillingMode: BillingModeToken}}},
		{name: "explicit free rule", cards: []ChannelModelPricing{{Models: []string{"*"}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(0), OutputPrice: testPtrFloat64(0)}}, wantPrice: true},
		{name: "explicit priced rule", cards: []ChannelModelPricing{{Models: []string{model}, BillingMode: BillingModeToken, InputPrice: testPtrFloat64(2e-6), OutputPrice: testPtrFloat64(10e-6)}}, wantPrice: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			billing := newTestBillingService()
			svc := &ModelPlazaService{billingService: billing, resolver: NewModelPricingResolver(nil, billing)}
			group := &Group{ID: 1, Platform: PlatformOpenAI, ModelPricing: tc.cards}
			entry := PlazaModel{Name: model, Platform: PlatformOpenAI}
			svc.fillDisplayPricing(context.Background(), &entry, group)
			if !tc.wantPrice {
				require.Nil(t, entry.Pricing)
			} else {
				require.NotNil(t, entry.Pricing)
				require.NotNil(t, entry.Pricing.InputPrice)
				require.Equal(t, *tc.cards[0].InputPrice, *entry.Pricing.InputPrice)
			}
		})
	}
}

func TestAccountCatalogScheduleDistinguishesMissingAndExplicitZero(t *testing.T) {
	zero := 0.0
	positive := 2e-6
	require.False(t, plazaScheduleHasPrice(nil, &ContextPricingSchedule{Tiers: []ContextPricingTier{{Input: &zero, Output: &zero}}}))
	require.False(t, plazaScheduleHasPrice(&ChannelModelPricing{}, nil))
	require.True(t, plazaScheduleHasPrice(&ChannelModelPricing{InputPrice: &zero}, nil))
	require.True(t, plazaScheduleHasPrice(&ChannelModelPricing{Intervals: []PricingInterval{{InputPrice: &zero}}}, nil))
	require.True(t, plazaScheduleHasPrice(nil, &ContextPricingSchedule{Tiers: []ContextPricingTier{{Input: &positive}}}))
}

func TestAccountCatalogEmptyChannelCardDoesNotInventPrice(t *testing.T) {
	const model = "private-unpriced-fixture"
	channel := Channel{
		ID: 1, Name: "catalog", Status: StatusActive, GroupIDs: []int64{10},
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"*"}, BillingMode: BillingModeToken}},
	}
	group := Group{ID: 10, Platform: PlatformOpenAI}
	channelService := newAvailableChannelService([]Channel{channel}, &stubGroupRepoForAvailable{activeGroups: []Group{group}})
	billing := newTestBillingService()
	svc := &ModelPlazaService{billingService: billing, resolver: NewModelPricingResolver(channelService, billing)}
	entry := PlazaModel{Name: model, Platform: PlatformOpenAI, Pricing: &channel.ModelPricing[0]}
	svc.fillDisplayPricing(context.Background(), &entry, &group)
	// An empty card may retain non-price display metadata. It must have no
	// synthesized zero price pointers that would make it look free.
	require.True(t, pricingNeedsFallback(entry.Pricing))
	require.Nil(t, channel.ModelPricing[0].InputPrice, "display lookup must not populate persisted prices")
}

func TestAccountCatalogPlazaAddsAliasWithoutGuessingTargetPrice(t *testing.T) {
	group := Group{ID: 10, Platform: PlatformOpenAI, ModelAllowlist: GroupModelAllowlist{Enabled: true, Models: []string{"private-public-alias"}}}
	channel := Channel{ID: 1, Name: "catalog", Status: StatusActive, GroupIDs: []int64{10}, RestrictModels: true,
		ModelMapping: map[string]map[string]string{PlatformOpenAI: {"private-public-alias": "mapped"}},
		ModelPricing: []ChannelModelPricing{{Platform: PlatformOpenAI, Models: []string{"mapped"}, InputPrice: testPtrFloat64(1e-6)}}}
	account := catalogAccount(1, PlatformOpenAI, []int64{10}, map[string]any{"mapped": "private-upstream"}, nil)
	svc := newPlazaService([]Channel{channel}, []Group{group}, nil)
	svc.modelCatalog = NewAccountModelCatalog(&catalogAccountRepositoryStub{accounts: []Account{account}})
	svc.billingService = newTestBillingService()
	channelService := newAvailableChannelService([]Channel{channel}, &stubGroupRepoForAvailable{activeGroups: []Group{group}})
	svc.resolver = NewModelPricingResolver(channelService, svc.billingService)
	groups, err := svc.ListGroups(context.Background())
	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.Len(t, groups[0].Models, 1)
	require.Equal(t, "private-public-alias", groups[0].Models[0].Name)
	require.Nil(t, groups[0].Models[0].Pricing)
	require.Equal(t, 1e-6, *channel.ModelPricing[0].InputPrice)
}
