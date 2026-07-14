package controller

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestUniversalVerifyAcceptsCurrentPasswordForEnrollmentStepUp(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TwoFA{}, &model.TwoFABackupCode{}, &model.PasskeyCredential{}, &model.Log{}))
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
	})
	hash, err := common.Password2Hash("CurrentPassword123")
	require.NoError(t, err)
	user := model.User{
		Username: "step-up-user", Password: hash,
		Status: common.UserStatusEnabled, Role: common.RoleCommonUser, Group: "default",
	}
	require.NoError(t, db.Create(&user).Error)

	router := gin.New()
	router.Use(sessions.Sessions("session", cookie.NewStore([]byte("test-secret"))))
	router.POST("/verify", func(c *gin.Context) {
		c.Set("id", user.Id)
		c.Next()
	}, UniversalVerify)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(`{"method":"password","password":"CurrentPassword123"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.True(t, response.Success)

	wrongPasswordRecorder := httptest.NewRecorder()
	wrongPasswordRequest := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(`{"method":"password","password":"WrongPassword123"}`))
	wrongPasswordRequest.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(wrongPasswordRecorder, wrongPasswordRequest)

	var wrongPasswordResponse struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(wrongPasswordRecorder.Body.Bytes(), &wrongPasswordResponse))
	assert.False(t, wrongPasswordResponse.Success)
	assert.Contains(t, wrongPasswordResponse.Message, "密码")
}

func TestGet2FAStatusReportsPasswordAvailabilityWithoutExposingPassword(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TwoFA{}, &model.TwoFABackupCode{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })

	users := []model.User{
		{Username: "password-user", Password: "hashed-password", Status: common.UserStatusEnabled, AffCode: "password-aff"},
		{Username: "oauth-only-user", Password: "", Status: common.UserStatusEnabled, AffCode: "oauth-aff"},
	}
	for index := range users {
		require.NoError(t, db.Create(&users[index]).Error)
	}

	for index, expected := range []bool{true, false} {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Set("id", users[index].Id)
		context.Request = httptest.NewRequest(http.MethodGet, "/api/user/2fa/status", nil)

		Get2FAStatus(context)

		assert.Equal(t, http.StatusOK, recorder.Code)
		var response struct {
			Success bool `json:"success"`
			Data    struct {
				HasPassword bool `json:"has_password"`
			} `json:"data"`
		}
		require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
		assert.True(t, response.Success)
		assert.Equal(t, expected, response.Data.HasPassword)
		assert.NotContains(t, recorder.Body.String(), "hashed-password")
	}
}
