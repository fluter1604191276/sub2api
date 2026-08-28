package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPostRawJSONMarksChannelMonitorRequests(t *testing.T) {
	var received http.Header
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	t.Cleanup(server.Close)

	previousClient := monitorHTTPClient
	monitorHTTPClient = server.Client()
	t.Cleanup(func() { monitorHTTPClient = previousClient })

	_, status, err := postRawJSON(context.Background(), server.URL, []byte(`{}`), map[string]string{
		"User-Agent":                "custom-user-agent",
		"X-Sub2API-Channel-Monitor": "0",
	})

	require.NoError(t, err)
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "1", received.Get("X-Sub2API-Channel-Monitor"))
	require.Equal(t, "custom-user-agent sub2api-channel-monitor/1", received.Get("User-Agent"))
}
