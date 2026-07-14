package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	appI18n "github.com/QuantumNous/new-api/i18n"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestForcedChannelStillEnforcesTokenModelLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, appI18n.Init())
	handlerCalled := false
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "42")
		common.SetContextKey(c, constant.ContextKeyTokenModelLimitEnabled, true)
		common.SetContextKey(c, constant.ContextKeyTokenModelLimit, map[string]bool{"allowed-model": true})
		c.Next()
	}, Distribute(), func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"forbidden-model"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	assert.False(t, handlerCalled)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}

func TestForcedChannelStillEnforcesTokenGroupAbility(t *testing.T) {
	gin.SetMode(gin.TestMode)
	require.NoError(t, appI18n.Init())
	oldDB := model.DB
	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	oldMainDatabaseType := common.MainDatabaseType()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Ability{}))
	model.DB = db
	common.MemoryCacheEnabled = false
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		model.DB = oldDB
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		common.SetMainDatabaseType(oldMainDatabaseType)
	})
	require.NoError(t, db.Create(&model.Channel{
		Id: 42, Name: "forced", Key: "provider-key", Status: common.ChannelStatusEnabled,
		Models: "gpt-test", Group: "other",
	}).Error)
	require.NoError(t, db.Create(&model.Ability{
		Group: "other", Model: "gpt-test", ChannelId: 42, Enabled: true,
	}).Error)

	handlerCalled := false
	router := gin.New()
	router.POST("/v1/chat/completions", func(c *gin.Context) {
		common.SetContextKey(c, constant.ContextKeyTokenSpecificChannelId, "42")
		common.SetContextKey(c, constant.ContextKeyUsingGroup, "default")
		c.Next()
	}, Distribute(), func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"gpt-test"}`))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(recorder, request)

	assert.False(t, handlerCalled)
	assert.Equal(t, http.StatusForbidden, recorder.Code)
}
