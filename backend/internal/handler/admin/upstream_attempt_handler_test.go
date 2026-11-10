package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type upstreamAttemptHandlerRepo struct {
	service.UpstreamAttemptRepository
	listFilter *service.UpstreamAttemptFilter
	dailyStart time.Time
	dailyEnd   time.Time
}

func (r *upstreamAttemptHandlerRepo) List(_ context.Context, filter *service.UpstreamAttemptFilter) (*service.UpstreamAttemptList, error) {
	r.listFilter = filter
	return &service.UpstreamAttemptList{Items: []*service.UpstreamAttempt{}, Page: filter.Page, PageSize: filter.PageSize}, nil
}

func (r *upstreamAttemptHandlerRepo) DailyCosts(_ context.Context, _ *int64, start, end time.Time) ([]service.UpstreamAttemptDailyCost, error) {
	r.dailyStart, r.dailyEnd = start, end
	return []service.UpstreamAttemptDailyCost{}, nil
}

func TestUpstreamAttemptHandlerValidatesFilters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, path := range []string{
		"/admin/ops/upstream-attempts?source=unknown",
		"/admin/ops/upstream-attempts?account_id=0",
		"/admin/ops/upstream-attempts?start_time=bad",
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		NewUpstreamAttemptHandler(&upstreamAttemptHandlerRepo{}).List(c)
		require.Equal(t, http.StatusBadRequest, recorder.Code, path)
	}
}

func TestUpstreamAttemptHandlerListsAndSummarizes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := &upstreamAttemptHandlerRepo{}
	handler := NewUpstreamAttemptHandler(repo)
	for _, test := range []struct {
		path string
		call func(*gin.Context)
	}{
		{"/admin/ops/upstream-attempts?account_id=4&source=request&page=2&page_size=20", handler.List},
		{"/admin/ops/upstream-attempts/daily-costs?time_range=24h", handler.DailyCosts},
	} {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, test.path, nil)
		test.call(c)
		require.Equal(t, http.StatusOK, recorder.Code, test.path)
	}
	require.NotNil(t, repo.listFilter)
	require.Equal(t, "request", repo.listFilter.Source)
	require.False(t, repo.dailyStart.IsZero())
	require.False(t, repo.dailyEnd.IsZero())
}

func TestUpstreamAttemptHandlerRejectsBadPaging(t *testing.T) {
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
			repo := &upstreamAttemptHandlerRepo{}
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/admin/ops/upstream-attempts?"+query, nil)
			NewUpstreamAttemptHandler(repo).List(c)
			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Nil(t, repo.listFilter, "repository must not be reached")
		})
	}
}

func TestUpstreamAttemptHandlerNormalPagingUnchanged(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for query, want := range map[string][2]int{
		"":                          {1, 20},
		"page=2&page_size=20":       {2, 20},
		"page=10000&page_size=1000": {10000, 1000},
		"page=3&limit=5":            {3, 5},
	} {
		repo := &upstreamAttemptHandlerRepo{}
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, "/admin/ops/upstream-attempts?"+query, nil)
		NewUpstreamAttemptHandler(repo).List(c)
		require.Equal(t, http.StatusOK, recorder.Code, query)
		require.Equal(t, want[0], repo.listFilter.Page, query)
		require.Equal(t, want[1], repo.listFilter.PageSize, query)
	}
}
