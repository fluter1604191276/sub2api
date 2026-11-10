//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func bridgeDiagnosticSSE(payloads ...string) string {
	var body strings.Builder
	for _, payload := range payloads {
		body.WriteString("data: " + payload + "\n\n")
	}
	return body.String()
}

func TestWSHTTPBridgeResponseDiagnosticsOutcomes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	created := `{"type":"response.created","response":{"id":"resp_bridge"}}`
	delta := `{"type":"response.output_text.delta","delta":"private-output"}`
	completed := `{"type":"response.completed","response":{"id":"resp_bridge","status":"completed","max_output_tokens":17,"usage":{"input_tokens":10,"input_tokens_details":{"cached_tokens":2},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":1}}}}`
	incomplete := `{"type":"response.incomplete","response":{"id":"resp_bridge","status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},"max_output_tokens":17,"usage":{"input_tokens":10,"output_tokens":7}}}`
	bareError := `{"type":"error","error":{"code":"invalid_request","message":"private-error"}}`
	for _, tc := range []struct {
		name, body, status, reason, disconnectAt string
		httpStatus                               int
		terminal                                 bool
	}{
		{name: "completed", body: bridgeDiagnosticSSE(created, completed), status: "completed", terminal: true},
		{name: "incomplete", body: bridgeDiagnosticSSE(created, incomplete), status: "incomplete", reason: "max_output_tokens", terminal: true},
		{name: "bare_error", body: bridgeDiagnosticSSE(created, delta, bareError), status: "failed", reason: "invalid_request", terminal: true},
		{name: "authoritative_after_error", body: bridgeDiagnosticSSE(created, bareError, completed), status: "completed", terminal: true},
		{name: "missing_terminal", body: bridgeDiagnosticSSE(created, delta, "[DONE]")},
		// HTTP failures use the actual HTTP status at the storage projection;
		// they must not invent a formal Responses terminal.
		{name: "http_error", body: `{"error":{"message":"unavailable"}}`, httpStatus: http.StatusServiceUnavailable},
		{name: "disconnect_then_drain", body: bridgeDiagnosticSSE(created, delta, completed), status: "completed", terminal: true, disconnectAt: "response.output_text.delta"},
		{name: "terminal_write_disconnect", body: bridgeDiagnosticSSE(completed), status: "completed", terminal: true, disconnectAt: "response.completed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status := tc.httpStatus
			if status == 0 {
				status = http.StatusOK
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: status,
				Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"upstream-bridge"}},
				Body:   io.NopCloser(strings.NewReader(tc.body))}}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
			var snapshots []OpenAIResponseDiagnosticSnapshot
			BindOpenAIResponseDiagnosticSink(c, func(s OpenAIResponseDiagnosticSnapshot) { snapshots = append(snapshots, s) })
			payload := []byte(`{"type":"response.create","model":"gpt-5","max_output_tokens":64,"input":"private-prompt"}`)
			_, _ = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c,
				&Account{ID: 981, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1},
				"test-token", payload, len(payload), "gpt-5", "", "", "", "", 2,
				func(message []byte) error {
					if tc.disconnectAt != "" && gjson.GetBytes(message, "type").String() == tc.disconnectAt {
						return context.Canceled
					}
					return nil
				})
			require.Len(t, snapshots, 1, "a turn must publish even without a formal terminal")
			s := snapshots[0]
			require.True(t, s.Active)
			require.NotEmpty(t, s.TurnID)
			require.EqualValues(t, 981, s.AccountID)
			require.Equal(t, "upstream-bridge", s.UpstreamRequestID)
			require.Equal(t, status, s.HTTPStatus)
			require.Equal(t, tc.status, s.Status)
			require.Equal(t, tc.reason, s.Reason)
			require.Equal(t, tc.terminal, s.Terminal)
			require.Equal(t, tc.disconnectAt != "", s.ClientDisconnected)
			require.EqualValues(t, 64, *s.RequestedMaxOutputTokens)
			require.EqualValues(t, 64, *s.ForwardedMaxOutputTokens)
			_, pending := TakeOpenAIResponseDiagnosticSnapshot(c)
			require.False(t, pending, "middleware fallback must not duplicate the published turn")
			if tc.status == "completed" || tc.status == "incomplete" {
				require.Equal(t, "resp_bridge", s.ResponseID)
				require.EqualValues(t, 17, *s.UpstreamMaxOutputTokens)
				require.EqualValues(t, 10, *s.InputTokens)
			}
		})
	}
}

