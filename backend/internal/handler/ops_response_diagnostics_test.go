package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestBuildOpsResponseDiagnosticHTTP200Incomplete(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	request := httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.RequestID, "server-id"))
	request = request.WithContext(context.WithValue(request.Context(), ctxkey.ClientRequestID, "client-id"))
	c.Request = request
	c.Set(opsModelKey, "gpt-test")
	c.Set(opsStreamKey, true)
	c.Set(opsAccountIDKey, int64(19))
	c.Writer.WriteHeader(http.StatusOK)
	requested, forwarded, input, cached, output := int64(128), int64(64), int64(7), int64(3), int64(2)
	entry := buildOpsResponseDiagnostic(c, service.OpenAIResponseDiagnosticSnapshot{
		Active: true, AccountID: 19, Terminal: true, Status: "incomplete", Reason: "max_output_tokens", ResponseID: "resp-id", UpstreamRequestID: "upstream-id",
		RequestedMaxOutputTokens: &requested, ForwardedMaxOutputTokens: &forwarded, InputTokens: &input, CacheReadTokens: &cached, OutputTokens: &output,
	})
	require.NotNil(t, entry)
	require.Equal(t, "server-id", entry.RequestID)
	require.Equal(t, "client-id", entry.ClientRequestID)
	require.Equal(t, "upstream-id", entry.UpstreamRequestID)
	require.Equal(t, "incomplete", entry.Status)
	require.Equal(t, "max_output_tokens", entry.Reason)
	require.Equal(t, http.StatusOK, entry.HTTPStatus)
	require.EqualValues(t, 19, *entry.AccountID)
	require.EqualValues(t, 3, *entry.CacheReadTokens)
	require.Nil(t, entry.CacheCreationTokens)
}

func TestBuildOpsResponseDiagnosticTerminalAndFailureStatusPrecedence(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(context.WithValue(context.Background(), ctxkey.RequestID, "server-id"))
	c.Writer.WriteHeader(http.StatusBadGateway)
	failed := buildOpsResponseDiagnostic(c, service.OpenAIResponseDiagnosticSnapshot{Active: true})
	require.Equal(t, "failed", failed.Status)
	completedDisconnect := buildOpsResponseDiagnostic(c, service.OpenAIResponseDiagnosticSnapshot{Active: true, Terminal: true, Status: "completed", ClientDisconnected: true})
	require.Equal(t, "completed", completedDisconnect.Status)
	require.True(t, completedDisconnect.ClientDisconnected)
	c = func() *gin.Context {
		next, _ := gin.CreateTestContext(httptest.NewRecorder())
		next.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(context.WithValue(context.Background(), ctxkey.RequestID, "server-id"))
		next.Writer.WriteHeader(http.StatusOK)
		return next
	}()
	missing := buildOpsResponseDiagnostic(c, service.OpenAIResponseDiagnosticSnapshot{Active: true})
	require.Equal(t, "missing_terminal", missing.Status)
	transportFailure := buildOpsResponseDiagnostic(c, service.OpenAIResponseDiagnosticSnapshot{Active: true, Status: "failed", Reason: "transport", HTTPStatus: http.StatusServiceUnavailable})
	require.Equal(t, "failed", transportFailure.Status)
	require.Equal(t, "transport", transportFailure.Reason)
	require.Equal(t, http.StatusServiceUnavailable, transportFailure.HTTPStatus)
}

func TestBuildOpsResponseDiagnosticCanceledClientContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(context.WithValue(ctx, ctxkey.RequestID, "server-id"))
	c.Writer.WriteHeader(http.StatusOK)
	disconnected := buildOpsResponseDiagnostic(c, service.OpenAIResponseDiagnosticSnapshot{Active: true})
	require.Equal(t, "client_disconnected", disconnected.Status)
	require.True(t, disconnected.ClientDisconnected)
	completed := buildOpsResponseDiagnostic(c, service.OpenAIResponseDiagnosticSnapshot{Active: true, Terminal: true, Status: "completed"})
	require.Equal(t, "completed", completed.Status)
	require.True(t, completed.ClientDisconnected)
}

func TestBuildOpsResponseDiagnosticRetainsAttemptAccountAfterCandidateFailure(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx, cancel := context.WithCancel(context.WithValue(context.Background(), ctxkey.RequestID, "failover-request"))
		defer cancel()
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(ctx)
		// A was sent and observed; the handler selects B, but fails to acquire its
		// slot (or the client cancels) before B ever reaches Forward.
		snapshot := service.OpenAIResponseDiagnosticSnapshot{Active: true, AccountID: 11, Terminal: true, Status: "failed", UpstreamRequestID: "upstream-A", ResponseID: "response-A"}
		setOpsSelectedAccount(c, 22, "openai")
		c.Writer.WriteHeader(http.StatusTooManyRequests)
		if canceled {
			cancel()
		}
		entry := buildOpsResponseDiagnostic(c, snapshot)
		require.EqualValues(t, 11, *entry.AccountID)
		require.Equal(t, "upstream-A", entry.UpstreamRequestID)
		require.Equal(t, "response-A", entry.ResponseID)
		require.Equal(t, canceled, entry.ClientDisconnected)
		// Unknown attempt identity must not fall back to an unattempted candidate.
		snapshot.AccountID = 0
		require.Nil(t, buildOpsResponseDiagnostic(c, snapshot).AccountID)
	}
}
