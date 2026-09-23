package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type streamStickyEscapeFixture struct {
	service     *OpenAIGatewayService
	context     *gin.Context
	account     *Account
	cache       *stubGatewayCache
	currentHash string
	legacyHash  string
}

func newStreamStickyEscapeFixture(t *testing.T, accountID int64) streamStickyEscapeFixture {
	t.Helper()
	groupID := int64(91000 + accountID%100)
	account := &Account{ID: accountID, Name: "stream-failed", Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	currentHash, legacyHash := deriveOpenAISessionHashes("stream-sticky-escape")
	cache := &stubGatewayCache{sessionBindings: map[string]int64{
		"openai:" + currentHash: account.ID,
		"openai:" + legacyHash:  account.ID,
	}}
	service := &OpenAIGatewayService{
		cache:                cache,
		openaiModelTransient: newOpenAIAccountModelTransientState(32),
		cfg: &config.Config{Gateway: config.GatewayConfig{
			MaxLineSize: defaultMaxLineSize,
			OpenAIScheduler: config.GatewayOpenAISchedulerConfig{
				StickyEscapeEnabled: true,
			},
		}},
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	attachOpenAILegacySessionHashToGin(c, legacyHash)
	c.Set("api_key", &APIKey{
		GroupID: &groupID,
		Group: &Group{
			ID:                        groupID,
			StreamStickyEscapeEnabled: true,
		},
	})
	c.Set(openAIStreamRecoverySessionHashKey, currentHash)
	return streamStickyEscapeFixture{
		service:     service,
		context:     c,
		account:     account,
		cache:       cache,
		currentHash: currentHash,
		legacyHash:  legacyHash,
	}
}

func (f streamStickyEscapeFixture) requireEscaped(t *testing.T, model string) {
	t.Helper()
	require.True(t, f.service.isOpenAIAccountModelRuntimeBlocked(f.account, model))
	require.Equal(t, 1, f.cache.deletedSessions["openai:"+f.currentHash])
	require.Equal(t, 1, f.cache.deletedSessions["openai:"+f.legacyHash])
}

func TestHandleCommittedOpenAIStreamDisconnectClearsStickyAndBlocksOnlyAccountModel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newStreamStickyEscapeFixture(t, 910011)

	fixture.service.handleCommittedOpenAIStreamDisconnect(
		fixture.context,
		fixture.account,
		"gpt-5.6-sol",
		errors.New("upstream connection reset"),
		true,
		false,
		false,
		false,
		"read_error",
	)

	fixture.requireEscaped(t, "gpt-5.6-sol")
	require.False(t, fixture.service.isOpenAIAccountModelRuntimeBlocked(fixture.account, "gpt-5.6-terra"))
	require.NotContains(t, fixture.cache.sessionBindings, "openai:"+fixture.currentHash)
	require.NotContains(t, fixture.cache.sessionBindings, "openai:"+fixture.legacyHash)
	require.Len(t, fixture.cache.deletedGroups, 2)

	// Multiple cleanup paths may observe the same request. The context flag keeps
	// cooldown and sticky deletion idempotent.
	fixture.service.handleCommittedOpenAIStreamDisconnect(
		fixture.context,
		fixture.account,
		"gpt-5.6-sol",
		errors.New("same request cleanup"),
		true,
		false,
		false,
		false,
		"missing_terminal",
	)
	require.Equal(t, 1, fixture.cache.deletedSessions["openai:"+fixture.currentHash])
	require.Equal(t, 1, fixture.cache.deletedSessions["openai:"+fixture.legacyHash])
}

func TestOpenAIStreamRecoverySessionAuthoritativeHashSurvivesForwardNormalization(t *testing.T) {
	gin.SetMode(gin.TestMode)
	currentHash, currentLegacyHash := deriveOpenAISessionHashes("handler-session")
	forwardHash, forwardLegacyHash := deriveOpenAISessionHashes("account-normalized-body")
	require.NotEqual(t, currentHash, forwardHash)
	require.NotEqual(t, currentLegacyHash, forwardLegacyHash)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	attachOpenAILegacySessionHashToGin(c, currentLegacyHash)
	SetOpenAIStreamRecoverySession(c, currentHash)

	// Simulate Forward's old body-derived fallback. It must not replace the
	// handler's routing identity while a failover reuses the same Gin context.
	rememberOpenAIStreamRecoverySession(c, forwardHash)
	recoveryHash, _ := c.Get(openAIStreamRecoverySessionHashKey)
	require.Equal(t, currentHash, recoveryHash)
	require.Equal(t, currentLegacyHash, openAILegacySessionHashFromContext(c.Request.Context()))

	fixture := newStreamStickyEscapeFixture(t, 910061)
	fixture.context = c
	fixture.cache.sessionBindings = map[string]int64{
		"openai:" + currentHash:       fixture.account.ID,
		"openai:" + currentLegacyHash: fixture.account.ID,
		"openai:" + forwardHash:       fixture.account.ID,
		"openai:" + forwardLegacyHash: fixture.account.ID,
	}
	groupID := int64(91061)
	c.Set("api_key", &APIKey{
		GroupID: &groupID,
		Group: &Group{
			ID:                        groupID,
			StreamStickyEscapeEnabled: true,
		},
	})

	fixture.service.handleCommittedOpenAIStreamDisconnect(
		c,
		fixture.account,
		"gpt-5.6-sol",
		errors.New("upstream connection reset"),
		true,
		false,
		false,
		false,
		"read_error",
	)

	require.Contains(t, fixture.cache.deletedSessions, "openai:"+currentHash)
	require.Contains(t, fixture.cache.deletedSessions, "openai:"+currentLegacyHash)
	require.NotContains(t, fixture.cache.deletedSessions, "openai:"+forwardHash)
	require.NotContains(t, fixture.cache.deletedSessions, "openai:"+forwardLegacyHash)
}

func TestHandleCommittedOpenAIStreamDisconnectIgnoresNonUpstreamOrNonSemanticEnds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	account := &Account{ID: 910021, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	for _, tc := range []struct {
		name                string
		semanticOutput      bool
		clientOutputStarted bool
		clientDisconnected  bool
		terminalSeen        bool
		requestContext      context.Context
		wantBlocked         bool
	}{
		{name: "no semantic output still escapes", requestContext: context.Background(), wantBlocked: true},
		{name: "client disconnected", semanticOutput: true, clientOutputStarted: true, clientDisconnected: true, requestContext: context.Background()},
		{name: "normal terminal", semanticOutput: true, clientOutputStarted: true, terminalSeen: true, requestContext: context.Background()},
		{name: "request canceled", semanticOutput: true, clientOutputStarted: true, requestContext: canceledContext()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &OpenAIGatewayService{
				openaiModelTransient: newOpenAIAccountModelTransientState(32),
				cfg: &config.Config{Gateway: config.GatewayConfig{
					OpenAIScheduler: config.GatewayOpenAISchedulerConfig{StickyEscapeEnabled: true},
				}},
			}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil).WithContext(tc.requestContext)
			c.Set("api_key", &APIKey{Group: &Group{StreamStickyEscapeEnabled: true}})

			svc.handleCommittedOpenAIStreamDisconnect(
				c,
				account,
				"gpt-5.6-sol",
				errors.New("stream ended"),
				tc.semanticOutput,
				tc.clientOutputStarted,
				tc.clientDisconnected,
				tc.terminalSeen,
				"test",
			)

			require.Equal(t, tc.wantBlocked, svc.isOpenAIAccountModelRuntimeBlocked(account, "gpt-5.6-sol"))
		})
	}
}

