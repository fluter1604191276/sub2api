package service

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestErrorPassthroughClientMessageRedactsUpstreamSecrets(t *testing.T) {
	for _, msg := range []string{
		"invalid schema: https://provider.invalid/v1?api_key=synthetic-query&token=synthetic-token",
		"invalid schema: https://provider.invalid/v1#access_token=synthetic-fragment",
		"invalid schema: http://127.0.0.1:8080/path?client_secret=synthetic-secret; lookup provider.invalid failed",
	} {
		t.Run(msg, func(t *testing.T) {
			inner, _ := json.Marshal(map[string]any{"error": map[string]string{"message": msg}})
			for _, body := range [][]byte{inner, func() []byte {
				b, _ := json.Marshal(map[string]any{"error": map[string]string{"message": string(inner)}})
				return b
			}()} {
				rule := &model.ErrorPassthroughRule{Enabled: true, PassthroughBody: true}
				got := ErrorPassthroughClientMessage(rule, body)
				require.Contains(t, got, "invalid schema")
				require.Contains(t, got, "[upstream_url]")
				for _, secret := range []string{"provider.invalid", "127.0.0.1", "synthetic-query", "synthetic-token", "synthetic-fragment", "synthetic-secret"} {
					require.NotContains(t, got, secret)
				}
			}
		})
	}
	custom := "Operator diagnostic: try a smaller input"
	require.Equal(t, custom, ErrorPassthroughClientMessage(&model.ErrorPassthroughRule{CustomMessage: &custom}, []byte(`{"error":{"message":"upstream"}}`)))
}

func TestApplyErrorPassthroughRuleRedactsMatchedBodyKeepsPolicy(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	code := 422
	rule := newNonFailoverPassthroughRule(418, "invalid schema", code, "")
	rule.PassthroughBody = true
	rule.SkipMonitoring = true
	svc := &ErrorPassthroughService{}
	svc.setLocalCache([]*model.ErrorPassthroughRule{rule})
	BindErrorPassthroughService(c, svc)
	status, kind, msg, matched := applyErrorPassthroughRule(c, PlatformOpenAI, 418, []byte(`{"error":{"message":"invalid schema at https://mock.invalid/?api_key=synthetic-query#token=synthetic-fragment"}}`), 502, "server_error", "default")
	require.True(t, matched)
	require.Equal(t, code, status)
	require.Equal(t, "upstream_error", kind)
	require.Contains(t, msg, "invalid schema")
	require.NotContains(t, msg, "mock.invalid")
	require.NotContains(t, msg, "synthetic-query")
	require.NotContains(t, msg, "synthetic-fragment")
	require.Equal(t, true, c.MustGet(OpsSkipPassthroughKey))
}

func TestErrorPassthroughClientMessageTopLevelAndEncodedQuery(t *testing.T) {
	for _, field := range []string{"message", "detail"} {
		t.Run(field, func(t *testing.T) {
			body, err := json.Marshal(map[string]string{field: "invalid schema at https://provider.invalid/path?%61pi_key=synthetic%2Dquery#refresh_token=synthetic%2Dfragment"})
			require.NoError(t, err)
			got := ErrorPassthroughClientMessage(&model.ErrorPassthroughRule{PassthroughBody: true}, body)
			require.Contains(t, got, "invalid schema")
			require.Contains(t, got, "[upstream_url]")
			for _, secret := range []string{"provider.invalid", "synthetic%2Dquery", "synthetic%2Dfragment", "%61pi_key", "refresh_token"} {
				require.NotContains(t, got, secret)
			}
		})
	}
}
