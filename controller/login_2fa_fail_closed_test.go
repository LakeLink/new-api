package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoginFailsClosedWhenTwoFALookupFails(t *testing.T) {
	db := setupModelListControllerTestDB(t)

	password := "CurrentPassword123"
	hashedPassword, err := common.Password2Hash(password)
	require.NoError(t, err)
	user := &model.User{
		Username: "twofa-db-error",
		Password: hashedPassword,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(user).Error)
	// setupModelListControllerTestDB deliberately does not migrate TwoFA. The
	// password lookup succeeds, while the second-factor lookup returns a real DB
	// error (missing table), reproducing the fail-open boundary deterministically.

	originalPasswordLoginEnabled := common.PasswordLoginEnabled
	common.PasswordLoginEnabled = true
	t.Cleanup(func() { common.PasswordLoginEnabled = originalPasswordLoginEnabled })

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-session-secret"))))
	router.POST("/login", Login)

	body := []byte(`{"username":"twofa-db-error","password":"CurrentPassword123"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/login", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
}

func TestPasswordToTwoFATransitionRevokesPreviousBrowserSession(t *testing.T) {
	require.NoError(t, i18n.Init())
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TwoFA{}))

	password := "CurrentPassword123"
	hashedPassword, err := common.Password2Hash(password)
	require.NoError(t, err)
	user := &model.User{
		Username: "twofa-session-rotation",
		Password: hashedPassword,
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "twofa-session-rotation-aff",
	}
	require.NoError(t, db.Create(user).Error)
	require.NoError(t, db.Create(&model.TwoFA{
		UserId:    user.Id,
		Secret:    "test-secret",
		IsEnabled: true,
	}).Error)
	now := time.Now().Unix()
	browserSessionID, err := model.CreateBrowserSession(user.Id, now)
	require.NoError(t, err)

	originalPasswordLoginEnabled := common.PasswordLoginEnabled
	common.PasswordLoginEnabled = true
	t.Cleanup(func() {
		common.PasswordLoginEnabled = originalPasswordLoginEnabled
	})

	router := gin.New()
	router.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("twofa-session-rotation-secret")),
	))
	router.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set("username", user.Username)
		session.Set("session_version", user.SessionVersion)
		session.Set(constant.SessionKeyBrowserSessionID, browserSessionID)
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.POST("/login", Login)

	seedRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		seedRecorder,
		httptest.NewRequest(http.MethodGet, "/seed", nil),
	)
	sessionCookie := browserSessionCookie(t, seedRecorder)

	body := []byte(
		`{"username":"twofa-session-rotation","password":"CurrentPassword123"}`,
	)
	loginRecorder := httptest.NewRecorder()
	loginRequest := httptest.NewRequest(
		http.MethodPost,
		"/login",
		bytes.NewReader(body),
	)
	loginRequest.Header.Set("Content-Type", "application/json")
	loginRequest.AddCookie(sessionCookie)
	router.ServeHTTP(loginRecorder, loginRequest)

	require.Equal(t, http.StatusOK, loginRecorder.Code)
	assert.Contains(t, loginRecorder.Body.String(), `"require_2fa":true`)
	resolved, err := model.GetUserByBrowserSession(
		user.Id,
		browserSessionID,
		now,
	)
	require.NoError(t, err)
	assert.Nil(t, resolved)
}

func TestTwoFACompletionRejectsAccountDisabledAfterPasswordStep(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	user := &model.User{
		Username:       "twofa-disabled-between-steps",
		Role:           common.RoleCommonUser,
		Status:         common.UserStatusDisabled,
		Group:          "default",
		AffCode:        "twofa-disabled-between-steps-aff",
		SessionVersion: 7,
	}
	require.NoError(t, db.Create(user).Error)

	router := gin.New()
	router.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("twofa-disabled-between-steps-secret")),
	))
	router.GET("/seed", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("pending_user_id", user.Id)
		session.Set("pending_login_at", time.Now().Unix())
		session.Set("pending_session_version", user.SessionVersion)
		require.NoError(t, session.Save())
		c.Status(http.StatusNoContent)
	})
	router.POST("/finish", Verify2FALogin)

	seedRecorder := httptest.NewRecorder()
	router.ServeHTTP(
		seedRecorder,
		httptest.NewRequest(http.MethodGet, "/seed", nil),
	)
	sessionCookie := browserSessionCookie(t, seedRecorder)

	finishRecorder := httptest.NewRecorder()
	finishRequest := httptest.NewRequest(
		http.MethodPost,
		"/finish",
		bytes.NewReader([]byte(`{"code":"123456"}`)),
	)
	finishRequest.Header.Set("Content-Type", "application/json")
	finishRequest.AddCookie(sessionCookie)
	router.ServeHTTP(finishRecorder, finishRequest)

	assert.Equal(t, http.StatusOK, finishRecorder.Code)
	assert.Contains(t, finishRecorder.Body.String(), `"success":false`)
	assert.Contains(t, finishRecorder.Body.String(), "用户已被禁用")

	var activeSessions int64
	require.NoError(t, db.Model(&model.BrowserSession{}).
		Where("user_id = ?", user.Id).
		Count(&activeSessions).Error)
	assert.Zero(t, activeSessions)
}
