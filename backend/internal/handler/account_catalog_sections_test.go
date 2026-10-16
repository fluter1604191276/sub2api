//go:build unit

package handler

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountCatalogSectionsDoNotLeakAnExclusivePool(t *testing.T) {
	public := service.SupportedModel{Name: "gpt-6.1-sol", Platform: service.PlatformOpenAI}
	private := service.SupportedModel{Name: "private-only-alias", Platform: service.PlatformOpenAI}
	channel := service.AvailableChannel{
		SupportedModels: []service.SupportedModel{public, private},
		GroupModels: map[int64][]service.SupportedModel{
			1: {public}, 2: {private},
		},
	}
	sections := buildPublicPlatformSections(channel,
		[]userAvailableGroup{{ID: 1, Platform: service.PlatformOpenAI}},
		service.DefaultPublicCatalogVisibilityConfig())
	require.Len(t, sections, 1)
	require.Len(t, sections[0].SupportedModels, 1)
	require.Equal(t, public.Name, sections[0].SupportedModels[0].Name)
	require.Nil(t, sections[0].SupportedModels[0].Pricing, "unknown prices must not become free")
	encoded, err := json.Marshal(sections)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), private.Name)
	require.Len(t, channel.SupportedModels, 2, "projection must not mutate the shared admin union")
}

func TestAccountCatalogSectionsKeepDifferentPoolsAndPricesSeparate(t *testing.T) {
	a, b := 2e-6, 4e-6
	channel := service.AvailableChannel{
		GroupModels: map[int64][]service.SupportedModel{
			1: {{Name: "gpt-6.1-sol", Platform: service.PlatformOpenAI, Pricing: &service.ChannelModelPricing{InputPrice: &a}}},
			2: {{Name: "gpt-6.1-sol", Platform: service.PlatformOpenAI, Pricing: &service.ChannelModelPricing{InputPrice: &b}}},
		},
	}
	groups := []userAvailableGroup{{ID: 1, Platform: service.PlatformOpenAI}, {ID: 2, Platform: service.PlatformOpenAI}}
	sections := buildPublicPlatformSections(channel, groups, service.DefaultPublicCatalogVisibilityConfig())
	require.Len(t, sections, 2)
	require.Equal(t, int64(1), sections[0].Groups[0].ID)
	require.Equal(t, a, *sections[0].SupportedModels[0].Pricing.InputPrice)
	require.Equal(t, int64(2), sections[1].Groups[0].ID)
	require.Equal(t, b, *sections[1].SupportedModels[0].Pricing.InputPrice)
}

func TestAccountCatalogSectionsMergeIdenticalPoolsAndRespectVisibility(t *testing.T) {
	model := service.SupportedModel{Name: "gpt-6.1-sol", Platform: service.PlatformOpenAI}
	channel := service.AvailableChannel{GroupModels: map[int64][]service.SupportedModel{1: {model}, 2: {model}, 3: {}}}
	groups := []userAvailableGroup{{ID: 1, Platform: service.PlatformOpenAI}, {ID: 2, Platform: service.PlatformOpenAI}, {ID: 3, Platform: service.PlatformOpenAI}}
	visibility := service.DefaultPublicCatalogVisibilityConfig()
	sections := buildPublicPlatformSections(channel, groups, visibility)
	require.Len(t, sections, 1)
	require.Len(t, sections[0].Groups, 2)
	visibility.Models["openai:gpt-6.1-sol"] = false
	require.Empty(t, buildPublicPlatformSections(channel, groups, visibility))
}
