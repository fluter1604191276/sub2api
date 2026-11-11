package routes

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type iqPresentationUsers struct {
	service.UserRepository
	users map[int64]*service.User
}

func (r *iqPresentationUsers) GetByID(_ context.Context, id int64) (*service.User, error) {
	u := r.users[id]
	if u == nil {
		return nil, service.ErrUserNotFound
	}
	copy := *u
	return &copy, nil
}
func (*iqPresentationUsers) GetUserAvatar(context.Context, int64) (*service.UserAvatar, error) {
	return nil, nil
}
func (*iqPresentationUsers) UpdateUserLastActiveAt(context.Context, int64, time.Time) error {
	return nil
}

type iqPresentationGroups struct{ service.GroupRepository }

func (*iqPresentationGroups) ListActive(context.Context) ([]service.Group, error) {
	return []service.Group{
		{ID: 1, Name: "public", Platform: service.PlatformOpenAI, Status: service.StatusActive},
		{ID: 2, Name: "private", Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true},
	}, nil
}
func (r *iqPresentationGroups) ListActiveByPlatform(ctx context.Context, _ string) ([]service.Group, error) {
	return r.ListActive(ctx)
}
func (*iqPresentationGroups) GetByID(_ context.Context, id int64) (*service.Group, error) {
	return &service.Group{ID: id, Platform: service.PlatformOpenAI, Status: service.StatusActive}, nil
}

type iqPresentationSettings struct{ service.SettingRepository }

func (*iqPresentationSettings) GetValue(context.Context, string) (string, error) {
	settings := service.DefaultIQSettings()
	settings.GroupIDs = []int64{1}
	raw, err := json.Marshal(settings)
	return string(raw), err
}

type iqPresentationSubscriptions struct {
	service.UserSubscriptionRepository
}

func (*iqPresentationSubscriptions) ListActiveByUserID(context.Context, int64) ([]service.UserSubscription, error) {
	return nil, nil
}

type iqPresentationRepo struct {
	service.OpenAIIQRepository
	err              error
	diagnosticsCalls int
	allowed          []int64
	page, pageSize   int
	claimedGroup     int64
	requestedBy      int64
}

func (*iqPresentationRepo) Identity(context.Context) (*service.IQIdentity, error) {
	return &service.IQIdentity{UserID: 123, Balance: 1}, nil
}
func (r *iqPresentationRepo) ClaimManual(_ context.Context, _ service.IQSettings, groupID, requestedBy int64) (*service.IQRun, error) {
	r.claimedGroup, r.requestedBy = groupID, requestedBy
	return &service.IQRun{ID: 501, Trigger: service.IQTriggerManual}, nil
}
func (*iqPresentationRepo) RunResults(context.Context, int64) ([]service.IQResult, error) {
	return nil, nil
}
func (*iqPresentationRepo) FinishRun(context.Context, int64) error { return nil }

func (r *iqPresentationRepo) LatestValid(_ context.Context, allowed, _ []int64, _, _, _ string) ([]service.IQPublicResult, error) {
	r.allowed = slices.Clone(allowed)
	if r.err != nil {
		return nil, r.err
	}
	checked := time.Date(2026, 11, 6, 1, 0, 0, 0, time.UTC)
	results := make([]service.IQPublicResult, 0)
	for _, id := range allowed {
		results = append(results, service.IQPublicResult{GroupID: id, GroupName: "visible group", Model: "gpt-6-astra", Question: "original question", ExpectedAnswer: "15", ModelAnswer: "15", Status: "correct", Output: "FINAL_ANSWER: 15", CheckedAt: &checked})
	}
	return results, nil
}
func (r *iqPresentationRepo) Diagnostics(_ context.Context, page, pageSize int) (*service.IQDiagnosticsPage, error) {
	r.diagnosticsCalls++
	r.page, r.pageSize = page, pageSize
	results := make([]service.IQDiagnosticResult, 0)
	requestID := "client:iq-admin-only"
	for i, status := range []string{"correct", "incorrect", "error", "skipped", "pending"} {
		results = append(results, service.IQDiagnosticResult{ID: int64(i + 1), RunID: 1,
			IQResult:    service.IQResult{GroupID: int64(i + 1), Status: status, Error: "operator diagnostic"},
			Reservation: &service.IQDiagnosticReservation{ReservedUSD: .5, RequestID: &requestID}})
	}
	return &service.IQDiagnosticsPage{Page: page, PageSize: pageSize, Total: 1, Runs: []service.IQDiagnosticRun{{ID: 1, Status: "complete", Results: results}}}, nil
}

