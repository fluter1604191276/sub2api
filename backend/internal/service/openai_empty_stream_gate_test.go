package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func deepSeekResponsesTestAccount() *Account {
	return &Account{
		ID:          7991,
		Name:        "deepseek-adaptive",
		Platform:    PlatformDeepseek,
		Type:        AccountTypeAPIKey,
		Status:      StatusActive,
		Schedulable: true,
		Credentials: map[string]any{
			"api_key":      "sk-test",
			"api_protocol": APIProtocolAdaptive,
			"api_base_urls": map[string]any{
				APIProtocolResponses: "https://relay.example",
			},
		},
	}
}

func deepSeekResponsesTestService(upstream *httpUpstreamRecorder) *OpenAIGatewayService {
	cfg := &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}}
	return &OpenAIGatewayService{
		cfg:              cfg,
		httpUpstream:     upstream,
		cache:            &stubGatewayCache{},
		openaiWSResolver: NewOpenAIWSProtocolResolver(cfg),
		toolCorrector:    NewCodexToolCorrector(),
	}
}

func deepSeekResponsesRequestBody() []byte {
	return []byte(`{
		"model":"deepseek-v4-pro",
		"stream":true,
		"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"continue"}]}]
	}`)
}

type cancelReader struct{ err error }

func (r cancelReader) Read([]byte) (int, error) { return 0, r.err }
func (r cancelReader) Close() error             { return nil }

func forwardDeepSeekSSE(t *testing.T, stream io.Reader, requestID string) (*gin.Context, *httptest.ResponseRecorder, *OpenAIGatewayService, error) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}, "x-request-id": []string{"upstream-1"}},
		Body:       io.NopCloser(stream),
	}}
	svc := deepSeekResponsesTestService(upstream)
	c, recorder := newOpenAIImageGenerationControlTestContext(false, "codex_cli_rs/0.144.1")
	if requestID != "" {
		c.Request = c.Request.WithContext(context.WithValue(c.Request.Context(), ctxkey.RequestID, requestID))
	}
	_, err := svc.Forward(context.Background(), c, deepSeekResponsesTestAccount(), deepSeekResponsesRequestBody())
	return c, recorder, svc, err
}

func TestDeepSeekResponsesEmptyStreamCases(t *testing.T) {
	cases := []struct {
		name         string
		stream       string
		wantFailover bool
		wantBody     string
	}{
		{
			name: "empty completed",
			stream: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_empty\",\"status\":\"in_progress\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_empty\",\"status\":\"completed\",\"output\":[]}}\n\n",
			wantFailover: true,
		},
		{
			name: "abnormal end",
			stream: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_eof\",\"status\":\"in_progress\"}}\n\n" +
				"data: {\"type\":\"heartbeat\"}\n\n" +
				"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"message\",\"role\":\"assistant\",\"content\":[]}}\n\n",
			wantFailover: true,
		},
		{
			name: "all-zero usage",
			stream: "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_zero\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_zero\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"id\":\"msg_1\"}],\"usage\":{\"input_tokens\":0,\"output_tokens\":0,\"total_tokens\":0}}}\n\n",
			wantFailover: true,
		},
		{
			name: "legitimate tool output",
			stream: "data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"name\":\"lookup\",\"arguments\":\"{}\"}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_tool\",\"status\":\"completed\",\"output\":[{\"type\":\"function_call\",\"name\":\"lookup\",\"arguments\":\"{}\"}],\"usage\":{\"input_tokens\":4,\"output_tokens\":2}}}\n\n",
			wantBody: "lookup",
		},
		{
			name: "non-empty success",
			stream: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":3,\"output_tokens\":1}}}\n\n",
			wantBody: "hello",
		},
		{
			name:   "context length is not an empty stream",
			stream: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ctx\",\"status\":\"completed\",\"output\":[],\"error\":{\"code\":\"context_length_exceeded\",\"message\":\"maximum context length exceeded\"},\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n",
		},
		{
			name:   "content filter is not an empty stream",
			stream: "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_filter\",\"status\":\"incomplete\",\"output\":[],\"incomplete_details\":{\"reason\":\"content_filter\"}}}\n\n",
		},
		{
			name: "output budget is not an empty stream",
			stream: "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_budget\",\"status\":\"incomplete\",\"output\":[{\"type\":\"message\",\"content\":[{\"type\":\"output_text\",\"text\":\"partial\"}]}],\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":8,\"output_tokens\":1}}}\n\n",
			wantBody: "partial",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, recorder, svc, err := forwardDeepSeekSSE(t, strings.NewReader(tc.stream), "req-"+tc.name)
			var failoverErr *UpstreamFailoverError
			if tc.wantFailover {
				require.ErrorAs(t, err, &failoverErr)
				require.Equal(t, http.StatusBadGateway, failoverErr.StatusCode)
				require.Empty(t, recorder.Body.String())
				events := opsUpstreamEvents(t, c)
				require.NotEmpty(t, events)
				require.Equal(t, int64(7991), events[0].AccountID)
				require.Equal(t, PlatformDeepseek, events[0].Platform)
				require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(deepSeekResponsesTestAccount(), "deepseek-v4-pro"))
				return
			}
			require.NotErrorAs(t, err, &failoverErr)
			if tc.wantBody != "" {
				require.Contains(t, recorder.Body.String(), tc.wantBody)
			}
			require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(deepSeekResponsesTestAccount(), "deepseek-v4-pro"))
		})
	}
}

