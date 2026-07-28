package gemini

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func geminiPriceData(model string) types.PriceData {
	return types.PriceData{
		ModelRatio:         ratio_setting.GetDefaultModelRatioMap()[model],
		CompletionRatio:    ratio_setting.GetDefaultCompletionRatio(model),
		CacheRatio:         ratio_setting.GetDefaultCacheRatio(model),
		CacheCreationRatio: ratio_setting.GetDefaultCreateCacheRatio(model),
	}
}

func TestBuildUsageFromGeminiResponseBillsLongContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	model := "gemini-2.5-pro"
	info := &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: model,
		},
		PriceData: geminiPriceData(model),
	}
	response := &dto.GeminiChatResponse{
		HasUsageMetadata: true,
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:        ratio_setting.GeminiLongContextThreshold - 1,
			ToolUsePromptTokenCount: 2,
			CandidatesTokenCount:    10,
			TotalTokenCount:         ratio_setting.GeminiLongContextThreshold + 11,
		},
	}

	usage := buildUsageFromGeminiResponse(ctx, info, response)

	require.Equal(t, ratio_setting.GeminiLongContextThreshold+1, usage.PromptTokens)
	assert.Equal(t, 1.25, info.PriceData.ModelRatio)
	assert.Equal(t, 6.0, info.PriceData.CompletionRatio)
}

func TestBuildUsageFromGeminiResponseBillsReportedServiceTier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	model := "gemini-3.5-flash"
	info := &relaycommon.RelayInfo{
		OriginModelName: model,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeGemini,
			UpstreamModelName: model,
		},
		PriceData: geminiPriceData(model),
	}
	response := &dto.GeminiChatResponse{
		HasUsageMetadata: true,
		UsageMetadata: dto.GeminiUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 2,
			TotalTokenCount:      12,
			ServiceTier:          "flex",
		},
	}

	usage := buildUsageFromGeminiResponse(ctx, info, response)

	assert.Equal(t, "flex", usage.GeminiServiceTier)
	assert.InDelta(t, 0.375, info.PriceData.ModelRatio, 1e-12)
	assert.InDelta(t, 0.08/0.75, info.PriceData.CacheRatio, 1e-12)
}

func TestApplyGeminiUsagePricingLongContext(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		promptTokens   int
		channelType    int
		mutatePricing  func(*types.PriceData)
		wantModelRatio float64
		wantCompletion float64
	}{
		{
			name:           "Gemini 2.5 Pro above 200K uses premium rates",
			model:          "gemini-2.5-pro",
			promptTokens:   ratio_setting.GeminiLongContextThreshold + 1,
			channelType:    constant.ChannelTypeGemini,
			wantModelRatio: 1.25,
			wantCompletion: 6,
		},
		{
			name:           "Gemini 3.1 Pro above 200K uses premium rates",
			model:          "gemini-3.1-pro-preview",
			promptTokens:   ratio_setting.GeminiLongContextThreshold + 1,
			channelType:    constant.ChannelTypeGemini,
			wantModelRatio: 2,
			wantCompletion: 4.5,
		},
		{
			name:           "Gemini Computer Use above 200K uses premium rates",
			model:          "gemini-2.5-computer-use-preview-10-2025",
			promptTokens:   ratio_setting.GeminiLongContextThreshold + 1,
			channelType:    constant.ChannelTypeGemini,
			wantModelRatio: 1.25,
			wantCompletion: 6,
		},
		{
			name:           "200K boundary remains standard",
			model:          "gemini-2.5-pro",
			promptTokens:   ratio_setting.GeminiLongContextThreshold,
			channelType:    constant.ChannelTypeGemini,
			wantModelRatio: 0.625,
			wantCompletion: 8,
		},
		{
			name:         "custom cache pricing remains authoritative",
			model:        "gemini-2.5-pro",
			promptTokens: ratio_setting.GeminiLongContextThreshold + 1,
			channelType:  constant.ChannelTypeGemini,
			mutatePricing: func(priceData *types.PriceData) {
				priceData.CacheRatio = 0.2
			},
			wantModelRatio: 0.625,
			wantCompletion: 8,
		},
		{
			name:           "Vertex Pro above 200K uses premium rates",
			model:          "gemini-2.5-pro",
			promptTokens:   ratio_setting.GeminiLongContextThreshold + 1,
			channelType:    constant.ChannelTypeVertexAi,
			wantModelRatio: 1.25,
			wantCompletion: 6,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priceData := geminiPriceData(tt.model)
			if tt.mutatePricing != nil {
				tt.mutatePricing(&priceData)
			}
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelType: tt.channelType,
				},
				PriceData: priceData,
			}

			applyGeminiUsagePricing(info, &dto.Usage{PromptTokens: tt.promptTokens})

			assert.Equal(t, tt.wantModelRatio, info.PriceData.ModelRatio)
			assert.Equal(t, tt.wantCompletion, info.PriceData.CompletionRatio)
		})
	}
}