func TestWSHTTPBridgeResponseDiagnosticsTransportFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{err: errors.New("fixture transport error")}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var snapshots []OpenAIResponseDiagnosticSnapshot
	BindOpenAIResponseDiagnosticSink(c, func(s OpenAIResponseDiagnosticSnapshot) { snapshots = append(snapshots, s) })
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"fixture"}`)
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c,
		&Account{ID: 982, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1},
		"test-token", payload, len(payload), "gpt-5", "", "", "", "", 2, func([]byte) error { return nil })
	require.Error(t, err)
	require.Len(t, snapshots, 1)
	require.Equal(t, "failed", snapshots[0].Status)
	require.False(t, snapshots[0].Terminal, "a transport error must not invent an upstream terminal")
	require.Nil(t, snapshots[0].RequestedMaxOutputTokens)
	require.Nil(t, snapshots[0].ForwardedMaxOutputTokens)
	require.Nil(t, snapshots[0].UpstreamMaxOutputTokens)
}

func TestWSHTTPBridgeResponseDiagnosticsDoesNotRecordUnsentCandidate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{StatusCode: http.StatusOK, Header: make(http.Header),
		Body: io.NopCloser(strings.NewReader(bridgeDiagnosticSSE(`{"type":"response.completed","response":{"id":"resp_sent","status":"completed"}}`)))}}
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	var snapshots []OpenAIResponseDiagnosticSnapshot
	BindOpenAIResponseDiagnosticSink(c, func(s OpenAIResponseDiagnosticSnapshot) { snapshots = append(snapshots, s) })
	payload := []byte(`{"type":"response.create","model":"gpt-5","input":"fixture"}`)
	_, err := svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c,
		&Account{ID: 984, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1},
		"test-token", payload, len(payload), "gpt-5", "", "", "", "", 2, func([]byte) error { return nil })
	require.NoError(t, err)
	_, err = svc.proxyOpenAIWSHTTPBridgeTurn(context.Background(), c,
		&Account{ID: 985, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1},
		"test-token", []byte(`{`), 1, "gpt-5", "", "", "", "", 3, func([]byte) error { return nil })
	require.Error(t, err)
	require.Len(t, snapshots, 1)
	require.EqualValues(t, 984, snapshots[0].AccountID)
	require.Len(t, upstream.requests, 1)
	_, pending := TakeOpenAIResponseDiagnosticSnapshot(c)
	require.False(t, pending, "the unsent candidate must not expose stale attempt metadata")
}

func TestWSHTTPBridgeResponseDiagnosticsRealWebSocketMultiTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	type observation struct {
		frames    []string
		usage     []OpenAIUsage
		snapshots []OpenAIResponseDiagnosticSnapshot
	}
	run := func(enabled bool) observation {
		var upstreamCalls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			call := upstreamCalls.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-Request-Id", "upstream-bridge-turn")
			if call == 1 {
				_, _ = io.WriteString(w, bridgeDiagnosticSSE(`{"type":"response.incomplete","response":{"id":"resp_one","status":"incomplete","model":"gpt-upstream","incomplete_details":{"reason":"max_output_tokens"},"usage":{"input_tokens":10,"output_tokens":7}}}`))
			} else {
				_, _ = io.WriteString(w, bridgeDiagnosticSSE(`{"type":"response.completed","response":{"id":"resp_two","status":"completed","model":"gpt-upstream","usage":{"input_tokens":8,"output_tokens":3}}}`))
			}
		}))
		defer upstream.Close()
		cfg := &config.Config{}
		cfg.Security.URLAllowlist.Enabled = false
		cfg.Security.URLAllowlist.AllowInsecureHTTP = true
		svc := &OpenAIGatewayService{cfg: cfg, httpUpstream: attemptRoundTripClient{client: upstream.Client()}}
		account := &Account{ID: 983, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1,
			Credentials: map[string]any{"base_url": upstream.URL, "api_key": "fixture"}}
		type outcome struct {
			observation
			err error
		}
		finished := make(chan outcome, 1)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			conn, err := coderws.Accept(w, r, nil)
			if err != nil {
				finished <- outcome{err: err}
				return
			}
			defer conn.CloseNow()
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = r.WithContext(context.WithValue(r.Context(), ctxkey.RequestID, "bridge-connection"))
			var got observation
			if enabled {
				BindOpenAIResponseDiagnosticSink(c, func(s OpenAIResponseDiagnosticSnapshot) { got.snapshots = append(got.snapshots, s) })
			}
			for turn := 1; turn <= 2; turn++ {
				_, payload, err := conn.Read(r.Context())
				if err != nil {
					finished <- outcome{err: err}
					return
				}
				result, err := svc.proxyOpenAIWSHTTPBridgeTurn(r.Context(), c, account, "fixture", payload, len(payload),
					"gpt-client", "", "", "", "", turn, func(message []byte) error { return conn.Write(r.Context(), coderws.MessageText, message) })
				if err != nil {
					finished <- outcome{err: err}
					return
				}
				got.usage = append(got.usage, result.Usage)
			}
			finished <- outcome{observation: got}
		}))
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		client, _, err := coderws.Dial(ctx, "ws"+strings.TrimPrefix(server.URL, "http"), nil)
		require.NoError(t, err)
		defer client.CloseNow()
		var frames []string
		for _, payload := range []string{
			`{"type":"response.create","model":"gpt-upstream","max_output_tokens":11,"input":"fixture"}`,
			`{"type":"response.create","model":"gpt-upstream","max_output_tokens":22,"input":"next"}`,
		} {
			require.NoError(t, client.Write(ctx, coderws.MessageText, []byte(payload)))
			_, frame, err := client.Read(ctx)
			require.NoError(t, err)
			frames = append(frames, string(frame))
		}
		select {
		case got := <-finished:
			require.NoError(t, got.err)
			got.frames = frames
			require.EqualValues(t, 2, upstreamCalls.Load())
			return got.observation
		case <-ctx.Done():
			t.Fatal("bridge turns did not finish")
			return observation{}
		}
	}
	baseline, observed := run(false), run(true)
	require.Equal(t, baseline.frames, observed.frames, "diagnostics must not change WebSocket payloads")
	require.Equal(t, baseline.usage, observed.usage, "diagnostics must not change billing usage")
	require.Len(t, observed.snapshots, 2)
	a, b := observed.snapshots[0], observed.snapshots[1]
	require.NotEqual(t, a.TurnID, b.TurnID)
	require.Equal(t, "incomplete", a.Status)
	require.Equal(t, "completed", b.Status)
	require.EqualValues(t, 11, *a.RequestedMaxOutputTokens)
	require.EqualValues(t, 22, *b.RequestedMaxOutputTokens)
	require.EqualValues(t, 983, a.AccountID)
	require.EqualValues(t, 983, b.AccountID)
}
