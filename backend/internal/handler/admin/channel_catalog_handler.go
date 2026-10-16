package admin

import (
	"sort"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type channelCatalogModel struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type channelCatalogEntry struct {
	ChannelID int64                 `json:"channel_id"`
	Models    []channelCatalogModel `json:"models"`
}

type channelCatalogResponse struct {
	Source       string                `json:"source"`
	Availability string                `json:"availability"`
	Channels     []channelCatalogEntry `json:"channels"`
}

// GetModelCatalog supplies read-only account-derived model suggestions to the
// pricing editor. It deliberately returns no price rules or account metadata.
// GET /api/v1/admin/channels/model-catalog
func (h *ChannelHandler) GetModelCatalog(c *gin.Context) {
	channels, err := h.channelService.ListAvailable(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, buildChannelCatalogResponse(channels))
}

func buildChannelCatalogResponse(channels []service.AvailableChannel) channelCatalogResponse {
	result := channelCatalogResponse{
		Source: "configured_accounts", Availability: "configured_not_live",
		Channels: make([]channelCatalogEntry, 0, len(channels)),
	}
	for _, channel := range channels {
		entry := channelCatalogEntry{ChannelID: channel.ID, Models: []channelCatalogModel{}}
		seen := make(map[string]bool)
		for _, model := range channel.SupportedModels {
			key := model.Platform + ":" + strings.ToLower(model.Name)
			if !seen[key] {
				seen[key] = true
				entry.Models = append(entry.Models, channelCatalogModel{Name: model.Name, Platform: model.Platform})
			}
		}
		sort.Slice(entry.Models, func(i, j int) bool {
			if entry.Models[i].Platform != entry.Models[j].Platform {
				return entry.Models[i].Platform < entry.Models[j].Platform
			}
			return entry.Models[i].Name < entry.Models[j].Name
		})
		result.Channels = append(result.Channels, entry)
	}
	return result
}
