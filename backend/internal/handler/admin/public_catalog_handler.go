package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
)

const publicCatalogVisibilityBodyLimit = 8 << 20

// PublicCatalogHandler manages presentation-only visibility for the two public
// catalogue surfaces. It does not write channel pricing or scheduling data.
type PublicCatalogHandler struct {
	settingService *service.SettingService
	channelService *service.ChannelService
}

func NewPublicCatalogHandler(
	settingService *service.SettingService,
	channelService *service.ChannelService,
) *PublicCatalogHandler {
	return &PublicCatalogHandler{
		settingService: settingService,
		channelService: channelService,
	}
}

// GetVisibility returns the independent display policy and active-channel model candidates.
func (h *PublicCatalogHandler) GetVisibility(c *gin.Context) {
	if h == nil || h.channelService == nil || h.settingService == nil {
		response.InternalError(c, "Public catalogue service is unavailable")
		return
	}
	channels, err := h.channelService.ListAvailable(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	config := h.settingService.GetPublicCatalogVisibility(c.Request.Context())
	response.Success(c, service.BuildPublicCatalogVisibilityView(channels, config))
}

// UpdateVisibility replaces only the public display policy. Candidate loading
// happens before persistence so a failed channel read cannot produce a
// misleading partially successful response.
func (h *PublicCatalogHandler) UpdateVisibility(c *gin.Context) {
	if h == nil || h.channelService == nil || h.settingService == nil {
		response.InternalError(c, "Public catalogue service is unavailable")
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, publicCatalogVisibilityBodyLimit)
	var input service.PublicCatalogVisibilityConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.BadRequest(c, "Invalid public catalogue visibility settings")
		return
	}

	channels, err := h.channelService.ListAvailable(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	updated, err := h.settingService.UpdatePublicCatalogVisibility(c.Request.Context(), input)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, service.BuildPublicCatalogVisibilityView(channels, updated))
}
