package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type openAIVideoHTTPUpstreamStub struct {
	response *http.Response
}

func (s *openAIVideoHTTPUpstreamStub) Do(_ *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	return s.response, nil
}

func (s *openAIVideoHTTPUpstreamStub) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return s.Do(req, proxyURL, accountID, accountConcurrency)
}

func TestBindOpenAIVideoTaskAccountUsesTaskIDStickyHash(t *testing.T) {
	ctx := context.Background()
	groupID := int64(7)
	cache := &stubGatewayCache{}
	svc := &OpenAIGatewayService{cache: cache}

	hash := OpenAIVideoTaskSessionHash("video-task-123")
	require.NotEmpty(t, hash)
	require.NoError(t, svc.BindOpenAIVideoTaskAccount(ctx, &groupID, "video-task-123", 63))

	accountID, err := svc.getStickySessionAccountID(ctx, &groupID, hash)
	require.NoError(t, err)
	require.Equal(t, int64(63), accountID)
}

func TestForwardVideoGenerationTracksTaskIDSeparatelyFromRequestID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	body := []byte(`{"model":"seedance-480p-5s","prompt":"test"}`)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", bytes.NewReader(body))

	response := &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
			"X-Request-Id": []string{"request-header-456"},
		},
		Body: io.NopCloser(strings.NewReader(`{"id":"video-task-123","status":"queued"}`)),
	}
	svc := &OpenAIGatewayService{
		httpUpstream: &openAIVideoHTTPUpstreamStub{response: response},
		cfg:          &config.Config{},
	}
	account := &Account{
		ID:          63,
		Name:        "video-upstream",
		Platform:    PlatformOpenAI,
		Type:        AccountTypeAPIKey,
		Concurrency: 1,
		Credentials: map[string]any{
			"api_key":  "test-api-key",
			"base_url": "https://video-upstream.example/v1",
		},
	}
	parsed, err := svc.ParseOpenAIVideoGenerationRequest(body)
	require.NoError(t, err)

	result, err := svc.ForwardVideoGeneration(context.Background(), c, account, body, parsed, "")
	require.NoError(t, err)
	require.Equal(t, "request-header-456", result.RequestID)
	require.Equal(t, "video-task-123", result.VideoTaskID)
}
