//go:build unit

package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type smartSchedulerGroupAdminServiceStub struct {
	service.AdminService
	createInput *service.CreateGroupInput
	updateInput *service.UpdateGroupInput
}

func (s *smartSchedulerGroupAdminServiceStub) CreateGroup(_ context.Context, input *service.CreateGroupInput) (*service.Group, error) {
	s.createInput = input
	return &service.Group{
		ID:                                101,
		Name:                              input.Name,
		Platform:                          service.NormalizeGroupPlatform(input.Platform),
		Status:                            service.StatusActive,
		RateMultiplier:                    input.RateMultiplier,
		SmartSchedulerEnabled:             input.SmartSchedulerEnabled,
		RecoveryProbeEnabled:              input.RecoveryProbeEnabled,
		RecoveryProbeMode:                 input.RecoveryProbeMode,
		RecoveryProbeModel:                input.RecoveryProbeModel,
		RecoveryProbeIntervalSeconds:      input.RecoveryProbeIntervalSeconds,
		RecoveryProbeAttemptsPerRound:     input.RecoveryProbeAttemptsPerRound,
		RecoveryProbeIdleThresholdSeconds: input.RecoveryProbeIdleThresholdSeconds,
		RecoveryProbeBackoffCapSeconds:    input.RecoveryProbeBackoffCapSeconds,
	}, nil
}

func (s *smartSchedulerGroupAdminServiceStub) UpdateGroup(_ context.Context, id int64, input *service.UpdateGroupInput) (*service.Group, error) {
	s.updateInput = input
	enabled := false
	if input.SmartSchedulerEnabled != nil {
		enabled = *input.SmartSchedulerEnabled
	}
	return &service.Group{
		ID:                    id,
		Name:                  input.Name,
		Platform:              service.PlatformOpenAI,
		Status:                service.StatusActive,
		RateMultiplier:        1,
		SmartSchedulerEnabled: enabled,
	}, nil
}

func setupSmartSchedulerGroupRouter(svc service.AdminService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handler := NewGroupHandler(svc, nil, nil, nil, nil)
	router.POST("/api/v1/admin/groups", handler.Create)
	router.PUT("/api/v1/admin/groups/:id", handler.Update)
	return router
}

func TestGroupHandlerCreateBindsAndReturnsSmartSchedulerEnabled(t *testing.T) {
	svc := &smartSchedulerGroupAdminServiceStub{}
	router := setupSmartSchedulerGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/groups",
		strings.NewReader(`{"name":"smart","platform":"openai","rate_multiplier":1,"smart_scheduler_enabled":true}`),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, svc.createInput)
	require.True(t, svc.createInput.SmartSchedulerEnabled)
	require.Contains(t, recorder.Body.String(), `"smart_scheduler_enabled":true`)
}

func TestGroupHandlerUpdateBindsAndReturnsSmartSchedulerEnabled(t *testing.T) {
	svc := &smartSchedulerGroupAdminServiceStub{}
	router := setupSmartSchedulerGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/groups/101",
		strings.NewReader(`{"name":"smart","smart_scheduler_enabled":true}`),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, svc.updateInput)
	require.NotNil(t, svc.updateInput.SmartSchedulerEnabled)
	require.True(t, *svc.updateInput.SmartSchedulerEnabled)
	require.Contains(t, recorder.Body.String(), `"smart_scheduler_enabled":true`)
}

func TestGroupHandlerCreateBindsAndReturnsRecoveryProbeConfig(t *testing.T) {
	svc := &smartSchedulerGroupAdminServiceStub{}
	router := setupSmartSchedulerGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/admin/groups",
		strings.NewReader(`{"name":"probe","platform":"anthropic","rate_multiplier":1,"recovery_probe_enabled":true,"recovery_probe_mode":"smart","recovery_probe_model":"claude-sonnet-4-6","recovery_probe_interval_seconds":900,"recovery_probe_attempts_per_round":2,"recovery_probe_idle_threshold_seconds":3600,"recovery_probe_backoff_cap_seconds":1800}`),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, svc.createInput)
	require.True(t, svc.createInput.RecoveryProbeEnabled)
	require.Equal(t, service.GroupRecoveryProbeModeSmart, svc.createInput.RecoveryProbeMode)
	require.Equal(t, "claude-sonnet-4-6", svc.createInput.RecoveryProbeModel)
	require.Equal(t, 2, svc.createInput.RecoveryProbeAttemptsPerRound)
	require.Contains(t, recorder.Body.String(), `"recovery_probe_enabled":true`)
	require.Contains(t, recorder.Body.String(), `"recovery_probe_model":"claude-sonnet-4-6"`)
}

func TestGroupHandlerUpdateKeepsRecoveryProbeFieldsTriState(t *testing.T) {
	svc := &smartSchedulerGroupAdminServiceStub{}
	router := setupSmartSchedulerGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/groups/101",
		strings.NewReader(`{"recovery_probe_interval_seconds":1200}`),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, svc.updateInput)
	require.NotNil(t, svc.updateInput.RecoveryProbeIntervalSeconds)
	require.Equal(t, 1200, *svc.updateInput.RecoveryProbeIntervalSeconds)
	require.Nil(t, svc.updateInput.RecoveryProbeEnabled)
	require.Nil(t, svc.updateInput.RecoveryProbeMode)
	require.Nil(t, svc.updateInput.RecoveryProbeModel)
	require.Nil(t, svc.updateInput.RecoveryProbeAttemptsPerRound)
	require.Nil(t, svc.updateInput.RecoveryProbeBackoffCapSeconds)
}

func TestGroupHandlerUpdateMapsExplicitNullPoolErrorPolicyToClearFlags(t *testing.T) {
	svc := &smartSchedulerGroupAdminServiceStub{}
	router := setupSmartSchedulerGroupRouter(svc)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPut,
		"/api/v1/admin/groups/101",
		strings.NewReader(`{"pool_mode_enabled":null,"pool_mode_retry_count":null,"pool_mode_retry_status_codes":null,"custom_error_codes_enabled":null,"custom_error_codes":null}`),
	)
	request.Header.Set("Content-Type", "application/json")

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	require.NotNil(t, svc.updateInput)
	require.Nil(t, svc.updateInput.PoolModeEnabled)
	require.Nil(t, svc.updateInput.PoolModeRetryCount)
	require.Nil(t, svc.updateInput.PoolModeRetryStatusCodes)
	require.Nil(t, svc.updateInput.CustomErrorCodesEnabled)
	require.Nil(t, svc.updateInput.CustomErrorCodes)
	require.True(t, svc.updateInput.PoolModeEnabledClear)
	require.True(t, svc.updateInput.PoolModeRetryCountClear)
	require.True(t, svc.updateInput.PoolModeRetryStatusCodesClear)
	require.True(t, svc.updateInput.CustomErrorCodesEnabledClear)
	require.True(t, svc.updateInput.CustomErrorCodesClear)
}
