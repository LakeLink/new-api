package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperPreConsumesProviderDynamicRates(t *testing.T) {
	gin.SetMode(gin.TestMode)
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	savedModelRatios := ratio_setting.ModelRatio2JSONString()
	savedCompletionRatios := ratio_setting.CompletionRatio2JSONString()
	savedCacheRatios := ratio_setting.CacheRatio2JSONString()
	savedCreateCacheRatios := ratio_setting.CreateCacheRatio2JSONString()
	savedGroupRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(savedModelRatios))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(savedCompletionRatios))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(savedCacheRatios))
		require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(savedCreateCacheRatios))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroupRatios))
	})

	ratio_setting.InitRatioSettings()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	t.Run("Gemini Pro long context reserves premium input and output", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
		info := &relaycommon.RelayInfo{
			OriginModelName: "gemini-2.5-pro",
			UserGroup:       "default",
			UsingGroup:      "default",
		}

		priceData, err := ModelPriceHelper(ctx, info, ratio_setting.GeminiLongContextThreshold+1, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 250751, priceData.QuotaToPreConsume)
	})

	t.Run("Vertex Gemini Pro long context reserves premium input and output", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeVertexAi)
		info := &relaycommon.RelayInfo{
			OriginModelName: "gemini-2.5-pro",
			UserGroup:       "default",
			UsingGroup:      "default",
		}

		priceData, err := ModelPriceHelper(ctx, info, ratio_setting.GeminiLongContextThreshold+1, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 250751, priceData.QuotaToPreConsume)
	})

	t.Run("Gemini image model reserves its maximum image output rate", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
		info := &relaycommon.RelayInfo{
			OriginModelName: "gemini-3.1-flash-image",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request: &dto.GeminiChatRequest{
				GenerationConfig: dto.GeminiChatGenerationConfig{CandidateCount: common.GetPointer(2)},
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{})

		require.NoError(t, err)
		assert.Equal(t, 151450, priceData.QuotaToPreConsume)
		assert.Equal(t, 6.0, priceData.CompletionRatio)
	})

	t.Run("Gemini priority reserves the requested service-tier premium", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowServiceTier: true})
		info := &relaycommon.RelayInfo{
			OriginModelName: "gemini-3.5-flash",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request: &dto.GeminiChatRequest{
				ServiceTier: common.GetPointer(dto.GeminiServiceTierPriority),
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 2160, priceData.QuotaToPreConsume)
	})

	t.Run("filtered Gemini priority reserves standard pricing", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeGemini)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowServiceTier: false})
		info := &relaycommon.RelayInfo{
			OriginModelName: "gemini-3.5-flash",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request: &dto.GeminiChatRequest{
				ServiceTier: common.GetPointer(dto.GeminiServiceTierPriority),
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 1200, priceData.QuotaToPreConsume)
	})

	t.Run("Claude Opus 5 fast reserves the model-specific premium", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowSpeed: true})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-opus-5",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request: &dto.ClaudeRequest{
				Speed: []byte(`"fast"`),
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 7500, priceData.QuotaToPreConsume)
	})

	t.Run("removed Claude Opus 4.7 fast mode reserves standard pricing", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowSpeed: true})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-opus-4-7",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request: &dto.ClaudeRequest{
				Speed: []byte(`"fast"`),
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 3750, priceData.QuotaToPreConsume)
	})

	t.Run("filtered Claude speed reserves standard pricing", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowSpeed: false})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-opus-5",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request: &dto.ClaudeRequest{
				Speed: []byte(`"fast"`),
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 3750, priceData.QuotaToPreConsume)
	})

	t.Run("Claude US inference stacks with fast-mode premium", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{
			AllowInferenceGeo: true,
			AllowSpeed:        true,
		})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-opus-5",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request: &dto.ClaudeRequest{
				InferenceGeo: common.GetPointer("us"),
				Speed:        []byte(`"fast"`),
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 8250, priceData.QuotaToPreConsume)
		assert.Equal(t, 1.1, priceData.OtherRatios()["anthropic_inference_geo"])
	})

	t.Run("filtered Claude US inference keeps standard pricing", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowInferenceGeo: false})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-sonnet-4-6",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request:         &dto.ClaudeRequest{InferenceGeo: common.GetPointer("us")},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 2250, priceData.QuotaToPreConsume)
		assert.False(t, priceData.HasOtherRatio("anthropic_inference_geo"))
	})

	t.Run("channel raw-body passthrough bills forwarded Claude fields", func(t *testing.T) {
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
		common.SetContextKey(ctx, constant.ContextKeyChannelSetting, dto.ChannelSettings{PassThroughBodyEnabled: true})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-opus-4-8",
			UserGroup:       "default",
			UsingGroup:      "default",
			ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeAnthropic},
			Request: &dto.ClaudeRequest{
				InferenceGeo: common.GetPointer("us"),
				Speed:        []byte(`"fast"`),
			},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 8250, priceData.QuotaToPreConsume)
		assert.Equal(t, 1.1, priceData.OtherRatios()["anthropic_inference_geo"])
	})

	t.Run("global raw-body passthrough bills forwarded Claude fields", func(t *testing.T) {
		original := model_setting.GetGlobalSettings().PassThroughRequestEnabled
		model_setting.GetGlobalSettings().PassThroughRequestEnabled = true
		t.Cleanup(func() {
			model_setting.GetGlobalSettings().PassThroughRequestEnabled = original
		})

		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-sonnet-4-6",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request:         &dto.ClaudeRequest{InferenceGeo: common.GetPointer("us")},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 2475, priceData.QuotaToPreConsume)
		assert.Equal(t, 1.1, priceData.OtherRatios()["anthropic_inference_geo"])
	})

	t.Run("custom Claude ratio remains authoritative for US inference", func(t *testing.T) {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"claude-sonnet-4-6":2}`))
		ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
		ctx.Set("group", "default")
		common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeAnthropic)
		common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowInferenceGeo: true})
		info := &relaycommon.RelayInfo{
			OriginModelName: "claude-sonnet-4-6",
			UserGroup:       "default",
			UsingGroup:      "default",
			Request:         &dto.ClaudeRequest{InferenceGeo: common.GetPointer("us")},
		}

		priceData, err := ModelPriceHelper(ctx, info, 1000, &types.TokenCountMeta{MaxTokens: 100})

		require.NoError(t, err)
		assert.Equal(t, 3000, priceData.QuotaToPreConsume)
		assert.False(t, priceData.HasOtherRatio("anthropic_inference_geo"))
	})
}
