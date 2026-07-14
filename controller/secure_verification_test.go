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
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TwoFA{}, &model.TwoFABackupCode{}, &model.PasskeyCredential{}))
	model.DB = db
	t.Cleanup(func() { model.DB = oldDB })
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
}