func TestHandleCommittedOpenAIStreamDisconnectHonorsGroupSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newStreamStickyEscapeFixture(t, 910071)
	apiKeyValue, ok := fixture.context.MustGet("api_key").(*APIKey)
	require.True(t, ok)
	require.NotNil(t, apiKeyValue.Group)
	apiKeyValue.Group.StreamStickyEscapeEnabled = false

	fixture.service.handleCommittedOpenAIStreamDisconnect(
		fixture.context,
		fixture.account,
		"gpt-5.6-sol",
		errors.New("upstream connection reset"),
		true,
		false,
		false,
		false,
		"read_error",
	)

	require.False(t, fixture.service.isOpenAIAccountModelRuntimeBlocked(fixture.account, "gpt-5.6-sol"))
	require.Empty(t, fixture.cache.deletedSessions)
}

func TestHandleCommittedOpenAIStreamDisconnectHonorsGlobalSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newStreamStickyEscapeFixture(t, 910081)
	fixture.service.cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = false

	fixture.service.handleCommittedOpenAIStreamDisconnect(
		fixture.context,
		fixture.account,
		"gpt-5.6-sol",
		errors.New("upstream connection reset"),
		true,
		false,
		false,
		false,
		"read_error",
	)

	require.False(t, fixture.service.isOpenAIAccountModelRuntimeBlocked(fixture.account, "gpt-5.6-sol"))
	require.Empty(t, fixture.cache.deletedSessions)
}

