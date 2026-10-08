package router

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/controller"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/active_request_setting"
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
	oldRedis := common.RedisEnabled
	oldLogDB := model.LOG_DB
	common.RedisEnabled = false
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.UserSession{}, &model.UserAccessToken{}, &model.Option{}, &model.AuditLog{}))
	require.NoError(t, db.Create(&model.User{
		Id:          1,
		Username:    "admin",
		Role:        common.RoleAdminUser,
		Status:      common.UserStatusEnabled,
		AuthVersion: 1,
	}).Error)
	model.DB = db
	model.LOG_DB = db

	t.Cleanup(func() {
		service.GlobalActiveRequestTracker = oldTracker
		setting.CompletedRetentionSeconds = oldRetentionSeconds
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedis
	})

	router := gin.New()
	now := time.Now().Unix()
	session := &model.UserSession{SID: "active-request-route-test", UserID: 1, Version: 1, UserAuthVersion: 1, Status: model.UserSessionStatusActive, RefreshHash: "test-refresh-hash", LoginMethod: "password", LastActiveAt: now, ExpiresAt: now + 3600}
	require.NoError(t, model.CreateUserSession(session))
	token, _, err := service.IssueAccessToken(service.AuthIdentity{UserID: 1, SessionID: session.SID, UserAuthVersion: 1, SessionVersion: 1})
	require.NoError(t, err)
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
	request.Header.Set("Authorization", "Bearer "+token)

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code, "GET /api/active-requests must be registered and accept an authenticated admin")
}
