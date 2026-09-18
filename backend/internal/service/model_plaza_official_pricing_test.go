//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDomesticOfficialPricingVerifiedReferences(t *testing.T) {
	svc := &ModelPlazaService{billingService: newTestBillingService()}
	for _, tc := range []struct {
		model                string
		input, output, cache float64
	}{
		{"glm-5.2", 8, 28, 2}, {"glm-5.3", 8, 28, 2},
		{"glm-5.3-flash", .8, 2.8, .23}, {"glm-5.1", 6, 24, 1.3},
		{"deepseek-flash", 2, 8, .04}, {"deepseek-v4.1-flash", 2, 8, .04},
		{"deepseek-v4.1-flash-0910", 2, 8, .04},
		{"deepseek-v4-flash", 2, 8, .04}, {"deepseek-v4-flash-0731", 2, 8, .04},
		{"deepseek-v4-flash-vision-exp", 2, 8, .04},
		{"deepseek-v4-pro", 9, 27, .30},
		{"deepseek-v4-pro-0813", 9, 27, .30},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := svc.lookupOfficialPricing(context.Background(), tc.model, map[string]*PlazaOfficialPricing{})
			require.NotNil(t, got)
			require.Equal(t, "CNY", got.Currency)
			require.InDelta(t, tc.input, *got.InputPrice*1e6, 1e-10)
			require.InDelta(t, tc.output, *got.OutputPrice*1e6, 1e-10)
			require.InDelta(t, tc.cache, *got.CacheReadPrice*1e6, 1e-10)
			require.Contains(t, got.SourceURL, "https://")
			require.Equal(t, "2026-09-18", got.VerifiedAt)
		})
	}
}

func TestDomesticOfficialPricingConditionsAreExplicit(t *testing.T) {
	svc := &ModelPlazaService{billingService: newTestBillingService()}
	lookup := func(model string) *PlazaOfficialPricing {
		return svc.lookupOfficialPricing(context.Background(), model, map[string]*PlazaOfficialPricing{})
	}
	glm := lookup("glm-5.1")
	require.Len(t, glm.DisplayTiers, 2)
	require.Equal(t, "<32K", glm.DisplayTiers[0].Label)
	require.Equal(t, "32K+", glm.DisplayTiers[1].Label)
	require.InDelta(t, 8e-6, *glm.DisplayTiers[1].InputPrice, 1e-15)
	require.InDelta(t, 28e-6, *glm.DisplayTiers[1].OutputPrice, 1e-15)
	require.InDelta(t, 2e-6, *glm.DisplayTiers[1].CacheReadPrice, 1e-15)
	require.Empty(t, glm.Intervals, "display labels must not invent billing boundaries")
	require.Contains(t, lookup("glm-5.3-flash").ReferenceNote, "09-09")
	for _, model := range []string{"deepseek-flash", "deepseek-v4-pro"} {
		require.Contains(t, lookup(model).ReferenceNote, "峰价")
		require.Contains(t, lookup(model).ReferenceNote, "不启用")
		require.Empty(t, lookup(model).DisplayTiers)
	}
}

func TestDomesticOfficialPricingDoesNotGuessOrMutateBilling(t *testing.T) {
	billing := newTestBillingService()
	svc := &ModelPlazaService{billingService: billing}
	before, err := billing.GetModelPricing("deepseek-v4.1-flash")
	require.NoError(t, err)
	for _, model := range []string{"deepseek-unknown", "deepseek-v4.1-flash-9999", "glm-unknown"} {
		require.Nil(t, svc.lookupOfficialPricing(context.Background(), model, map[string]*PlazaOfficialPricing{}), model)
	}
	for _, model := range []string{"deepseek-flash", "deepseek-v4.1-flash", "deepseek-v4.1-flash-0910", "deepseek-v4-flash-0731", "glm-5.2"} {
		require.NotNil(t, svc.lookupOfficialPricing(context.Background(), model, map[string]*PlazaOfficialPricing{}))
	}
	after, err := billing.GetModelPricing("deepseek-v4.1-flash")
	require.NoError(t, err)
	require.Equal(t, before, after, "reference lookup must not reprice real requests")

	// An exact domestic entry supplied only as an observed upstream card is not official.
	billing.fallbackPrices["glm-observed"] = &ModelPricing{Currency: "CNY", PriceBasis: "Observed upstream billing card (CNY)", InputPricePerToken: 2e-6}
	require.Nil(t, svc.lookupOfficialPricing(context.Background(), "glm-observed", map[string]*PlazaOfficialPricing{}))
}
