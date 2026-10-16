//go:build unit

package admin

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestChannelCatalogResponseIsReadOnlyModelMetadata(t *testing.T) {
	price := 17.0
	channels := []service.AvailableChannel{{
		ID: 7, Name: "internal channel name", Description: "private notes",
		SupportedModels: []service.SupportedModel{
			{Name: "gpt-6.1-sol", Platform: "openai", Pricing: &service.ChannelModelPricing{InputPrice: &price}},
			{Name: "gpt-6.1-sol", Platform: "openai"},
			{Name: "claude-alias", Platform: "anthropic"},
		},
	}}
	result := buildChannelCatalogResponse(channels)
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	require.JSONEq(t, `{"source":"configured_accounts","availability":"configured_not_live","channels":[{"channel_id":7,"models":[{"name":"claude-alias","platform":"anthropic"},{"name":"gpt-6.1-sol","platform":"openai"}]}]}`, string(encoded))
	require.Len(t, channels[0].SupportedModels, 3)
	require.Equal(t, price, *channels[0].SupportedModels[0].Pricing.InputPrice)
}

func TestChannelCatalogResponseEmptyIsAnArray(t *testing.T) {
	encoded, err := json.Marshal(buildChannelCatalogResponse(nil))
	require.NoError(t, err)
	require.JSONEq(t, `{"source":"configured_accounts","availability":"configured_not_live","channels":[]}`, string(encoded))
}
