package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrateConsoleSettingPreservesMalformedLegacyData(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, db.Create(&model.Option{
		Key:   "ApiInfo",
		Value: "{malformed",
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/console/migrate", nil)

	MigrateConsoleSetting(context)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)

	var legacy model.Option
	require.NoError(t, db.Where("key = ?", "ApiInfo").First(&legacy).Error)
	assert.Equal(t, "{malformed", legacy.Value)

	var migrated int64
	require.NoError(t, db.Model(&model.Option{}).
		Where("key = ?", "console_setting.api_info").
		Count(&migrated).Error)
	assert.Zero(t, migrated)
}

func TestMigrateConsoleSettingPreservesIncompleteUptimeConfig(t *testing.T) {
	db := setupModelListControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Option{}))
	require.NoError(t, db.Create(&model.Option{
		Key:   "UptimeKumaUrl",
		Value: "https://status.example.com",
	}).Error)

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodPost, "/api/console/migrate", nil)

	MigrateConsoleSetting(context)

	var response struct {
		Success bool `json:"success"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)

	var legacy model.Option
	require.NoError(t, db.Where("key = ?", "UptimeKumaUrl").First(&legacy).Error)
	assert.Equal(t, "https://status.example.com", legacy.Value)
}
