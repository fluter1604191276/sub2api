package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMonitorCostCorrelationUsesGatewayClientID(t *testing.T) {
	original := monitorHTTPClient
	monitorHTTPClient = &http.Client{}
	t.Cleanup(func() { monitorHTTPClient = original })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Client-Request-ID", "gateway-generated-id")
		w.Header().Set("X-Request-ID", "different-upstream-id")
		w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`))
	}))
	defer server.Close()
	result := runCheckForModel(context.Background(), "openai", server.URL, "test-only", "gpt-test", &CheckOptions{
		BodyOverrideMode: MonitorBodyOverrideModeReplace,
		BodyOverride:     map[string]any{"model": "gpt-test", "messages": []any{map[string]any{"role": "user", "content": "hi"}}},
	})
	require.Equal(t, "client:gateway-generated-id", result.BillingRequestID)
	require.True(t, result.UsageComplete)
}