func TestNewOpenAIStreamDisconnectFailoverErrorHonorsGroupSwitch(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newStreamStickyEscapeFixture(t, 910091)

	enabled := fixture.service.newOpenAIStreamDisconnectFailoverError(
		fixture.context,
		fixture.account,
		false,
		"request-enabled",
		nil,
		"upstream connection reset",
	)
	require.True(t, enabled.AvoidSameAccountRetry)

	apiKeyValue, ok := fixture.context.MustGet("api_key").(*APIKey)
	require.True(t, ok)
	require.NotNil(t, apiKeyValue.Group)
	apiKeyValue.Group.StreamStickyEscapeEnabled = false

	disabled := fixture.service.newOpenAIStreamDisconnectFailoverError(
		fixture.context,
		fixture.account,
		false,
		"request-disabled",
		nil,
		"upstream connection reset",
	)
	require.False(t, disabled.AvoidSameAccountRetry)
}

func canceledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func TestOpenAIChatChunkHasSemanticOutput(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload string
		want    bool
	}{
		{name: "content", payload: `{"choices":[{"delta":{"content":"hello"}}]}`, want: true},
		{name: "reasoning", payload: `{"choices":[{"delta":{"reasoning_content":"think"}}]}`, want: true},
		{name: "tool arguments", payload: `{"choices":[{"delta":{"tool_calls":[{"id":"call_1","function":{"arguments":"{}"}}]}}]}`, want: true},
		{name: "role only", payload: `{"choices":[{"delta":{"role":"assistant"}}]}`, want: false},
		{name: "finish only", payload: `{"choices":[{"delta":{},"finish_reason":"stop"}]}`, want: false},
		{name: "usage only", payload: `{"choices":[],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, want: false},
		{name: "empty tool placeholder", payload: `{"choices":[{"delta":{"tool_calls":[{"index":0,"type":"function"}]}}]}`, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, openAIChatChunkHasSemanticOutput(tc.payload))
		})
	}
}

func TestOpenAIResponsesChatBridgeMissingTerminalEscapesSticky(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newStreamStickyEscapeFixture(t, 910031)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-chat-bridge-truncated"}},
	}

	_, err := fixture.service.handleChatStreamingResponse(
		resp,
		fixture.context,
		fixture.account,
		"gpt-5.6-sol",
		"gpt-5.6-sol",
		"gpt-5.6-sol",
		time.Now(),
		0,
	)
	require.ErrorContains(t, err, "missing terminal event")
	fixture.requireEscaped(t, "gpt-5.6-sol")
}

func TestOpenAIResponsesAnthropicBridgeMissingTerminalEscapesSticky(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newStreamStickyEscapeFixture(t, 910041)
	fixture.context.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	attachOpenAILegacySessionHashToGin(fixture.context, fixture.legacyHash)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(strings.Join([]string{
			`data: {"type":"response.output_text.delta","delta":"partial"}`,
			"",
		}, "\n"))),
		Header: http.Header{"X-Request-Id": []string{"rid-messages-bridge-truncated"}},
	}

	_, err := fixture.service.handleAnthropicStreamingResponse(
		resp,
		fixture.context,
		fixture.account,
		"claude-sonnet-4-6",
		"claude-sonnet-4-6",
		"claude-sonnet-4-6",
		time.Now(),
	)
	require.ErrorContains(t, err, "missing terminal event")
	fixture.requireEscaped(t, "claude-sonnet-4-6")
}

func TestOpenAIRawChatTruncationEscapesStickyAfterSemanticOutput(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fixture := newStreamStickyEscapeFixture(t, 910051)
	fixture.context.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	attachOpenAILegacySessionHashToGin(fixture.context, fixture.legacyHash)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: &openAIStreamReadThenErrorCloser{
			reader: strings.NewReader("data: {\"id\":\"chatcmpl_partial\",\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"),
			err:    io.ErrUnexpectedEOF,
		},
		Header: http.Header{"X-Request-Id": []string{"rid-raw-truncated"}},
	}

	_, err := fixture.service.streamRawChatCompletions(
		fixture.context,
		resp,
		fixture.account,
		"gpt-5.6-sol",
		"gpt-5.6-sol",
		"gpt-5.6-sol",
		nil,
		nil,
		time.Now(),
		0,
	)
	require.Error(t, err)
	fixture.requireEscaped(t, "gpt-5.6-sol")
}
