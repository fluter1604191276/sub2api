package handler

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type redactionRuleRepository struct{ rule *model.ErrorPassthroughRule }

func (r *redactionRuleRepository) List(context.Context) ([]*model.ErrorPassthroughRule, error) {
	return []*model.ErrorPassthroughRule{r.rule}, nil
}
func (r *redactionRuleRepository) GetByID(context.Context, int64) (*model.ErrorPassthroughRule, error) {
	return r.rule, nil
}
func (r *redactionRuleRepository) Create(_ context.Context, rule *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return rule, nil
}
func (r *redactionRuleRepository) Update(_ context.Context, rule *model.ErrorPassthroughRule) (*model.ErrorPassthroughRule, error) {
	return rule, nil
}
func (r *redactionRuleRepository) Delete(context.Context, int64) error { return nil }

func TestFailoverPassthroughHandlersRedactUpstreamSecrets(t *testing.T) {
	gin.SetMode(gin.TestMode)
	code := 422
	rule := &model.ErrorPassthroughRule{ID: 1, Enabled: true, ErrorCodes: []int{418}, MatchMode: model.MatchModeAny, PassthroughBody: true, ResponseCode: &code, SkipMonitoring: true}
	svc := service.NewErrorPassthroughService(&redactionRuleRepository{rule: rule}, nil)
	fail := &service.UpstreamFailoverError{StatusCode: 418, ResponseBody: []byte(`{"error":{"message":"invalid schema at https://mock.invalid/path?api_key=synthetic-query#token=synthetic-fragment"}}`)}
	tests := []struct {
		name   string
		invoke func(*gin.Context)
	}{
		{"anthropic", func(c *gin.Context) {
			(&GatewayHandler{errorPassthroughService: svc}).handleFailoverExhausted(c, fail, service.PlatformAnthropic, false)
		}},
		{"openai", func(c *gin.Context) {
			(&OpenAIGatewayHandler{errorPassthroughService: svc}).handleFailoverExhausted(c, fail, false)
		}},
		{"gemini", func(c *gin.Context) {
			(&GatewayHandler{errorPassthroughService: svc}).handleGeminiFailoverExhausted(c, fail)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			tt.invoke(c)
			require.Equal(t, code, rec.Code)
			body := rec.Body.String()
			require.Contains(t, body, "invalid schema")
			for _, secret := range []string{"mock.invalid", "synthetic-query", "synthetic-fragment"} {
				require.NotContains(t, body, secret)
			}
			require.Equal(t, true, c.MustGet(service.OpsSkipPassthroughKey))
		})
	}
}