func iqPresentationRouter(t *testing.T, repo *iqPresentationRepo) (*gin.Engine, string, string) {
	return iqPresentationRouterWithSettings(t, repo, nil)
}

func iqPresentationRouterWithSettings(t *testing.T, repo *iqPresentationRepo, settings service.SettingRepository) (*gin.Engine, string, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	users := &iqPresentationUsers{users: map[int64]*service.User{
		1: {ID: 1, Role: service.RoleUser, Status: service.StatusActive, TokenVersion: 1},
		2: {ID: 2, Role: service.RoleAdmin, Status: service.StatusActive, TokenVersion: 1},
	}}
	groups := &iqPresentationGroups{}
	cfg := &config.Config{JWT: config.JWTConfig{Secret: "iq-presentation-fixture-only-secret", ExpireHour: 1}}
	auth := service.NewAuthService(nil, users, nil, nil, cfg, nil, nil, nil, nil, nil, nil, nil, nil)
	userService := service.NewUserService(users, nil, nil, nil)
	keys := service.NewAPIKeyService(nil, users, groups, &iqPresentationSubscriptions{}, nil, nil, cfg)
	if settings != nil {
		// The manual fixture has no gateway/cache dependencies or HTTP work.
		keys = nil
	}
	iqService := service.NewOpenAIIQService(settings, groups, users, keys, repo, nil, nil, nil, cfg)
	t.Cleanup(iqService.Stop)
	iq := handler.NewOpenAIIQHandler(iqService)
	router := gin.New()
	v1 := router.Group("/api/v1")
	userRoutes := v1.Group("", gin.HandlerFunc(middleware.NewJWTAuthMiddleware(auth, userService, nil, nil)))
	userRoutes.GET("/openai-iq", iq.Get)
	admin := v1.Group("/admin", gin.HandlerFunc(middleware.NewAdminAuthMiddleware(auth, userService, nil, nil)))
	registerOpenAIIQRoutes(admin, &handler.Handlers{OpenAIIQ: iq})
	userToken, err := auth.GenerateToken(context.Background(), users.users[1])
	require.NoError(t, err)
	adminToken, err := auth.GenerateToken(context.Background(), users.users[2])
	require.NoError(t, err)
	return router, userToken, adminToken
}

func iqPresentationGET(router *gin.Engine, path, token string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestOpenAIIQHTTPPublicWhitelistAndAuthentication(t *testing.T) {
	repo := &iqPresentationRepo{}
	router, userToken, _ := iqPresentationRouter(t, repo)
	require.Equal(t, http.StatusUnauthorized, iqPresentationGET(router, "/api/v1/openai-iq", "").Code)
	w := iqPresentationGET(router, "/api/v1/openai-iq", userToken)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []int64{1}, repo.allowed, "private group must be excluded before query")
	var body struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	keys := func(values map[string]json.RawMessage) []string {
		out := make([]string, 0, len(values))
		for key := range values {
			out = append(out, key)
		}
		return out
	}
	require.ElementsMatch(t, []string{"enabled", "model", "question", "expected_answer", "interval_seconds", "results", "summary"}, keys(body.Data))
	var results []map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body.Data["results"], &results))
	require.Len(t, results, 1)
	require.ElementsMatch(t, []string{"group_id", "group_name", "rate_multiplier", "model", "question", "expected_answer", "model_answer", "output", "status", "checked_at"}, keys(results[0]))
	var summary map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(body.Data["summary"], &summary))
	require.ElementsMatch(t, []string{"total", "correct", "incorrect", "no_results"}, keys(summary))

	repo.err = errors.New("sentinel-private-request-account-budget-detail")
	w = iqPresentationGET(router, "/api/v1/openai-iq", userToken)
	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.NotContains(t, w.Body.String(), "sentinel-private")
	require.Contains(t, w.Body.String(), "Unable to load OpenAI IQ results")
}

