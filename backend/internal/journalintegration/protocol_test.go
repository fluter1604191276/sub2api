//go:build localjournal

package journalintegration

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type memoryAntigravity struct{ calls int }

func (m *memoryAntigravity) Do(req *http.Request, _ string, _ int64, _ int) (*http.Response, error) {
	m.calls++
	if m.calls > 1 {
		return nil, fmt.Errorf("unexpected extra synthetic call")
	}
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}, "X-Request-Id": []string{"synthetic-antigravity-upstream"}}, Body: io.NopCloser(strings.NewReader("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"synthetic reply\"}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":1000,\"candidatesTokenCount\":2000,\"cachedContentTokenCount\":100}}}\n\n"))}, nil
}
func (m *memoryAntigravity) DoWithTLS(req *http.Request, p string, a int64, c int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return m.Do(req, p, a, c)
}
func TestJournalActualAntigravityForwardProtocolToDurableUsage(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprint(stream), func(t *testing.T) {
			f := newFixture(t)
			f.cfg.Gateway.MaxLineSize = 2 * 1024 * 1024
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(middleware.RequestLogger(), middleware.ClientRequestID())
			transport := &memoryAntigravity{}
			account := &service.Account{ID: 3, Type: service.AccountTypeOAuth, Platform: service.PlatformAntigravity, Name: "synthetic-account", Concurrency: 1, Credentials: map[string]any{"access_token": "SYNTHETIC_NOT_A_REAL_CREDENTIAL", "project_id": "synthetic-project", "model_mapping": map[string]any{"gemini-2.5-flash": "gemini-2.5-flash"}}}
			group := int64(1)
			api := &service.APIKey{ID: 2, UserID: 1, Quota: 100, GroupID: &group, Group: &service.Group{ID: 1, Platform: service.PlatformAntigravity, RateMultiplier: 1}}
			var requestCtx context.Context
			r.POST("/synthetic/forward", func(c *gin.Context) {
				requestCtx = c.Request.Context()
				body := []byte(`{"contents":[{"role":"user","parts":[{"text":"synthetic input"}]}]}`)
				action := "generateContent"
				if stream {
					action = "streamGenerateContent"
				}
				result, e := service.LocalJournalAntigravity(f.cfg, transport).ForwardGemini(requestCtx, c, account, "gemini-2.5-flash", action, stream, body, false)
				check(t, e)
				if result == nil || result.Usage.InputTokens+result.Usage.CacheReadInputTokens != 1000 || result.Usage.OutputTokens != 2000 {
					t.Fatalf("actual usage parser mismatch: %+v", result)
				}
				g, loadErr := service.LocalJournalGatewayWithPricingFile(f.cfg, repository.NewUsageBillingRepository(nil, f.db), "../../resources/model-pricing/model_prices_and_context_window.json")
				check(t, loadErr)
				in := &service.RecordUsageInput{Result: result, APIKey: api, User: &service.User{ID: 1}, Account: account, APIKeyService: rejectLegacyQuota{}}
				check(t, g.RecordUsage(requestCtx, in))
				check(t, g.RecordUsage(requestCtx, in))
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/synthetic/forward", strings.NewReader(`{}`)))
			if rec.Code != 200 || transport.calls != 1 {
				t.Fatal(rec.Code, transport.calls, rec.Body.String())
			}
			_, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
			check(t, e)
			var ok bool
			check(t, f.db.QueryRow(`SELECT (SELECT count(*)=1 AND bool_and(state='done') FROM usage_recovery_journal_v1) AND (SELECT count(*)=1 AND bool_and(input_tokens+cache_read_tokens=1000 AND output_tokens=2000) FROM usage_logs) AND (SELECT 100-balance FROM users WHERE id=1)=(SELECT quota_used FROM api_keys WHERE id=2) AND (SELECT quota_used>0 FROM api_keys WHERE id=2)`).Scan(&ok))
			if !ok {
				t.Fatal("protocol→actual billing→worker mismatch")
			}
			note(t, map[string]any{"actual_Antigravity_ForwardGemini": true, "stream": stream, "actual_response_parser": true, "actual_RecordUsage_repository_worker": true, "transport": "in-memory HTTPUpstream; no socket/DNS/real token", "upstream_call_count": transport.calls, "input_including_cache": 1000, "output": 2000, "financial_replay": false})
		})
	}
}
