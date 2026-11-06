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
