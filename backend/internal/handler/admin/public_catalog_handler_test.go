//go:build unit

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type publicCatalogHandlerSettingRepo struct {
	service.SettingRepository
	values   map[string]string
	setCalls int
}

func (r *publicCatalogHandlerSettingRepo) GetValue(_ context.Context, key string) (string, error) {
	if value, ok := r.values[key]; ok {
		return value, nil
	}
	return "", service.ErrSettingNotFound
}

func (r *publicCatalogHandlerSettingRepo) Set(_ context.Context, key, value string) error {
	r.setCalls++
	if r.values == nil {
		r.values = make(map[string]string)
	}
	r.values[key] = value
	return nil
}

type publicCatalogHandlerChannelRepo struct {
	service.ChannelRepository
	channels  []service.Channel
	listErr   error
	listCalls int
}

func (r *publicCatalogHandlerChannelRepo) ListAll(_ context.Context) ([]service.Channel, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.channels, nil
}

type publicCatalogHandlerGroupRepo struct {
	service.GroupRepository
	groups []service.Group
}

func (r *publicCatalogHandlerGroupRepo) ListActive(_ context.Context) ([]service.Group, error) {
	return r.groups, nil
}

func newPublicCatalogHandlerForTest(
	settingRepo *publicCatalogHandlerSettingRepo,
	channelRepo *publicCatalogHandlerChannelRepo,
) *PublicCatalogHandler {
	settingService := service.NewSettingService(settingRepo, &config.Config{})
	channelService := service.NewChannelService(
		channelRepo,
		&publicCatalogHandlerGroupRepo{},
		nil,
		nil,
		nil,
	)
	return NewPublicCatalogHandler(settingService, channelService)
}

func publicCatalogTestChannel() service.Channel {
	return service.Channel{
		ID:     1,
		Name:   "public",
		Status: service.StatusActive,
		ModelPricing: []service.ChannelModelPricing{
			{
				Platform:    service.PlatformOpenAI,
				Models:      []string{"gpt-5.6-sol"},
				BillingMode: service.BillingModeToken,
			},
		},
	}
}

func TestPublicCatalogHandlerNilDependenciesReturn500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := NewPublicCatalogHandler(nil, nil)

	for _, tc := range []struct {
		name   string
		method string
		body   string
		call   func(*gin.Context)
	}{
		{name: "get", method: http.MethodGet, call: h.GetVisibility},
		{name: "update", method: http.MethodPut, body: `{}`, call: h.UpdateVisibility},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tc.method, "/api/v1/admin/public-catalog/visibility", bytes.NewBufferString(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")

			tc.call(c)

			require.Equal(t, http.StatusInternalServerError, w.Code)
		})
	}
}

func TestPublicCatalogHandlerGetVisibilityReturnsStoredPolicyAndCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stored, err := json.Marshal(service.PublicCatalogVisibilityConfig{
		DefaultMediaVisibility: service.PublicCatalogMediaHidden,
		Models:                 map[string]bool{"openai:gpt-5.6-sol": false},
	})
	require.NoError(t, err)
	settingRepo := &publicCatalogHandlerSettingRepo{values: map[string]string{
		service.SettingKeyPublicCatalogVisibility: string(stored),
	}}
	channelRepo := &publicCatalogHandlerChannelRepo{channels: []service.Channel{publicCatalogTestChannel()}}
	h := newPublicCatalogHandlerForTest(settingRepo, channelRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/public-catalog/visibility", nil)
	h.GetVisibility(c)

	require.Equal(t, http.StatusOK, w.Code)
	var envelope struct {
		Code int                                 `json:"code"`
		Data service.PublicCatalogVisibilityView `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &envelope))
	require.Zero(t, envelope.Code)
	require.Equal(t, service.PublicCatalogMediaHidden, envelope.Data.DefaultMediaVisibility)
	require.Equal(t, map[string]bool{"openai:gpt-5.6-sol": false}, envelope.Data.Models)
	require.Len(t, envelope.Data.Candidates, 1)
	require.Equal(t, "openai:gpt-5.6-sol", envelope.Data.Candidates[0].Key)
	require.False(t, envelope.Data.Candidates[0].Visible)
}

func TestPublicCatalogHandlerUpdateRejectsInvalidPayloadWithoutPersisting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "invalid json", body: `{"default_media_visibility":`},
		{name: "invalid model key", body: `{"default_media_visibility":"hidden","models":{"missing-platform":true}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			settingRepo := &publicCatalogHandlerSettingRepo{values: map[string]string{}}
			channelRepo := &publicCatalogHandlerChannelRepo{channels: []service.Channel{publicCatalogTestChannel()}}
			h := newPublicCatalogHandlerForTest(settingRepo, channelRepo)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(http.MethodPut, "/api/v1/admin/public-catalog/visibility", bytes.NewBufferString(tc.body))
			c.Request.Header.Set("Content-Type", "application/json")
			h.UpdateVisibility(c)

			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Zero(t, settingRepo.setCalls)
		})
	}
}

func TestPublicCatalogHandlerUpdateDoesNotPersistWhenChannelReadFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	settingRepo := &publicCatalogHandlerSettingRepo{values: map[string]string{}}
	channelRepo := &publicCatalogHandlerChannelRepo{listErr: errors.New("channel database unavailable")}
	h := newPublicCatalogHandlerForTest(settingRepo, channelRepo)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/public-catalog/visibility",
		bytes.NewBufferString(`{"default_media_visibility":"hidden","models":{}}`),
	)
	c.Request.Header.Set("Content-Type", "application/json")
	h.UpdateVisibility(c)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, 1, channelRepo.listCalls)
	require.Zero(t, settingRepo.setCalls)
}
