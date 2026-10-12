package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *AccountHandler) PreviewPriorityConversion(c *gin.Context) {
	if role, ok := middleware.GetUserRoleFromContext(c); !ok || role != service.RoleAdmin {
		response.Error(c, 403, "Admin access required")
		return
	}
	var req struct {
		AccountIDs []int64 `json:"account_ids" binding:"required,min=1,dive,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid account selection")
		return
	}
	svc, ok := h.adminService.(service.AccountPriorityConversionAdminService)
	if !ok {
		response.Error(c, 500, "Priority conversion unavailable")
		return
	}
	result, err := svc.PreviewAccountPriorityConversion(c.Request.Context(), req.AccountIDs)
	if err != nil {
		response.Error(c, 500, "Could not load selected accounts")
		return
	}
	response.Success(c, result)
}

func (h *AccountHandler) ApplyPriorityConversion(c *gin.Context) {
	if role, ok := middleware.GetUserRoleFromContext(c); !ok || role != service.RoleAdmin {
		response.Error(c, 403, "Admin access required")
		return
	}
	var req struct {
		Accounts []service.AccountPriorityConfirmation `json:"accounts" binding:"required,min=1,dive"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid priority confirmation")
		return
	}
	svc, ok := h.adminService.(service.AccountPriorityConversionAdminService)
	if !ok {
		response.Error(c, 500, "Priority conversion unavailable")
		return
	}
	result, err := svc.ApplyAccountPriorityConversion(c.Request.Context(), req.Accounts)
	if err != nil {
		response.Error(c, 500, "Could not update selected accounts")
		return
	}
	response.Success(c, result)
}
