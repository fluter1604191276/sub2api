package admin

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// UpstreamAttemptHandler exposes the non-billing shadow ledger to admins for
// reconciliation. Route authentication is deliberately supplied by the caller.
type UpstreamAttemptHandler struct {
	repository service.UpstreamAttemptRepository
}

func NewUpstreamAttemptHandler(repository service.UpstreamAttemptRepository) *UpstreamAttemptHandler {
	return &UpstreamAttemptHandler{repository: repository}
}

func (h *UpstreamAttemptHandler) QueueStatus(c *gin.Context) {
	response.Success(c, service.UpstreamAttemptQueueStatus())
}

// List returns individual upstream attempts. GET /admin/ops/upstream-attempts
func (h *UpstreamAttemptHandler) List(c *gin.Context) {
	if h == nil || h.repository == nil {
		response.Error(c, http.StatusServiceUnavailable, "Upstream attempt ledger is not available")
		return
	}
	start, end, err := parseOpsTimeRange(c, "24h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	accountID, err := parseOptionalPositiveID(c, "account_id")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	source := strings.TrimSpace(c.Query("source"))
	if source != "" && !service.ValidUpstreamAttemptSource(source) {
		response.BadRequest(c, "Invalid source")
		return
	}
	page, size := response.ParsePagination(c)
	result, err := h.repository.List(c.Request.Context(), &service.UpstreamAttemptFilter{
		StartTime: &start,
		EndTime:   &end,
		AccountID: accountID,
		Source:    source,
		Page:      page,
		PageSize:  size,
	})
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}

// DailyCosts returns the reconciliation totals grouped in Asia/Shanghai days.
// GET /admin/ops/upstream-attempts/daily-costs
func (h *UpstreamAttemptHandler) DailyCosts(c *gin.Context) {
	if h == nil || h.repository == nil {
		response.Error(c, http.StatusServiceUnavailable, "Upstream attempt ledger is not available")
		return
	}
	start, end, err := parseOpsTimeRange(c, "24h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	accountID, err := parseOptionalPositiveID(c, "account_id")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.repository.DailyCosts(c.Request.Context(), accountID, start, end)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
