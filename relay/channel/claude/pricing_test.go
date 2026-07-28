package claude

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func claudePriceData(model string) types.PriceData {
	return types.PriceData{
		ModelRatio:         ratio_setting.GetDefaultModelRatioMap()[model],
		CompletionRatio:    ratio_setting.GetDefaultCompletionRatio(model),
		CacheRatio:         ratio_setting.GetDefaultCacheRatio(model),
		CacheCreationRatio: ratio_setting.GetDefaultCreateCacheRatio(model),
	}
}

func TestApplyClaudeUsagePricingUsesActualSpeed(t *testing.T) {
	tests := []struct {
		name           string
		model          string
		speed          string
		channelType    int
		mutatePricing  func(*types.PriceData)
		wantModelRatio float64
	}{
		{
			name:           "Opus 5 fast uses two-times premium",
			model:          "claude-opus-5",
			speed:          "fast",
			channelType:    constant.ChannelTypeAnthropic,
			wantModelRatio: 5,
		},
		{
			name:           "Opus 4.8 fast uses two-times premium",
			model:          "claude-opus-4-8",
			speed:          "fast",
			channelType:    constant.ChannelTypeAnthropic,
			wantModelRatio: 5,
		},
		{
			name:           "Opus 4.7 no longer applies fast pricing",
			model:          "claude-opus-4-7",
			speed:          "fast",
			channelType:    constant.ChannelTypeAnthropic,
			wantModelRatio: 2.5,
		},
		{
			name:           "Opus 4.6 fallback is billed standard",
			model:          "claude-opus-4-6",
			speed:          "standard",
			channelType:    constant.ChannelTypeAnthropic,
			wantModelRatio: 2.5,
		},
		{
			name:           "custom ratio remains authoritative",
			model:          "claude-opus-4-8",
			speed:          "fast",
			channelType:    constant.ChannelTypeAnthropic,
			mutatePricing:  func(priceData *types.PriceData) { priceData.ModelRatio = 9 },
			wantModelRatio: 9,
		},
		{
			name:           "Bedrock pricing is not rewritten",
			model:          "claude-opus-4-8",
			speed:          "fast",
			channelType:    constant.ChannelTypeAws,
			wantModelRatio: 2.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priceData := claudePriceData(tt.model)
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

			applyClaudeUsagePricing(info, &dto.Usage{ClaudeSpeed: tt.speed})

			assert.Equal(t, tt.wantModelRatio, info.PriceData.ModelRatio)
			assert.Equal(t, 5.0, info.PriceData.CompletionRatio)
		})
	}
}

func TestFormatClaudeResponseInfoPreservesUsageSpeed(t *testing.T) {
	claudeInfo := &ClaudeResponseInfo{Usage: &dto.Usage{}}
	response := &dto.ClaudeResponse{
		Type: "message_delta",
		Usage: &dto.ClaudeUsage{
			InputTokens:  10,
			OutputTokens: 4,
			Speed:        "fast",
		},
	}

	assert.True(t, FormatClaudeResponseInfo(response, nil, claudeInfo))
	assert.Equal(t, "fast", claudeInfo.Usage.ClaudeSpeed)
	require.NotNil(t, claudeInfo.Usage.BillingUsage)
	require.NotNil(t, claudeInfo.Usage.BillingUsage.ClaudeUsage)
	assert.Equal(t, "fast", claudeInfo.Usage.BillingUsage.ClaudeUsage.Speed)
}

func TestClaudeSpeedBillingMetadataDoesNotLeakIntoOpenAIUsage(t *testing.T) {
	payload, err := common.Marshal(dto.Usage{
		PromptTokens: 10,
		ClaudeSpeed:  "fast",
	})

	require.NoError(t, err)
	assert.NotContains(t, string(payload), "speed")
}

func TestClaudeHandlerBillsReportedFastSpeed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	model := "claude-opus-4-8"
	info := &relaycommon.RelayInfo{
		OriginModelName: model,
		RelayFormat:     types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAnthropic,
			UpstreamModelName: model,
		},
		PriceData: claudePriceData(model),
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-opus-4-8","stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":4,"speed":"fast"}}`,
		)),
	}

	usage, apiErr := ClaudeHandler(ctx, response, info)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, "fast", usage.ClaudeSpeed)
	assert.Equal(t, 5.0, info.PriceData.ModelRatio)
}

func TestClaudeHandlerDoesNotBillPreOutputFableRefusal(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	model := "claude-fable-5"
	info := &relaycommon.RelayInfo{
		OriginModelName: model,
		RelayFormat:     types.RelayFormatClaude,
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeAnthropic,
			UpstreamModelName: model,
		},
		PriceData: claudePriceData(model),
	}
	response := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body: io.NopCloser(strings.NewReader(
			`{"id":"msg_1","type":"message","role":"assistant","content":[],"model":"claude-fable-5","stop_reason":"refusal","usage":{"input_tokens":412,"output_tokens":0}}`,
		)),
	}

	usage, apiErr := ClaudeHandler(ctx, response, info)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	// Preserve provider-reported token usage for clients and observability.
	assert.Equal(t, 412, usage.PromptTokens)
	require.NotNil(t, usage.BillingUsage)
	require.NotNil(t, usage.BillingUsage.ClaudeUsage)
	assert.True(t, usage.BillingUsage.VerifiedZero)
	// Internal settlement usage is zero because Anthropic does not charge a
	// refusal that occurs before any output.
	assert.Zero(t, usage.BillingUsage.ClaudeUsage.InputTokens)
	assert.Zero(t, usage.BillingUsage.ClaudeUsage.OutputTokens)
}

func TestClaudeRefusalAfterOutputRemainsBillable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyAdminRejectReason, "claude_stop_reason=refusal")
	usage := &dto.Usage{
		CompletionTokens: 3,
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{
			InputTokens:  100,
			OutputTokens: 3,
		}),
	}

	applyClaudeRefusalBilling(ctx, "claude-fable-5", usage)

	require.NotNil(t, usage.BillingUsage.ClaudeUsage)
	assert.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
	assert.Equal(t, 3, usage.BillingUsage.ClaudeUsage.OutputTokens)
}

func TestClaudeRefusalForOtherModelsRemainsBillable(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyAdminRejectReason, "claude_stop_reason=refusal")
	usage := &dto.Usage{
		BillingUsage: dto.NewClaudeMessagesBillingUsage(&dto.ClaudeUsage{InputTokens: 100}),
	}

	applyClaudeRefusalBilling(ctx, "claude-sonnet-5", usage)

	require.NotNil(t, usage.BillingUsage.ClaudeUsage)
	assert.Equal(t, 100, usage.BillingUsage.ClaudeUsage.InputTokens)
}
