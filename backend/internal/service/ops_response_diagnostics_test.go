package service

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeResponseDiagnosticMetadata(t *testing.T) {
	negative := int64(-1)
	zero := int64(0)
	d := &OpsResponseDiagnostic{RequestID: " request\n", Status: "incomplete", Reason: "max_output_tokens", OutputTokens: &zero, ReasoningTokens: &negative, ResponseID: strings.Repeat("a", 257)}
	require.NoError(t, NormalizeResponseDiagnostic(d))
	require.Equal(t, "request", d.RequestID)
	require.Equal(t, "max_output_tokens", d.Reason)
	require.Empty(t, d.ResponseID)
	require.Nil(t, d.ReasoningTokens)
	require.NotNil(t, d.OutputTokens)
	require.Zero(t, *d.OutputTokens)
	require.False(t, d.CreatedAt.IsZero())
	d.Reason = "upstream echoed a private prompt"
	require.NoError(t, NormalizeResponseDiagnostic(d))
	require.Equal(t, "unknown", d.Reason)
}
func TestNormalizeResponseDiagnosticRequiresIdentityAndTerminalClass(t *testing.T) {
	require.Error(t, NormalizeResponseDiagnostic(nil))
	require.Error(t, NormalizeResponseDiagnostic(&OpsResponseDiagnostic{Status: "completed"}))
	require.Error(t, NormalizeResponseDiagnostic(&OpsResponseDiagnostic{RequestID: "id", Status: "success"}))
	for _, s := range []string{"completed", "incomplete", "failed", "client_disconnected", "missing_terminal"} {
		require.True(t, ValidResponseDiagnosticStatus(s))
	}
}

func TestNormalizeResponseDiagnosticRejectsFreeformIdentifiers(t *testing.T) {
	invalidID := int64(0)
	d := &OpsResponseDiagnostic{RequestID: "server-uuid", Status: "failed", ClientRequestID: "Bearer private-token", UpstreamRequestID: "{\"prompt\":\"secret\"}", ResponseID: "resp\nsecret", Model: "<script>alert(1)</script>", UserID: &invalidID}
	require.NoError(t, NormalizeResponseDiagnostic(d))
	require.Empty(t, d.ClientRequestID)
	require.Empty(t, d.UpstreamRequestID)
	require.Empty(t, d.ResponseID)
	require.Empty(t, d.Model)
	require.Nil(t, d.UserID)
	d.RequestID = "invalid request id"
	require.Error(t, NormalizeResponseDiagnostic(d))
}

func TestResponseDiagnosticMonitoringDisabled(t *testing.T) {
	s := &OpsService{}
	s.runtimeSettings.Store(&opsRuntimeSettingsSnapshot{monitoringEnabled: false})
	// Disabled ingestion does not touch persistence, even for malformed metadata.
	require.NoError(t, s.InsertResponseDiagnostic(context.Background(), nil))
	_, err := s.ListResponseDiagnostics(context.Background(), nil)
	require.ErrorIs(t, err, ErrOpsDisabled)
}
