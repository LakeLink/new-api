package controller

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestGetWaffoPayMoneyRejectsUnrepresentableCharges(t *testing.T) {
	originalUnitPrice := setting.WaffoUnitPrice
	originalQuotaDisplayType := operation_setting.GetGeneralSetting().QuotaDisplayType
	originalQuotaPerUnit := common.QuotaPerUnit
	originalTopupGroupRatio := common.TopupGroupRatio2JSONString()
	t.Cleanup(func() {
		setting.WaffoUnitPrice = originalUnitPrice
		operation_setting.GetGeneralSetting().QuotaDisplayType = originalQuotaDisplayType
		common.QuotaPerUnit = originalQuotaPerUnit
		require.NoError(t, common.UpdateTopupGroupRatioByJSONString(originalTopupGroupRatio))
	})

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	setting.WaffoUnitPrice = math.MaxFloat64
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	require.Zero(t, getWaffoPayMoney(10, "default"))

	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	setting.WaffoUnitPrice = 1
	common.QuotaPerUnit = 0
	require.Zero(t, getWaffoPayMoney(10, "default"))
}

func TestRequestWaffoPayFailsClosedWhenGroupLookupFails(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalDB := model.DB
	originalRedisEnabled := common.RedisEnabled
	originalMainDatabaseType := common.MainDatabaseType()
	originalLogDatabaseType := common.LogDatabaseType()
	common.OptionMapRWMutex.Lock()
	originalOptionMap := common.OptionMap
	common.OptionMap = map[string]string{
		"WaffoEnabled":     "true",
		"WaffoApiKey":      "api-key",
		"WaffoPrivateKey":  "private-key",
		"WaffoPublicCert":  "public-cert",
		"WaffoMerchantId":  "merchant",
		"WaffoSandbox":     "false",
		"WaffoUnitPrice":   "1",
		"WaffoMinTopUp":    "1",
		"WaffoPayMethods":  "[]",
		"QuotaDisplayType": operation_setting.QuotaDisplayTypeUSD,
	}
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = originalOptionMap
		common.OptionMapRWMutex.Unlock()
	})

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	model.DB = db
	common.RedisEnabled = false
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = originalDB
		common.RedisEnabled = originalRedisEnabled
		common.SetDatabaseTypes(originalMainDatabaseType, originalLogDatabaseType)
		require.NoError(t, sqlDB.Close())
	})
	require.NoError(t, db.AutoMigrate(&model.User{}))
	user := model.User{
		Username: "waffo-group-error",
		Group:    "vip",
		AffCode:  "waffo-group-error-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		"test:fail_waffo_group_lookup",
		func(tx *gorm.DB) {
			for _, selected := range tx.Statement.Selects {
				if strings.Trim(selected, "`\"") == "group" {
					_ = tx.AddError(errors.New("injected group lookup failure"))
					return
				}
			}
		},
	))
	router := gin.New()
	router.POST("/waffo/pay", func(c *gin.Context) {
		c.Set("id", user.Id)
		RequestWaffoPay(c)
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/waffo/pay", strings.NewReader(`{"amount":10}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	assert.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Message string `json:"message"`
		Data    string `json:"data"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.Equal(t, "error", response.Message)
	assert.Equal(t, "获取用户分组失败", response.Data)
}