func TestApplyGeminiUsagePricingSplitsImageAndTextOutput(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		completion     int
		image          int
		mutatePricing  func(*types.PriceData)
		wantCompletion float64
		wantModelRatio float64
	}{
		{
			name:           "mixed Flash Image output",
			model:          "gemini-3.1-flash-image",
			completion:     120,
			image:          20,
			wantCompletion: 25,
			wantModelRatio: 0.25,
		},
		{
			name:           "pure Pro Image output",
			model:          "gemini-3-pro-image",
			completion:     2000,
			image:          2000,
			wantCompletion: 60,
			wantModelRatio: 1,
		},
		{
			name:       "custom completion pricing remains authoritative",
			model:      "gemini-3.1-flash-image",
			completion: 120,
			image:      20,
			mutatePricing: func(priceData *types.PriceData) {
				priceData.CompletionRatio = 7
			},
			wantCompletion: 7,
			wantModelRatio: 0.25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priceData := geminiPriceData(tt.model)
			if tt.mutatePricing != nil {
				tt.mutatePricing(&priceData)
			}
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini},
				PriceData:       priceData,
			}
			usage := &dto.Usage{CompletionTokens: tt.completion}
			usage.CompletionTokenDetails.ImageTokens = tt.image

			applyGeminiUsagePricing(info, usage)

			assert.InDelta(t, tt.wantCompletion, info.PriceData.CompletionRatio, 1e-12)
			assert.Equal(t, tt.wantModelRatio, info.PriceData.ModelRatio)
		})
	}
}

func TestApplyGeminiUsagePricingUsesActualServiceTier(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		serviceTier    string
		promptTokens   int
		wantModelRatio float64
		wantCompletion float64
		wantCacheRatio float64
	}{
		{name: "3.5 Flash Flex", model: "gemini-3.5-flash", serviceTier: "flex", wantModelRatio: 0.375, wantCompletion: 6, wantCacheRatio: 0.08 / 0.75},
		{name: "3.1 Flash Lite Priority", model: "gemini-3.1-flash-lite", serviceTier: "priority", wantModelRatio: 0.225, wantCompletion: 6, wantCacheRatio: 0.1},
		{name: "3 Flash Flex keeps standard cache price", model: "gemini-3-flash-preview", serviceTier: "flex", wantModelRatio: 0.125, wantCompletion: 6, wantCacheRatio: 0.2},
		{name: "3.1 Pro Flex long context stacks", model: "gemini-3.1-pro-preview", serviceTier: "flex", promptTokens: ratio_setting.GeminiLongContextThreshold + 1, wantModelRatio: 1, wantCompletion: 4.5, wantCacheRatio: 0.2},
		{name: "2.5 Pro Priority long context stacks", model: "gemini-2.5-pro", serviceTier: "priority", promptTokens: ratio_setting.GeminiLongContextThreshold + 1, wantModelRatio: 2.25, wantCompletion: 6, wantCacheRatio: 0.1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				OriginModelName: tt.model,
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeGemini},
				PriceData:       geminiPriceData(tt.model),
			}

			applyGeminiUsagePricing(info, &dto.Usage{PromptTokens: tt.promptTokens, GeminiServiceTier: tt.serviceTier})

			assert.Equal(t, tt.wantModelRatio, info.PriceData.ModelRatio)
			assert.Equal(t, tt.wantCompletion, info.PriceData.CompletionRatio)
			assert.Equal(t, tt.wantCacheRatio, info.PriceData.CacheRatio)
		})
	}
}
