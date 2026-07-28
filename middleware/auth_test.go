package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUserAuthWritesSessionGroupToRelayContextKeys(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.BrowserSession{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, db.Create(&model.User{
		Id: 1, Username: "admin", Role: common.RoleAdminUser,
		Status: common.UserStatusEnabled, Group: "user:admin",
	}).Error)
	sessionID, err := model.CreateBrowserSession(1, time.Now().Unix())
	require.NoError(t, err)

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	router.GET(
		"/test",
		func(c *gin.Context) {
			session := sessions.Default(c)
			session.Set("username", "admin")
			session.Set("role", common.RoleAdminUser)
			session.Set("id", 1)
			session.Set("status", common.UserStatusEnabled)
			session.Set("group", "user:admin")
			session.Set(constant.SessionKeyBrowserSessionID, sessionID)
			require.NoError(t, session.Save())
			c.Next()
		},
		UserAuth(),
		func(c *gin.Context) {
			assert.Equal(t, "user:admin", common.GetContextKeyString(c, constant.ContextKeyUserGroup))
			assert.Equal(t, "user:admin", common.GetContextKeyString(c, constant.ContextKeyUsingGroup))
			c.Status(http.StatusNoContent)
		},
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/test", nil)
	request.Header.Set("New-Api-User", "1")
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestUserAuthRejectsRevokedCookieSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.BrowserSession{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, db.Create(&model.User{
		Id: 7, Username: "revoked", Role: common.RoleCommonUser,
		Status: common.UserStatusEnabled, Group: "default", SessionVersion: 2,
	}).Error)
	sessionID, err := model.CreateBrowserSession(7, time.Now().Unix())
	require.NoError(t, err)

	handlerCalled := false
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	router.GET("/test", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("username", "revoked")
		session.Set("role", common.RoleCommonUser)
		session.Set("id", 7)
		session.Set("status", common.UserStatusEnabled)
		session.Set("group", "default")
		session.Set("session_version", int64(1))
		session.Set(constant.SessionKeyBrowserSessionID, sessionID)
		require.NoError(t, session.Save())
		c.Next()
	}, UserAuth(), func(c *gin.Context) { handlerCalled = true })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/test", nil)
	request.Header.Set("New-Api-User", "7")
	router.ServeHTTP(recorder, request)

	assert.False(t, handlerCalled)
	assert.Equal(t, http.StatusUnauthorized, recorder.Code)
}

func TestAdminAuthRejectsDemotedCookieSessionUsingCurrentDatabaseRole(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.BrowserSession{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
	require.NoError(t, db.Create(&model.User{
		Id:       42,
		Username: "demoted-admin",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}).Error)
	sessionID, err := model.CreateBrowserSession(42, time.Now().Unix())
	require.NoError(t, err)

	handlerCalled := false
	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	router.GET(
		"/admin",
		func(c *gin.Context) {
			session := sessions.Default(c)
			session.Set("username", "demoted-admin")
			session.Set("role", common.RoleAdminUser)
			session.Set("id", 42)
			session.Set("status", common.UserStatusEnabled)
			session.Set("group", "default")
			session.Set(constant.SessionKeyBrowserSessionID, sessionID)
			require.NoError(t, session.Save())
			c.Next()
		},
		AdminAuth(),
		func(c *gin.Context) {
			handlerCalled = true
			c.Status(http.StatusNoContent)
		},
	)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)
	request.Header.Set("New-Api-User", "42")
	router.ServeHTTP(recorder, request)

	assert.False(t, handlerCalled)
	assert.Equal(t, http.StatusOK, recorder.Code)
}
