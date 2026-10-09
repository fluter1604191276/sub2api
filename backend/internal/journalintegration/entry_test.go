//go:build localjournal

package journalintegration

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJournalRegisteredRoutesMiddlewareToActualBilling(t *testing.T) {
	paths := []string{"/v1/messages", "/v1/responses", "/v1/chat/completions", "/responses", "/chat/completions", "/backend-api/codex/responses", "/v1beta/models/gemini-2.5-pro:generateContent", "/antigravity/v1/messages", "/antigravity/v1beta/models/gemini-2.5-pro:generateContent", "/v1/images/generations", "/v1/embeddings", "/v1/videos", "/v1/web_search"}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			f := newFixture(t)
			gin.SetMode(gin.TestMode)
			r := gin.New()
			r.Use(middleware.RequestLogger())
			var captured context.Context
			r.Use(func(c *gin.Context) { c.Next(); captured = c.Request.Context() })
			// Real route registration and middleware. Synthetic auth aborts before any
			// scheduling/Forward. Google routes use actual auth and reject missing key.
			routes.RegisterGatewayRoutes(r, &handler.Handlers{}, func(c *gin.Context) { c.AbortWithStatus(204) }, nil, nil, nil, nil, nil, f.cfg)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"synthetic"}`))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			if rec.Code == 404 || captured == nil {
				t.Fatalf("route absent: %d", rec.Code)
			}
			id, _ := captured.Value(ctxkey.ClientRequestID).(string)
			if id == "" || rec.Header().Get("X-Client-Request-ID") != id {
				t.Fatal("real middleware did not propagate billing event key")
			}
			group := int64(1)
			api := &service.APIKey{ID: 2, UserID: 1, GroupID: &group, Group: &service.Group{ID: 1, RateMultiplier: 1, Platform: service.PlatformAnthropic}, Quota: 100}
			account := &service.Account{ID: 3, Type: service.AccountTypeOAuth, Platform: service.PlatformAnthropic}
			if strings.Contains(path, "antigravity") {
				account.Platform = service.PlatformAntigravity
				api.Group.Platform = service.PlatformAntigravity
			}
			in := &service.RecordUsageInput{Result: &service.ForwardResult{RequestID: "synthetic-upstream-event", Model: "claude-sonnet-4", Usage: service.ClaudeUsage{InputTokens: 1000000}}, APIKey: api, User: &service.User{ID: 1}, Account: account, APIKeyService: rejectLegacyQuota{}}
			g := service.LocalJournalGateway(f.cfg, repository.NewUsageBillingRepository(nil, f.db))
			check(t, g.RecordUsage(captured, in))
			check(t, g.RecordUsage(captured, in))
			_, e := repository.NewUsageJournalRepository(f.db).ProcessNext(context.Background())
			check(t, e)
			f.assertOnce(t, "client:"+id, true)
			note(t, map[string]any{"registered_route": path, "actual_client_request_id_middleware": true, "actual_RecordUsage_same_event_twice": true, "billing_key_prefix": "client:", "auth_aborted_before_forward": true, "HTTP_handler_forward_not_executed": true, "status": rec.Code})
		})
	}
}
