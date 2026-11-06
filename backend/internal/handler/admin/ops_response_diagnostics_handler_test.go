package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResponseDiagnosticHandlerValidatesFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{"status=success", "user_id=0", "account_id=-1", "start_time=invalid", "start_time=2026-10-18T00:00:00Z&end_time=2026-10-17T00:00:00Z"} {
		t.Run(query, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/response-diagnostics?"+query, nil)
			newIngressRejectHandlerForTest().ListResponseDiagnostics(c)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
		})
	}
}
func TestResponseDiagnosticHandlerUnavailable(t *testing.T) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/response-diagnostics", nil)
	NewOpsHandler(nil).ListResponseDiagnostics(c)
	require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
}
