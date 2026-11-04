package handler

import (
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type OpenAIIQHandler struct{ service *service.OpenAIIQService }

func NewOpenAIIQHandler(service *service.OpenAIIQService) *OpenAIIQHandler {
	return &OpenAIIQHandler{service: service}
}

func (h *OpenAIIQHandler) IQRequestMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h != nil && h.service != nil && c.Request != nil {
			c.Request = h.service.MarkIQRequest(c.Request)
			if service.IsOpenAIIQRequest(c.Request.Context()) {
				if requestID, _ := c.Request.Context().Value(ctxkey.ClientRequestID).(string); strings.HasPrefix(requestID, "iq-") {
					c.Header("X-Client-Request-ID", requestID)
				}
			}
		}
		c.Next()
	}
}
func (h *OpenAIIQHandler) Get(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "User not found in context")
		return
	}
	view, err := h.service.View(c.Request.Context(), subject.UserID)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, view)
}
func (h *OpenAIIQHandler) GetSettings(c *gin.Context) {
	settings, groups, budget, err := h.service.Admin(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	identity, err := h.service.TestIdentity(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	var userID *int64
	var balance float64
	if identity != nil {
		userID = &identity.UserID
		balance = identity.Balance
	}
	response.Success(c, gin.H{"settings": settings, "groups": groups, "test_user_id": userID, "test_user_balance": balance, "budget": budget})
}
func (h *OpenAIIQHandler) UpdateSettings(c *gin.Context) {
	var in service.IQSettings
	if err := c.ShouldBindJSON(&in); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	_, err := h.service.UpdateSettings(c.Request.Context(), in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.GetSettings(c)
}
