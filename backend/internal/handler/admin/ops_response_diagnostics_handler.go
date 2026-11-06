package admin

import (
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ListResponseDiagnostics exposes metadata only, behind the existing admin/ops auth boundary.
func (h *OpsHandler) ListResponseDiagnostics(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	if err := h.opsService.RequireMonitoringEnabled(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	start, end, err := parseOpsTimeRange(c, "1h")
	if err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	page, size := response.ParsePagination(c)
	f := &service.OpsResponseDiagnosticFilter{StartTime: &start, EndTime: &end, Page: page, PageSize: size, Status: strings.TrimSpace(c.Query("status")), Reason: strings.TrimSpace(c.Query("reason")), RequestID: strings.TrimSpace(c.Query("request_id"))}
	if f.Status != "" && !service.ValidResponseDiagnosticStatus(f.Status) {
		response.BadRequest(c, "Invalid status")
		return
	}
	if len(f.Reason) > 128 || len(f.RequestID) > 256 {
		response.BadRequest(c, "Diagnostic filter too long")
		return
	}
	if f.UserID, err = parseOptionalPositiveID(c, "user_id"); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if f.AccountID, err = parseOptionalPositiveID(c, "account_id"); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	result, err := h.opsService.ListResponseDiagnostics(c.Request.Context(), f)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, result)
}