func TestDeepSeekResponsesAfterPartialDoesNotReplay(t *testing.T) {
	c, recorder, svc, err := forwardDeepSeekSSE(t, strings.NewReader(
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"already sent\"}\n\n",
	), "req-partial")
	var failoverErr *UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
	require.Error(t, err)
	require.Contains(t, err.Error(), "missing terminal event")
	require.Contains(t, recorder.Body.String(), "already sent")
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(deepSeekResponsesTestAccount(), "deepseek-v4-pro"))
	events := opsUpstreamEvents(t, c)
	require.NotEmpty(t, events)
	require.Equal(t, "stream_incomplete", events[len(events)-1].Kind)
}

func TestDeepSeekResponsesClientCancelDoesNotFailoverOrIsolate(t *testing.T) {
	c, _, svc, err := forwardDeepSeekSSE(t, cancelReader{err: context.Canceled}, "req-cancel")
	var failoverErr *UpstreamFailoverError
	require.NotErrorAs(t, err, &failoverErr)
	require.False(t, svc.isOpenAIAccountModelRuntimeBlocked(deepSeekResponsesTestAccount(), "deepseek-v4-pro"))
	_, hasEvents := c.Get(OpsUpstreamErrorsKey)
	require.False(t, hasEvents)
}

func TestOpenAIEmptyStreamIsolationWindow(t *testing.T) {
	state := newOpenAIAccountModelTransientState(16)
	now := time.Date(2026, 11, 3, 2, 0, 0, 0, time.UTC)
	accountID := int64(7991)

	first := state.recordEmptyStream(accountID, "DeepSeek-V4-Pro", "req-1", now)
	require.Equal(t, 1, first.FailureStreak)
	require.False(t, state.isBlocked(accountID, "deepseek-v4-pro", now))

	duplicate := state.recordEmptyStream(accountID, "deepseek-v4-pro", "req-1", now.Add(time.Second))
	require.Equal(t, 1, duplicate.FailureStreak)
	require.False(t, state.isBlocked(accountID, "deepseek-v4-pro", now.Add(time.Second)))

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		state.recordEmptyStream(accountID, "deepseek-v4-pro", "req-left", now.Add(2*time.Second))
	}()
	go func() {
		defer wg.Done()
		state.recordEmptyStream(accountID, "deepseek-v4-pro", "req-right", now.Add(2*time.Second))
	}()
	wg.Wait()
	require.True(t, state.isBlocked(accountID, "deepseek-v4-pro", now.Add(3*time.Second)))
	require.False(t, state.isBlocked(accountID, "deepseek-v4-flash", now.Add(3*time.Second)))

	state.recordSuccess(accountID, "deepseek-v4-pro")
	require.True(t, state.isBlocked(accountID, "deepseek-v4-pro", now.Add(4*time.Second)), "a short success must not clear empty-stream isolation")
	require.False(t, state.isBlocked(accountID, "deepseek-v4-pro", now.Add(openAIEmptyStreamIsolation+time.Minute)))

	later := now.Add(openAIEmptyStreamWindow + time.Minute)
	reset := state.recordEmptyStream(accountID, "deepseek-v4-pro", "req-new-window", later)
	require.Equal(t, 1, reset.FailureStreak)
	require.False(t, state.isBlocked(accountID, "deepseek-v4-pro", later))
}

