package service_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	claudechannel "github.com/QuantumNous/new-api/relay/channel/claude"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupClaudeBillingEndToEndTest(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldRedisClient := common.RDB
	oldBatchEnabled := common.BatchUpdateEnabled
	oldLogConsumeEnabled := common.LogConsumeEnabled
	oldDataExportEnabled := common.DataExportEnabled
	oldQuotaPerUnit := common.QuotaPerUnit

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", url.QueryEscape(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.BatchUpdateEnabled = false
	common.LogConsumeEnabled = true
	common.DataExportEnabled = false
	common.QuotaPerUnit = 500_000
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Channel{},
		&model.Log{},
		&model.BillingReservation{},
		&model.TaskBillingFinalization{},
		&model.QuotaData{},
		&model.AffiliateReward{},
		&model.SystemTask{},
	))

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRedisClient
		common.BatchUpdateEnabled = oldBatchEnabled
		common.LogConsumeEnabled = oldLogConsumeEnabled
		common.DataExportEnabled = oldDataExportEnabled
		common.QuotaPerUnit = oldQuotaPerUnit
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestFablePreOutputRefusalSettlesVerifiedZeroEndToEnd(t *testing.T) {
	db := setupClaudeBillingEndToEndTest(t)
	user := model.User{
		Username: "fable-zero-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    1_000,
		AffCode:  "fable-zero-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "fable-zero-token", RemainQuota: 500}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "fable-zero-channel", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Set("username", user.Username)
	modelName := "claude-fable-5"
	info := &relaycommon.RelayInfo{
		RequestId:       "fable-verified-zero-request",
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		OriginModelName: modelName,
		UsingGroup:      "default",
		StartTime:       time.Now(),
		ForcePreConsume: true,
		RelayFormat:     types.RelayFormatClaude,
		UserSetting:     dto.UserSetting{BillingPreference: "wallet_only"},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAnthropic,
			ChannelId:         channel.Id,
			UpstreamModelName: modelName,
		},
		PriceData: types.PriceData{
			ModelRatio:      5,
			CompletionRatio: 5,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	require.Nil(t, service.PreConsumeBilling(ctx, 100, info))

	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":"refusal","usage":{"input_tokens":412,"output_tokens":0}}`,
		)),
	}
	usage, apiErr := claudechannel.ClaudeHandler(ctx, response, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	require.NotNil(t, usage.BillingUsage)
	assert.True(t, usage.BillingUsage.VerifiedZero)

	service.PostTextConsumeQuota(ctx, info, usage, nil)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 1_000, user.Quota)
	assert.Zero(t, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, 500, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)

	var consumeLog model.Log
	require.NoError(t, db.Where("user_id = ?", user.Id).Order("id DESC").First(&consumeLog).Error)
	assert.Zero(t, consumeLog.Quota)
	assert.NotContains(t, consumeLog.Content, "上游返回无效计费信息")
}

func TestClaudeStreamServerToolUsageFlowsIntoSettlement(t *testing.T) {
	db := setupClaudeBillingEndToEndTest(t)
	user := model.User{
		Username: "claude-stream-tool-user",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    50_000,
		AffCode:  "claude-stream-tool-aff",
	}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "claude-stream-tool-token", RemainQuota: 50_000}
	require.NoError(t, db.Create(&token).Error)
	channel := model.Channel{Name: "claude-stream-tool-channel", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	ctx.Set("username", user.Username)
	maxUses := uint(2)
	modelName := "claude-sonnet-4-6"
	info := &relaycommon.RelayInfo{
		RequestId:                       "claude-stream-tool-request",
		UserId:                          user.Id,
		TokenId:                         token.Id,
		TokenKey:                        token.Key,
		OriginModelName:                 modelName,
		UsingGroup:                      "default",
		StartTime:                       time.Now(),
		ForcePreConsume:                 true,
		RelayFormat:                     types.RelayFormatClaude,
		FinalRequestRelayFormat:         types.RelayFormatClaude,
		EffectiveClaudeWebSearchMaxUses: &maxUses,
		UserSetting:                     dto.UserSetting{BillingPreference: "wallet_only"},
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAnthropic,
			ChannelId:         channel.Id,
			UpstreamModelName: modelName,
		},
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	require.Nil(t, service.PreConsumeBilling(ctx, 100, info))

	claudeInfo := &claudechannel.ClaudeResponseInfo{
		Model:        modelName,
		ResponseText: strings.Builder{},
		Usage:        &dto.Usage{},
	}
	require.Nil(t, claudechannel.HandleStreamResponseData(ctx, info, claudeInfo,
		`{"type":"message_start","message":{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-sonnet-4-6","usage":{"input_tokens":10,"output_tokens":0}}}`))
	require.Nil(t, claudechannel.HandleStreamResponseData(ctx, info, claudeInfo,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2,"server_tool_use":{"web_search_requests":2}}}`))
	claudechannel.HandleStreamFinalResponse(ctx, info, claudeInfo)

	require.NotNil(t, claudeInfo.Usage.BillingUsage)
	require.NotNil(t, claudeInfo.Usage.BillingUsage.ClaudeUsage)
	require.NotNil(t, claudeInfo.Usage.BillingUsage.ClaudeUsage.ServerToolUse)
	assert.Equal(t, 2, claudeInfo.Usage.BillingUsage.ClaudeUsage.ServerToolUse.WebSearchRequests)

	service.PostTextConsumeQuota(ctx, info, claudeInfo.Usage, nil)

	const expectedQuota = 10_012
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 50_000-expectedQuota, user.Quota)
	assert.Equal(t, expectedQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, 50_000-expectedQuota, token.RemainQuota)
	assert.Equal(t, expectedQuota, token.UsedQuota)

	var consumeLog model.Log
	require.NoError(t, db.Where("user_id = ?", user.Id).Order("id DESC").First(&consumeLog).Error)
	assert.Equal(t, expectedQuota, consumeLog.Quota)
	assert.Contains(t, consumeLog.Content, "Claude Web Search")
	assert.Contains(t, consumeLog.Other, `"web_search_call_count":2`)
}
