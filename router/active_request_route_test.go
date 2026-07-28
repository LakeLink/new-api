package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestActiveRequestsRouteIsRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)

	oldTracker := service.GlobalActiveRequestTracker
	service.GlobalActiveRequestTracker = &service.ActiveRequestTracker{}

	setting := active_request_setting.GetActiveRequestSetting()
	oldRetentionSeconds := setting.CompletedRetentionSeconds
	setting.CompletedRetentionSeconds = 10
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.BrowserSession{}))
	require.NoError(t, db.Create(&model.User{
		Id:       1,
		Username: "admin",
		Role:     common.RoleAdminUser,
		Status:   common.UserStatusEnabled,
	}).Error)
	model.DB = db
	browserSessionID, err := model.CreateBrowserSession(1, time.Now().Unix())
	require.NoError(t, err)

	t.Cleanup(func() {
		service.GlobalActiveRequestTracker = oldTracker
		setting.CompletedRetentionSeconds = oldRetentionSeconds
		model.DB = oldDB
	})

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test"))))
	router.Use(func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "admin")
		session.Set("role", common.RoleAdminUser)
		session.Set("id", 1)
		session.Set("status", common.UserStatusEnabled)
		session.Set("session_version", int64(0))
		session.Set(constant.SessionKeyBrowserSessionID, browserSessionID)
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