func TestRecoveryProbeProtocolEligibilityDoesNotCrossProtocols(t *testing.T) {
	state := &GroupRecoveryProbeState{Status: GroupRecoveryProbeStatusFailed, LastErrorClass: GroupRecoveryProbeErrorTransient}
	adaptive := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	require.False(t, recoveryProbeAllowsFailover(adaptive, "responses", nil))
	require.False(t, recoveryProbeAllowsFailover(adaptive, "responses", state))

	state.ProtocolEligibility = map[string]GroupRecoveryProbeProtocolStatus{
		APIProtocolAnthropic: mergeBusinessProtocolProbe(GroupRecoveryProbeProtocolStatus{}, false, "Adaptive Anthropic endpoint timed out"),
		APIProtocolResponses: mergeBusinessProtocolProbe(GroupRecoveryProbeProtocolStatus{}, true, ""),
	}
	require.Equal(t, GroupRecoveryProbeStatusFailed, state.ProtocolEligibility[APIProtocolAnthropic].Status)
	require.Equal(t, GroupRecoveryProbeStatusWarm, state.ProtocolEligibility[APIProtocolResponses].Status)
	require.False(t, recoveryProbeAllowsFailover(adaptive, "responses", state))
	require.False(t, recoveryProbeAllowsFailover(adaptive, "messages", state))

	state.ProtocolEligibility[APIProtocolResponses] = mergeBusinessProtocolProbe(state.ProtocolEligibility[APIProtocolResponses], true, "")
	require.Equal(t, GroupRecoveryProbeStatusEligible, state.ProtocolEligibility[APIProtocolResponses].Status)
	require.True(t, recoveryProbeAllowsFailover(adaptive, "responses", state))
	require.False(t, recoveryProbeAllowsFailover(adaptive, "messages", state), "Responses success must not unlock Anthropic")

	openai := &Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	require.False(t, recoveryProbeAllowsFailover(openai, "responses", &GroupRecoveryProbeState{Status: GroupRecoveryProbeStatusWarm}))
	require.False(t, recoveryProbeAllowsFailover(openai, "responses", &GroupRecoveryProbeState{Status: GroupRecoveryProbeStatusPending}))
	require.True(t, recoveryProbeAllowsFailover(openai, "responses", &GroupRecoveryProbeState{Status: GroupRecoveryProbeStatusEligible}))
}

