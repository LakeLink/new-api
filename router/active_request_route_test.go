package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

func TestActiveRequestsRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldTracker := service.GlobalActiveRequestTracker
	service.GlobalActiveRequestTracker = &service.ActiveRequestTracker{}

	setting := active_request_setting.GetActiveRequestSetting()
	oldRetentionSeconds := setting.CompletedRetentionSeconds
	setting.CompletedRetentionSeconds = 10

	t.Cleanup(func() {
		service.GlobalActiveRequestTracker = oldTracker
		setting.CompletedRetentionSeconds = oldRetentionSeconds
	})

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test"))))
	router.Use(func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "admin")
		session.Set("role", common.RoleAdminUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		c.Next()
	})
	SetApiRouter(router)
	router.NoRoute(func(c *gin.Context) {
		if strings.HasPrefix(c.Request.RequestURI, "/api") {
			controller.RelayNotFound(c)
			return
		}
		c.Status(http.StatusNotFound)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/active-requests", nil)
	request.Header.Set("New-Api-User", "1")

	router.ServeHTTP(recorder, request)

	if recorder.Code == http.StatusNotFound {
		t.Fatalf("GET /api/active-requests returned 404; route is not registered")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET /api/active-requests status = %d, want %d", recorder.Code, http.StatusOK)
	}
}
