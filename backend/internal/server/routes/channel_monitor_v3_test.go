package routes

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	adminhandler "github.com/Wei-Shaw/sub2api/internal/handler/admin"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type channelMonitorV3RouteRepo struct {
	groups []service.ChannelMonitorV3GroupConfig
}

func (r *channelMonitorV3RouteRepo) Settings(context.Context) (service.ChannelMonitorV3Settings, error) {
	return service.DefaultChannelMonitorV3Settings(), nil
}
func (r *channelMonitorV3RouteRepo) SaveSettings(_ context.Context, settings service.ChannelMonitorV3Settings) (service.ChannelMonitorV3Settings, error) {
	return settings, nil
}
func (r *channelMonitorV3RouteRepo) ListGroups(context.Context) ([]service.ChannelMonitorV3GroupConfig, error) {
	return r.groups, nil
}
func (r *channelMonitorV3RouteRepo) SaveGroup(_ context.Context, group service.ChannelMonitorV3GroupConfig) (*service.ChannelMonitorV3GroupConfig, error) {
	return &group, nil
}

func newChannelMonitorV3RouteHandlers() *handler.Handlers {
	repo := &channelMonitorV3RouteRepo{groups: []service.ChannelMonitorV3GroupConfig{
		{GroupID: 1, Name: "Visible", Platform: "openai", Status: service.StatusActive, Visible: true},
		{GroupID: 2, Name: "Hidden", Platform: "openai", Status: service.StatusActive, Visible: false},
		{GroupID: 3, Name: "Disabled", Platform: "openai", Status: "disabled", Visible: true},
	}}
	v3 := handler.NewChannelMonitorV3Handler(service.NewChannelMonitorV3Service(repo, service.NewChannelMonitorV3QualityService(nil)))
	return &handler.Handlers{ChannelMonitorV3: v3, Admin: &handler.AdminHandlers{Subscription: adminhandler.NewSubscriptionHandler(nil)}}
}

func TestChannelMonitorV3PublicRouteIsAnonymousAndMinimal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterChannelMonitorV3PublicRoutes(router.Group("/api/v1"), newChannelMonitorV3RouteHandlers(), nil)

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/channel-monitor-v3", nil))
	require.Equal(t, http.StatusOK, w.Code)

	var body struct {
		Data struct {
			Groups []map[string]any `json:"groups"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data.Groups, 1)
	group := body.Data.Groups[0]
	require.Equal(t, "Visible", group["name"])
	require.Contains(t, group, "group_id")
	require.Contains(t, group, "last_10")
	require.Contains(t, group, "last_100")
	require.NotContains(t, group, "platform")
	require.NotContains(t, group, "visible")
	require.NotContains(t, group, "test_model")
}

func TestChannelMonitorV3AdminRoutesRequireAuthentication(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	adminAuth := servermiddleware.AdminAuthMiddleware(func(c *gin.Context) {
		servermiddleware.AbortWithError(c, http.StatusUnauthorized, "UNAUTHORIZED", "Authorization required")
	})
	auditLog := servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() })
	stepUp := servermiddleware.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() })
	RegisterAdminRoutes(router.Group("/api/v1"), newChannelMonitorV3RouteHandlers(), adminAuth, auditLog, stepUp, nil, nil)

	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/admin/channel-monitor-v3", nil),
		httptest.NewRequest(http.MethodPut, "/api/v1/admin/channel-monitor-v3/settings", strings.NewReader(`{"enabled":true,"daily_budget_usd":2}`)),
	} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, request)
		require.Equal(t, http.StatusUnauthorized, w.Code)
	}
}