func TestRecoveryProbeBudgetExhaustionStaysPending(t *testing.T) {
	now := time.Date(2026, 11, 3, 3, 0, 0, 0, time.UTC)
	job := GroupRecoveryProbeJob{
		State:          GroupRecoveryProbeState{ID: 1, Status: GroupRecoveryProbeStatusPending},
		PreviousStatus: GroupRecoveryProbeStatusPending,
		ClaimedAt:      now,
	}
	completion, settlement := buildGroupRecoveryProbeReservationFailure(job, ErrGroupRecoveryProbeBudgetExceeded, now)
	require.Equal(t, GroupRecoveryProbeStatusPending, completion.Status)
	require.Equal(t, GroupRecoveryProbeSettlementBudgetBlocked, settlement)
	adaptive := &Account{Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	require.False(t, recoveryProbeAllowsFailover(adaptive, "responses", &GroupRecoveryProbeState{Status: completion.Status}))
}

func TestAdaptiveProtocolProbeIgnoresPollutedLegacyStatus(t *testing.T) {
	account := &Account{ID: 7962, Platform: PlatformDeepseek, Type: AccountTypeAPIKey, Credentials: map[string]any{"api_protocol": APIProtocolAdaptive}}
	state := &GroupRecoveryProbeState{
		Status:         GroupRecoveryProbeStatusFailed,
		LastErrorClass: GroupRecoveryProbeErrorTransient,
		LastError:      "Adaptive Anthropic endpoint returned 404",
		ProtocolEligibility: map[string]GroupRecoveryProbeProtocolStatus{
			APIProtocolResponses: {Status: GroupRecoveryProbeStatusEligible, ConsecutiveSuccesses: 2},
			APIProtocolAnthropic: {Status: GroupRecoveryProbeStatusFailed, ConsecutiveFailures: 11, LastErrorClass: GroupRecoveryProbeErrorTransient},
		},
	}
	item := SmartSchedulerPreviewItem{AccountID: account.ID, Pool: "warm", Schedulable: true, ModelSupported: true, EndpointSupported: true}
	applyGroupRecoveryProbeStateForRequest(&item, account, "responses", state)
	require.NotEqual(t, "isolated", item.Pool)
	require.True(t, item.ProbeBootstrap)

	messages := SmartSchedulerPreviewItem{AccountID: account.ID, Pool: "primary", Schedulable: true, ModelSupported: true, EndpointSupported: true}
	applyGroupRecoveryProbeStateForRequest(&messages, account, "messages", state)
	require.Equal(t, "isolated", messages.Pool)

	group := &Group{ID: 54, RecoveryProbeEnabled: true, SmartSchedulerEnabled: true}
	ordering := &SmartSchedulerOrdering{
		Active: true,
		ItemByAccountID: map[int64]SmartSchedulerPreviewItem{
			account.ID: {AccountID: account.ID, RecoveryProbe: state},
		},
		RankByAccountID: map[int64]int{account.ID: 1},
	}
	require.Empty(t, filterFailoverByProtocolEligibility(group, "messages", []*Account{account}, nil, false, ordering))
	require.Equal(t, []*Account{account}, filterFailoverByProtocolEligibility(group, "responses", []*Account{account}, nil, false, ordering))
	require.Equal(t, []*Account{account}, filterFailoverByProtocolEligibility(&Group{RecoveryProbeEnabled: false}, "messages", []*Account{account}, nil, false, ordering))
}

func TestOpenAIResponsesCompletedEventTreatsZeroUsageAndEmptyShellAsEmpty(t *testing.T) {
	require.True(t, openAIResponsesCompletedEventIsEmpty([]byte(`{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":0,"output_tokens":0,"total_tokens":0}}}`), nil))
	require.True(t, openAIResponsesCompletedEventIsEmpty([]byte(`{"type":"response.completed","response":{"output":[{"type":"message","role":"assistant","content":[]}]}}`), &OpenAIUsage{}))
	require.False(t, openAIResponsesCompletedEventIsEmpty([]byte(`{"type":"response.completed","response":{"output":[{"type":"function_call","name":"lookup"}]}}`), nil))
	require.False(t, openAIResponsesCompletedEventIsEmpty([]byte(`{"type":"response.completed","response":{"output":[],"usage":{"input_tokens":3,"output_tokens":0}}}`), nil))
	require.False(t, openAIResponsesCompletedEventIsEmpty([]byte(`{"type":"response.completed","response":{"incomplete_details":{"reason":"max_output_tokens"},"output":[]}}`), nil))
	require.False(t, openAIResponsesCompletedEventIsEmpty([]byte(`{"response":{"error":{"code":"context_length_exceeded","message":"maximum context length exceeded"}}}`), nil))

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("User-Agent", "sub2api-recovery-probe/1.0")
	require.False(t, openAIEmptyStreamCountsForBreaker(c))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Request.Header.Set("User-Agent", "codex")
	require.False(t, openAIEmptyStreamCountsForBreaker(c))
}

func opsUpstreamEvents(t *testing.T, c *gin.Context) []*OpsUpstreamErrorEvent {
	t.Helper()
	raw, ok := c.Get(OpsUpstreamErrorsKey)
	if !ok {
		return nil
	}
	events, _ := raw.([]*OpsUpstreamErrorEvent)
	return events
}
