package controller

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sort"
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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func telegramAuthorizationBody(
	t *testing.T,
	token string,
	telegramID int64,
) []byte {
	t.Helper()

	authDate := time.Now().Unix()
	signedFields := map[string]string{
		"id":         strconv.FormatInt(telegramID, 10),
		"first_name": "Test",
		"username":   "telegram_test",
		"auth_date":  strconv.FormatInt(authDate, 10),
	}
	keys := make([]string, 0, len(signedFields))
	for key := range signedFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	checkLines := make([]string, 0, len(keys))
	for _, key := range keys {
		checkLines = append(checkLines, key+"="+signedFields[key])
	}
	secret := sha256.Sum256([]byte(token))
	mac := hmac.New(sha256.New, secret[:])
	_, err := mac.Write([]byte(strings.Join(checkLines, "\n")))
	require.NoError(t, err)

	body, err := common.Marshal(gin.H{
		"id":         telegramID,
		"first_name": "Test",
		"username":   "telegram_test",
		"auth_date":  authDate,
		"hash":       hex.EncodeToString(mac.Sum(nil)),
	})
	require.NoError(t, err)
	return body
}

func TestTelegramBindAcceptsSameOriginJSONCallback(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.BuiltInOAuthIdentity{},
		&model.AuthenticationToken{},
	))

	user := model.User{
		Username: "telegram-json-bind",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "telegram-json-bind-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	browserSessionID, err := model.CreateBrowserSession(
		user.Id,
		time.Now().Unix(),
	)
	require.NoError(t, err)

	oldEnabled := common.TelegramOAuthEnabled
	oldToken := common.TelegramBotToken
	common.TelegramOAuthEnabled = true
	common.TelegramBotToken = "telegram-bind-test-token"
	t.Cleanup(func() {
		common.TelegramOAuthEnabled = oldEnabled
		common.TelegramBotToken = oldToken
	})

	body := telegramAuthorizationBody(
		t,
		common.TelegramBotToken,
		123456789,
	)

	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("telegram-json-bind-session-secret")),
	))
	engine.POST("/bind", func(c *gin.Context) {
		session := sessions.Default(c)
		session.Set("id", user.Id)
		session.Set("session_version", user.SessionVersion)
		session.Set(constant.SessionKeyBrowserSessionID, browserSessionID)
		TelegramBind(c)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/bind",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	taken, err := model.IsTelegramIdAlreadyTaken("123456789")
	require.NoError(t, err)
	assert.True(t, taken)
}

func TestTelegramLoginAcceptsSameOriginJSONCallback(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.BuiltInOAuthIdentity{},
		&model.AuthenticationToken{},
	))

	user := model.User{
		Username: "telegram-json-login",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
		AffCode:  "telegram-json-login-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	const telegramID = int64(987654321)
	require.NoError(t, db.Transaction(func(tx *gorm.DB) error {
		return model.BindBuiltInOAuthIdentityWithTx(
			tx,
			model.BuiltInOAuthProviderTelegram,
			strconv.FormatInt(telegramID, 10),
			user.Id,
		)
	}))

	oldEnabled := common.TelegramOAuthEnabled
	oldToken := common.TelegramBotToken
	common.TelegramOAuthEnabled = true
	common.TelegramBotToken = "telegram-login-test-token"
	t.Cleanup(func() {
		common.TelegramOAuthEnabled = oldEnabled
		common.TelegramBotToken = oldToken
	})

	body := telegramAuthorizationBody(t, common.TelegramBotToken, telegramID)
	engine := gin.New()
	engine.Use(sessions.Sessions(
		"session",
		cookie.NewStore([]byte("telegram-json-login-session-secret")),
	))
	engine.POST("/api/oauth/telegram/login", TelegramLogin)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/telegram/login",
		bytes.NewReader(body),
	)
	request.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"success":true`)
	assert.NotEmpty(t, recorder.Header().Values("Set-Cookie"))

	var activeSessions int64
	require.NoError(t, db.Model(&model.BrowserSession{}).
		Where("user_id = ? AND expires_at > ?", user.Id, time.Now().Unix()).
		Count(&activeSessions).Error)
	assert.EqualValues(t, 1, activeSessions)

	replayRecorder := httptest.NewRecorder()
	replayRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/oauth/telegram/login",
		bytes.NewReader(body),
	)
	replayRequest.Header.Set("Content-Type", "application/json")
	engine.ServeHTTP(replayRecorder, replayRequest)

	assert.Equal(t, http.StatusOK, replayRecorder.Code)
	assert.Contains(t, replayRecorder.Body.String(), `"success":false`)
	assert.Contains(t, replayRecorder.Body.String(), "无效的请求")

	require.NoError(t, db.Model(&model.BrowserSession{}).
		Where("user_id = ? AND expires_at > ?", user.Id, time.Now().Unix()).
		Count(&activeSessions).Error)
	assert.EqualValues(t, 1, activeSessions)
}
