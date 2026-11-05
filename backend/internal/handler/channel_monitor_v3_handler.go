package handler

import (
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ChannelMonitorV3Handler struct {
	svc    *service.ChannelMonitorV3Service
	runner *service.ChannelMonitorV3ProbeRunner
}

func NewChannelMonitorV3Handler(svc *service.ChannelMonitorV3Service) *ChannelMonitorV3Handler {
	return &ChannelMonitorV3Handler{svc: svc}
}

func (h *ChannelMonitorV3Handler) SetProbeRunner(runner *service.ChannelMonitorV3ProbeRunner) {
	h.runner = runner
}

func (h *ChannelMonitorV3Handler) MarkRequest(req *http.Request) *http.Request {
	return h.runner.MarkRequest(req)
}

func (h *ChannelMonitorV3Handler) Config(c *gin.Context) {
	out, err := h.svc.Config(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	if h.runner != nil {
		budget, err := h.runner.Budget(c.Request.Context(), out.Settings.DailyBudgetUSD)
		if err != nil {
			response.ErrorFrom(c, err)
			return
		}
		out.Budget = &budget
	}
	response.Success(c, out)
}

func (h *ChannelMonitorV3Handler) SaveSettings(c *gin.Context) {
	var in service.ChannelMonitorV3Settings
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid settings")
		return
	}
	out, err := h.svc.SaveSettings(c.Request.Context(), in)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, out)
}

func (h *ChannelMonitorV3Handler) SaveGroup(c *gin.Context) {
	groupID, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || groupID <= 0 {
		response.BadRequest(c, "Invalid group id")
		return
	}
	var in service.ChannelMonitorV3GroupConfig
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, "Invalid group configuration")
		return
	}
	in.GroupID = groupID
	out, err := h.svc.SaveGroup(c.Request.Context(), in)
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	response.Success(c, out)
}
