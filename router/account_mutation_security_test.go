package router

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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

func TestAccountCredentialMutationsRequireRecentVerification(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.BrowserSession{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	user := model.User{
		Username: "account-mutation-step-up",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "account-mutation-step-up-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	browserSessionID, err := model.CreateBrowserSession(
		user.Id,
		time.Now().Unix(),
	)
	require.NoError(t, err)

	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("account-mutation-step-up-secret")),
	))
	engine.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set("username", user.Username)
		session.Set("role", user.Role)
		session.Set("status", user.Status)
		session.Set("group", user.Group)
		session.Set("session_version", user.SessionVersion)
		session.Set(constant.SessionKeyBrowserSessionID, browserSessionID)
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	SetApiRouter(engine)

	seedRecorder := httptest.NewRecorder()
	engine.ServeHTTP(
		seedRecorder,
		httptest.NewRequest(http.MethodGet, "/seed", nil),
	)
	var sessionCookie *http.Cookie
	for _, responseCookie := range seedRecorder.Result().Cookies() {
		if responseCookie.Name == "session" {
			sessionCookie = responseCookie
			break
		}
	}
	require.NotNil(t, sessionCookie)

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{
			name:   "delete self",
			method: http.MethodDelete,
			path:   "/api/user/self",
		},
		{
			name:   "custom OAuth unbind",
			method: http.MethodDelete,
			path:   "/api/user/oauth/bindings/1",
		},
		{
			name:   "WeChat bind",
			method: http.MethodPost,
			path:   "/api/oauth/wechat/bind",
			body:   `{}`,
		},
		{
			name:   "Telegram bind",
			method: http.MethodPost,
			path:   "/api/oauth/telegram/bind",
			body:   `{}`,
		},
		{
			name:   "access token generation",
			method: http.MethodPost,
			path:   "/api/user/token",
		},
		{
			name:   "single API token reveal",
			method: http.MethodPost,
			path:   "/api/token/1/key",
		},
		{
			name:   "batch API token reveal",
			method: http.MethodPost,
			path:   "/api/token/batch/keys",
			body:   `{"ids":[1]}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(
				test.method,
				test.path,
				strings.NewReader(test.body),
			)
			request.Header.Set("New-Api-User", strconv.Itoa(user.Id))
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(sessionCookie)
			engine.ServeHTTP(recorder, request)

			assert.Equal(t, http.StatusForbidden, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "VERIFICATION_REQUIRED")
		})
	}

	var count int64
	require.NoError(t, db.Model(&model.User{}).
		Where("id = ?", user.Id).
		Count(&count).Error)
	assert.EqualValues(t, 1, count)
}
