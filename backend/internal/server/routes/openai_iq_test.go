package routes

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	servermiddleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGatewayResponsesIQMarkerRequiresPrivateLoopbackHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := &config.Config{Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024, TextMaxBodySize: 1024 * 1024}}
	iq := service.NewOpenAIIQService(nil, nil, nil, nil, nil, nil, nil, nil, cfg)
	marked := false
	headerRemoved := false
	correlationID := ""
	router := gin.New()
	RegisterGatewayRoutes(router, &handler.Handlers{
		Gateway:       &handler.GatewayHandler{},
		OpenAIGateway: &handler.OpenAIGatewayHandler{},
		OpenAIIQ:      handler.NewOpenAIIQHandler(iq),
		AsyncImage:    handler.NewAsyncImageHandler(nil, nil),
	}, servermiddleware.APIKeyAuthMiddleware(func(c *gin.Context) {
		c.Next()
		marked = service.IsOpenAIIQRequest(c.Request.Context())
		headerRemoved = c.GetHeader("X-Sub2API-IQ-Request") == "" && c.GetHeader("X-Sub2API-IQ-Dispatch") == ""
		correlationID, _ = c.Request.Context().Value(ctxkey.ClientRequestID).(string)
	}), nil, nil, nil, nil, nil, cfg)

	for _, tc := range []struct {
		name, remote, token, dispatch string
		wantMarked                    bool
		wantCorrelation               string
	}{
		{"correct loopback", "127.0.0.1:1000", string(iqTokenForRouteTest(iq)), "7f7c19ba-903f-4142-b93e-f9f72387ba55", true, "iq-7f7c19ba-903f-4142-b93e-f9f72387ba55"},
		{"wrong token", "127.0.0.1:1000", "wrong", "7f7c19ba-903f-4142-b93e-f9f72387ba55", false, ""},
		{"nonloopback", "192.0.2.1:1000", string(iqTokenForRouteTest(iq)), "7f7c19ba-903f-4142-b93e-f9f72387ba55", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			marked, headerRemoved, correlationID = false, false, ""
			req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{}`))
			req.RemoteAddr = tc.remote
			req.Header.Set("X-Sub2API-IQ-Request", tc.token)
			req.Header.Set("X-Sub2API-IQ-Dispatch", tc.dispatch)
			writer := httptest.NewRecorder()
			router.ServeHTTP(writer, req)
			require.Equal(t, tc.wantMarked, marked)
			require.True(t, headerRemoved)
			if tc.wantCorrelation != "" {
				require.Equal(t, tc.wantCorrelation, correlationID)
				require.Equal(t, tc.wantCorrelation, writer.Header().Get("X-Client-Request-ID"))
			} else {
				require.False(t, strings.HasPrefix(correlationID, "iq-"))
			}
		})
	}
}

// The test stays in the routes package, so it cannot access the token field.
// The constructor's request client carries the same token only as its header.
func iqTokenForRouteTest(s *service.OpenAIIQService) []byte {
	return reflect.ValueOf(s).Elem().FieldByName("requestToken").Bytes()
}
