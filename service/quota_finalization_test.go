package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAudioAndRealtimeFinalizationHandleZeroUsageByPricingMode(t *testing.T) {
	tests := []struct {
		name        string
		realtime    bool
		fixedPrice  bool
		totalTokens int
		invalid     string
	}{
		{name: "audio fixed-price usage", fixedPrice: true, totalTokens: 10},
		{name: "audio fixed-price verified zero usage", fixedPrice: true},
		{name: "audio token-priced verified zero usage"},
		{name: "audio negative prompt", fixedPrice: true, totalTokens: 10, invalid: "negative_prompt"},
		{name: "audio oversized detail", fixedPrice: true, totalTokens: 10, invalid: "oversized_audio_detail"},
		{name: "realtime fixed-price usage", realtime: true, fixedPrice: true, totalTokens: 10},
		{name: "realtime fixed-price verified zero usage", realtime: true, fixedPrice: true},
		{name: "realtime token-priced verified zero usage", realtime: true},
		{name: "realtime negative completion", realtime: true, fixedPrice: true, totalTokens: 10, invalid: "negative_output"},
		{name: "realtime oversized detail", realtime: true, fixedPrice: true, totalTokens: 10, invalid: "oversized_text_detail"},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncate(t)
			userID := 120 + index
			tokenID := 120 + index
			channelID := 120 + index
			const initialUserQuota, initialTokenQuota, preConsumedQuota = 10_000, 5_000, 100
			seedUser(t, userID, initialUserQuota-preConsumedQuota)
			seedToken(t, tokenID, userID, fmt.Sprintf("sk-audio-finalization-%d", index), initialTokenQuota-preConsumedQuota)
			seedChannel(t, channelID)
			require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Update("used_quota", preConsumedQuota).Error)

			requestID := fmt.Sprintf("audio-finalization-%d", index)
			modelPrice := 0.0
			if test.fixedPrice {
				modelPrice = 0.001
			}
			info := &relaycommon.RelayInfo{
				RequestId:       requestID,
				UserId:          userID,
				TokenId:         tokenID,
				TokenKey:        fmt.Sprintf("sk-audio-finalization-%d", index),
				OriginModelName: "audio-test-model",
				UsingGroup:      "default",
				StartTime:       time.Now(),
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channelID},
				PriceData: types.PriceData{
					UsePrice:   test.fixedPrice,
					ModelPrice: modelPrice,
					ModelRatio: 1,
					GroupRatioInfo: types.GroupRatioInfo{
						GroupRatio: 1,
					},
				},
			}
			info.Billing = &BillingSession{
				relayInfo:        info,
				funding:          &WalletFunding{userId: userID, consumed: preConsumedQuota},
				preConsumedQuota: preConsumedQuota,
				tokenConsumed:    preConsumedQuota,
			}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/audio", nil)
			ctx.Set(common.RequestIdKey, requestID)
			ctx.Set("username", "test_user")
			ctx.Set("token_name", "test_token")

			if test.realtime {
				usage := &dto.RealtimeUsage{
					TotalTokens: test.totalTokens,
					InputTokens: test.totalTokens,
					InputTokenDetails: dto.InputTokenDetails{
						TextTokens: test.totalTokens,
					},
				}
				if test.invalid == "negative_output" {
					usage.OutputTokens = -1
				}
				if test.invalid == "oversized_text_detail" {
					usage.InputTokenDetails.TextTokens = common.MaxTokensLimit + 1
				}
				PostWssConsumeQuota(ctx, info, info.OriginModelName, usage, "")
			} else {
				usage := &dto.Usage{
					TotalTokens:  test.totalTokens,
					PromptTokens: test.totalTokens,
					PromptTokensDetails: dto.InputTokenDetails{
						TextTokens: test.totalTokens,
					},
				}
				if test.invalid == "negative_prompt" {
					usage.PromptTokens = -1
				}
				if test.invalid == "oversized_audio_detail" {
					usage.PromptTokensDetails.AudioTokens = common.MaxTokensLimit + 1
				}
				PostAudioConsumeQuota(ctx, info, usage, "")
			}

			expectedQuota := 0
			if test.invalid != "" {
				expectedQuota = preConsumedQuota
			} else if test.totalTokens != 0 || test.fixedPrice {
				var clamp *common.QuotaClamp
				expectedQuota, clamp = calculateAudioQuota(QuotaInfo{
					ModelName:  info.OriginModelName,
					UsePrice:   test.fixedPrice,
					ModelPrice: info.PriceData.ModelPrice,
					ModelRatio: info.PriceData.ModelRatio,
					GroupRatio: 1,
				})
				require.Nil(t, clamp)
			}
			var user model.User
			require.NoError(t, model.DB.First(&user, userID).Error)
			assert.Equal(t, initialUserQuota-expectedQuota, user.Quota)
			assert.Equal(t, expectedQuota, user.UsedQuota)
			if test.totalTokens == 0 && test.invalid == "" && !test.fixedPrice {
				assert.Zero(t, user.RequestCount)
			} else {
				assert.Equal(t, 1, user.RequestCount)
			}
			var channel model.Channel
			require.NoError(t, model.DB.First(&channel, channelID).Error)
			assert.Equal(t, int64(expectedQuota), channel.UsedQuota)
			var token model.Token
			require.NoError(t, model.DB.First(&token, tokenID).Error)
			assert.Equal(t, initialTokenQuota-expectedQuota, token.RemainQuota)
			assert.Equal(t, expectedQuota, token.UsedQuota)
			assert.Equal(t, int64(1), countLogs(t), "zero usage must still create its consume log")
			log := getLastLog(t)
			require.NotNil(t, log)
			assert.Equal(t, expectedQuota, log.Quota)
			if test.invalid != "" {
				var other map[string]interface{}
				require.NoError(t, common.UnmarshalJsonStr(log.Other, &other))
				adminInfo, ok := other["admin_info"].(map[string]interface{})
				require.True(t, ok)
				assert.Contains(t, adminInfo, "invalid_billing_usage")
			}
		})
	}
}

func TestPreWssConsumeQuotaRejectsInvalidUsageBeforeReservation(t *testing.T) {
	tests := []struct {
		name  string
		usage *dto.RealtimeUsage
	}{
		{name: "missing usage"},
		{
			name: "negative cumulative usage",
			usage: &dto.RealtimeUsage{
				TotalTokens: -1,
			},
		},
		{
			name: "inconsistent cumulative usage",
			usage: &dto.RealtimeUsage{
				TotalTokens: 1,
				InputTokens: 2,
				InputTokenDetails: dto.InputTokenDetails{
					TextTokens: 2,
				},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			billing := &realtimeReserveBilling{reserved: 10}
			info := &relaycommon.RelayInfo{Billing: billing}
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())

			err := PreWssConsumeQuota(ctx, info, test.usage)

			require.ErrorContains(t, err, "invalid realtime billing usage")
			assert.Empty(t, billing.targets)
			assert.Equal(t, 10, billing.reserved)
		})
	}
}