func TestOpenAIIQHTTPDiagnosticsRequiresAdministrator(t *testing.T) {
	repo := &iqPresentationRepo{}
	router, userToken, adminToken := iqPresentationRouter(t, repo)
	path := "/api/v1/admin/openai-iq/diagnostics"
	require.Equal(t, http.StatusUnauthorized, iqPresentationGET(router, path, "").Code)
	w := iqPresentationGET(router, path, userToken)
	require.Equal(t, http.StatusForbidden, w.Code)
	require.Zero(t, repo.diagnosticsCalls, "denied user must not read history")
	require.NotContains(t, w.Body.String(), "operator diagnostic")
	w = iqPresentationGET(router, path+"?page=2&page_size=500", adminToken)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 2, repo.page)
	require.Equal(t, 50, repo.pageSize)
	var body struct {
		Data service.IQDiagnosticsPage `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Len(t, body.Data.Runs, 1)
	require.Len(t, body.Data.Runs[0].Results, 5)
	for i, status := range []string{"correct", "incorrect", "error", "skipped", "pending"} {
		require.Equal(t, status, body.Data.Runs[0].Results[i].Status)
	}
	require.Contains(t, w.Body.String(), "operator diagnostic")
	require.Contains(t, w.Body.String(), "client:iq-admin-only")
}

func TestOpenAIIQHTTPRecheckRequiresAdminAndCostConfirmation(t *testing.T) {
	router, userToken, adminToken := iqPresentationRouter(t, &iqPresentationRepo{})
	post := func(path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	path := "/api/v1/admin/openai-iq/groups/1/recheck"
	valid := `{"confirm_cost":true,"confirmed_limit_usd":0.1}`
	require.Equal(t, http.StatusUnauthorized, post(path, "", valid).Code)
	require.Equal(t, http.StatusForbidden, post(path, userToken, valid).Code)
	for _, body := range []string{`{}`, `{"confirm_cost":false,"confirmed_limit_usd":0.1}`, `{"confirm_cost":true}`, `not-json`} {
		require.Equal(t, http.StatusBadRequest, post(path, adminToken, body).Code, body)
	}
	for _, id := range []string{"0", "-1", "abc"} {
		require.Equal(t, http.StatusBadRequest, post("/api/v1/admin/openai-iq/groups/"+id+"/recheck", adminToken, valid).Code)
	}
}

func TestOpenAIIQHTTPRecheckAcceptsConfirmedAdministrator(t *testing.T) {
	repo := &iqPresentationRepo{}
	router, _, adminToken := iqPresentationRouterWithSettings(t, repo, &iqPresentationSettings{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/openai-iq/groups/1/recheck", strings.NewReader(`{"confirm_cost":true,"confirmed_limit_usd":0.1}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+adminToken)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusAccepted, w.Code)
	var body struct {
		Data struct {
			RunID int64 `json:"run_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, int64(501), body.Data.RunID)
	require.Equal(t, int64(1), repo.claimedGroup)
	require.Equal(t, int64(2), repo.requestedBy)
}

func TestOpenAIIQHTTPDiagnosticsRejectsOversizedOrMalformedPagination(t *testing.T) {
	for _, query := range []string{
		"page=1000001", "page=9223372036854775807", "page=18446744073709551615",
		"page=not-a-number", "page=2junk", "page_size=18446744073709551615", "page_size=bad",
	} {
		t.Run(query, func(t *testing.T) {
			repo := &iqPresentationRepo{}
			router, _, adminToken := iqPresentationRouter(t, repo)
			w := iqPresentationGET(router, "/api/v1/admin/openai-iq/diagnostics?"+query, adminToken)
			require.Equal(t, http.StatusBadRequest, w.Code)
			require.Zero(t, repo.diagnosticsCalls, "invalid pagination must not reach the repository")
		})
	}
	repo := &iqPresentationRepo{}
	router, _, adminToken := iqPresentationRouter(t, repo)
	w := iqPresentationGET(router, "/api/v1/admin/openai-iq/diagnostics?page=1000000&page_size=500", adminToken)
	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, 1000000, repo.page)
	require.Equal(t, 50, repo.pageSize)
}
