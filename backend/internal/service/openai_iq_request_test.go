package service

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestMarkIQRequestRequiresLoopbackTokenAndStripsHeader(t *testing.T) {
	s := &OpenAIIQService{requestToken: []byte("synthetic-test-token")}
	for _, tc := range []struct {
		name, remote, token string
		marked              bool
	}{
		{"correct loopback", "127.0.0.1:1234", "synthetic-test-token", true},
		{"wrong token", "127.0.0.1:1234", "wrong", false},
		{"nonloopback", "192.0.2.4:1234", "synthetic-test-token", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://example.test/v1/responses", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set(iqRequestHeader, tc.token)
			req = s.MarkIQRequest(req)
			require.Equal(t, tc.marked, IsOpenAIIQRequest(req.Context()))
			require.Empty(t, req.Header.Get(iqRequestHeader))
		})
	}
}

func TestMarkIQRequestSetsTrustedCanonicalDispatchID(t *testing.T) {
	const dispatch = "7f7c19ba-903f-4142-b93e-f9f72387ba55"
	s := &OpenAIIQService{requestToken: []byte("synthetic-test-token")}
	for _, tc := range []struct {
		name, remote, token, dispatch string
		wantID                        string
	}{
		{"valid", "127.0.0.1:1234", "synthetic-test-token", dispatch, "iq-" + dispatch},
		{"invalid UUID", "127.0.0.1:1234", "synthetic-test-token", "not-a-uuid", ""},
		{"wrong token", "127.0.0.1:1234", "wrong", dispatch, ""},
		{"nonloopback", "192.0.2.4:1234", "synthetic-test-token", dispatch, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "http://example.test/v1/responses", nil)
			req.RemoteAddr = tc.remote
			req.Header.Set(iqRequestHeader, tc.token)
			req.Header.Set(iqDispatchHeader, tc.dispatch)
			req = s.MarkIQRequest(req)
			got, _ := req.Context().Value(ctxkey.ClientRequestID).(string)
			require.Equal(t, tc.wantID, got)
			require.Empty(t, req.Header.Get(iqRequestHeader))
			require.Empty(t, req.Header.Get(iqDispatchHeader))
		})
	}
}

func TestMarkIQRequestDoesNotGrantOtherPermissions(t *testing.T) {
	s := &OpenAIIQService{requestToken: []byte("synthetic-test-token")}
	req := httptest.NewRequest("POST", "http://example.test/v1/responses", nil)
	req.RemoteAddr = "[::1]:1234"
	req.Header.Set(iqRequestHeader, "synthetic-test-token")
	marked := s.MarkIQRequest(req)
	require.True(t, IsOpenAIIQRequest(marked.Context()))
	require.Nil(t, marked.Context().Value(struct{}{}))
	require.False(t, IsOpenAIIQRequest(context.Background()))
}
