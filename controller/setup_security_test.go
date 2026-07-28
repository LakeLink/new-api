package controller

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPostSetupRejectsBlankTrimmedRootUsername(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Setup{}, &model.Option{}))

	originalSetup := constant.Setup.Load()
	constant.Setup.Store(false)
	t.Cleanup(func() { constant.Setup.Store(originalSetup) })

	router := gin.New()
	router.POST("/setup", PostSetup)
	body := []byte(`{
		"username":"   ",
		"password":"SecurePassword123",
		"confirmPassword":"SecurePassword123"
	}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/setup", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)

	var setupCount, rootCount int64
	require.NoError(t, db.Model(&model.Setup{}).Count(&setupCount).Error)
	require.NoError(t, db.Model(&model.User{}).Where("role = ?", common.RoleRootUser).Count(&rootCount).Error)
	assert.Zero(t, setupCount)
	assert.Zero(t, rootCount)
}

func TestGetSetupDoesNotExposeDatabaseErrors(t *testing.T) {
	setupModelListControllerTestDB(t)
	require.NoError(t, i18n.Init())

	originalSetup := constant.Setup.Load()
	constant.Setup.Store(false)
	t.Cleanup(func() { constant.Setup.Store(originalSetup) })

	router := gin.New()
	router.GET("/setup", GetSetup)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/setup", nil)
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "Database error, please contact the administrator", response.Message)
	assert.NotContains(t, response.Message, "no such table")
}

func TestPostSetupDoesNotExposeDatabaseErrors(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, i18n.Init())
	require.NoError(t, db.Migrator().DropTable(&model.User{}))

	originalSetup := constant.Setup.Load()
	constant.Setup.Store(false)
	t.Cleanup(func() { constant.Setup.Store(originalSetup) })

	router := gin.New()
	router.POST("/setup", PostSetup)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/setup", bytes.NewReader([]byte(`{
		"username":"root",
		"password":"SecurePassword123",
		"confirmPassword":"SecurePassword123"
	}`)))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Equal(t, "Database error, please contact the administrator", response.Message)
	assert.NotContains(t, response.Message, "no such table")
}
