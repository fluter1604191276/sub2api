package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestOpenAIGenericUpstreamFailureClassification(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		upstreamMsg  string
		upstreamBody string
		wantGeneric  bool
		wantFailover bool
	}{
		{
			name:         "structured error message",
			statusCode:   http.StatusBadRequest,
			upstreamBody: `{"error":{"type":"upstream_error","message":"Upstream request failed"}}`,
			wantGeneric:  true,
			wantFailover: true,
		},
		{
			name:         "response envelope error message",
			statusCode:   http.StatusBadRequest,
			upstreamBody: `{"response":{"error":{"message":"Upstream request failed"}}}`,
			wantGeneric:  true,
			wantFailover: true,
		},
		{
			name:         "message supplied by caller",
			statusCode:   http.StatusBadRequest,
			upstreamMsg:  "Upstream request failed",
			wantGeneric:  true,
			wantFailover: true,
		},
		{
			name:         "plain text upstream body",
			statusCode:   http.StatusBadRequest,
			upstreamBody: "Upstream request failed",
			wantGeneric:  true,
			wantFailover: true,
		},
		{
			name:         "ordinary invalid request",
			statusCode:   http.StatusBadRequest,
			upstreamBody: `{"error":{"type":"invalid_request_error","message":"Invalid model parameter"}}`,
			wantGeneric:  false,
			wantFailover: false,
		},
		{
			name:         "echoed request text does not qualify",
			statusCode:   http.StatusBadRequest,
			upstreamBody: `{"error":{"message":"Invalid input"},"echo":{"prompt":"Upstream request failed"}}`,
			wantGeneric:  false,
			wantFailover: false,
		},
		{
			name:         "generic message on non400 is handled by status rules",
			statusCode:   http.StatusBadGateway,
			upstreamBody: `{"error":{"message":"Upstream request failed"}}`,
			wantGeneric:  false,
			wantFailover: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(tt.upstreamBody)
			require.Equal(t, tt.wantGeneric, isOpenAIGenericUpstreamFailure(tt.statusCode, tt.upstreamMsg, body))
			require.Equal(t, tt.wantFailover, (&OpenAIGatewayService{}).shouldFailoverOpenAIUpstreamResponse(tt.statusCode, tt.upstreamMsg, body))
		})
	}
}

func TestOpenAIGenericUpstreamFailureDoesNotTrustEchoedJSONFields(t *testing.T) {
	body := []byte(`{"error":{"type":"invalid_request_error","message":"Invalid input"},"details":{"message":"Upstream request failed"}}`)

	require.False(t, isOpenAIGenericUpstreamFailure(http.StatusBadRequest, "", body))
	require.False(t, (&OpenAIGatewayService{}).shouldFailoverOpenAIUpstreamResponse(http.StatusBadRequest, "", body))
}

func TestNewOpenAIGenericUpstreamFailureIsAccountScopedFailover(t *testing.T) {
	body := []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
	err := newOpenAIUpstreamFailoverError(
		http.StatusBadRequest,
		nil,
		body,
		"Upstream request failed",
		false,
	)

	require.True(t, err.ShouldRetryNextAccount())
	require.False(t, err.RetryableOnSameAccount)
	require.False(t, err.RequestScopedTransient)
	require.Equal(t, GatewayFailureScopeAccount, err.Scope)
	require.Equal(t, OpenAIGenericUpstreamFailureReason, err.Reason)
	require.Equal(t, NextAccountRetry, err.NextAccountAction)
	require.Equal(t, http.StatusBadGateway, err.ClientStatusCode)
	require.Equal(t, "Upstream request failed", err.ClientMessage)
}
