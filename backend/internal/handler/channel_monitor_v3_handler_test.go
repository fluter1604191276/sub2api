package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type channelMonitorV3HandlerRepoStub struct {
	group service.ChannelMonitorV3GroupConfig
}

func (s *channelMonitorV3HandlerRepoStub) Settings(context.Context) (service.ChannelMonitorV3Settings, error) {
	return service.DefaultChannelMonitorV3Settings(), nil
}
func (s *channelMonitorV3HandlerRepoStub) SaveSettings(_ context.Context, in service.ChannelMonitorV3Settings) (service.ChannelMonitorV3Settings, error) {
	return in, nil
}
func (s *channelMonitorV3HandlerRepoStub) ListGroups(context.Context) ([]service.ChannelMonitorV3GroupConfig, error) {
	return nil, nil
}
func (s *channelMonitorV3HandlerRepoStub) SaveGroup(_ context.Context, in service.ChannelMonitorV3GroupConfig) (*service.ChannelMonitorV3GroupConfig, error) {
	s.group = in
	return &in, nil
}

func TestChannelMonitorV3SaveGroupPathIDOverridesBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &channelMonitorV3HandlerRepoStub{}
	h := NewChannelMonitorV3Handler(service.NewChannelMonitorV3Service(repo, nil))
	r := gin.New()
	r.PUT("/groups/:id", h.SaveGroup)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/groups/9", strings.NewReader(`{"group_id":999,"visible":true,"probe_interval_seconds":60,"test_model":"m"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, int64(9), repo.group.GroupID)
}
