package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type priorityAdminStub struct {
	service.AdminService
	previews int
	applies  int
}

func (s *priorityAdminStub) PreviewAccountPriorityConversion(context.Context, []int64) (*service.AccountPriorityConversionResult, error) {
	s.previews++
	return &service.AccountPriorityConversionResult{Rows: []service.AccountPriorityConversionRow{}}, nil
}
func (s *priorityAdminStub) ApplyAccountPriorityConversion(context.Context, []service.AccountPriorityConfirmation) (*service.AccountPriorityConversionResult, error) {
	s.applies++
	return &service.AccountPriorityConversionResult{Rows: []service.AccountPriorityConversionRow{}, Updated: 1}, nil
}

func TestAccountPriorityAdminOnlyAndRequestBoundaries(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, test := range []struct {
		role, path, body          string
		status, previews, applies int
	}{
		{"", "preview", `{"account_ids":[1]}`, 403, 0, 0},
		{service.RoleUser, "preview", `{"account_ids":[1]}`, 403, 0, 0},
		{service.RoleUser, "apply", `{"accounts":[{"account_id":1,"rate_multiplier":"2.0070","old_priority":50}]}`, 403, 0, 0},
		{service.RoleAdmin, "preview", `{"account_ids":[]}`, 400, 0, 0},
		{service.RoleAdmin, "preview", `{"account_ids":[0]}`, 400, 0, 0},
		{service.RoleAdmin, "preview", `{"account_ids":[1,2]}`, 200, 1, 0},
		{service.RoleAdmin, "apply", `{"accounts":[]}`, 400, 0, 0},
		{service.RoleAdmin, "apply", `{"accounts":[{"account_id":1,"rate_multiplier":"2.0070","old_priority":50}]}`, 200, 0, 1},
	} {
		t.Run(test.role+test.path+test.body, func(t *testing.T) {
			svc := &priorityAdminStub{}
			handler := &AccountHandler{adminService: svc}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if test.role != "" {
					c.Set(string(middleware.ContextKeyUserRole), test.role)
				}
			})
			r.POST("/preview", handler.PreviewPriorityConversion)
			r.POST("/apply", handler.ApplyPriorityConversion)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/"+test.path, strings.NewReader(test.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, test.status, w.Code)
			require.Equal(t, test.previews, svc.previews)
			require.Equal(t, test.applies, svc.applies)
		})
	}
}
