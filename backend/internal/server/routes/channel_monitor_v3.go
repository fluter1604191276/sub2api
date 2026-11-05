package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
)

func RegisterChannelMonitorV3PublicRoutes(v1 *gin.RouterGroup, h *handler.Handlers, limiter *middleware.PanelRateLimiter) {
	if h.ChannelMonitorV3 == nil {
		return
	}
	v1.GET("/channel-monitor-v3", limiter.PublicIP(), h.ChannelMonitorV3.Public)
}
