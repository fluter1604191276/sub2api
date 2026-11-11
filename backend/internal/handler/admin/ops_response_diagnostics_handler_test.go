package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
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

type diagnosticPagingRepo struct {
	service.OpsRepository
	filter *service.OpsResponseDiagnosticFilter
}

func (r *diagnosticPagingRepo) InsertResponseDiagnostic(context.Context, *service.OpsResponseDiagnostic) error {
	return nil
}
func (r *diagnosticPagingRepo) ListResponseDiagnostics(_ context.Context, f *service.OpsResponseDiagnosticFilter) (*service.OpsResponseDiagnosticList, error) {
	r.filter = f
	return &service.OpsResponseDiagnosticList{Items: []*service.OpsResponseDiagnostic{}, Page: f.Page, PageSize: f.PageSize}, nil
}

func listDiagnosticsForPagingTest(query string) (*httptest.ResponseRecorder, *diagnosticPagingRepo) {
	repo := &diagnosticPagingRepo{}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/v1/admin/ops/response-diagnostics?"+query, nil)
	NewOpsHandler(service.NewOpsService(repo, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)).ListResponseDiagnostics(c)
	return recorder, repo
}

func TestResponseDiagnosticHandlerRejectsBadPaging(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, query := range []string{
		"page=9223372036854775807&page_size=20",
		"page=9223372036854775808",
		"page=10001",
		"page=0",
		"page=-1",
		"page=abc",
		"page_size=0",
		"page_size=-1",
		"page_size=1001",
		"page_size=9223372036854775807",
		"limit=abc",
	} {
		t.Run(query, func(t *testing.T) {
			recorder, repo := listDiagnosticsForPagingTest(query)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Nil(t, repo.filter, "repository must not be reached")
		})
	}
}

func TestResponseDiagnosticHandlerNormalPagingUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for query, want := range map[string][2]int{
		"":                          {1, 20},
		"page=2&page_size=20":       {2, 20},
		"page=10000&page_size=1000": {10000, 1000},
		"page=3&limit=5":            {3, 5},
	} {
		recorder, repo := listDiagnosticsForPagingTest(query)
		require.Equal(t, http.StatusOK, recorder.Code, query)
		require.Equal(t, want[0], repo.filter.Page, query)
		require.Equal(t, want[1], repo.filter.PageSize, query)
	}
}
