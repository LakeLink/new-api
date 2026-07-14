package openai

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

func TestApplyOpenAIUsagePricing(t *testing.T) {
	basePriceData := types.PriceData{
		ModelRatio:         2.5,
		CompletionRatio:    6,
		CacheRatio:         0.1,
		CacheCreationRatio: 1.25,
	}

	tests := []struct {
		name                   string
		channelType            int
		model                  string
		promptTokens           int
		serviceTier            string
		priceData              types.PriceData
		wantModelRatio         float64
		wantCompletionRatio    float64
		wantCacheRatio         float64
		wantCacheCreationRatio float64
	}{
		{
			name:                   "actual priority tier replaces standard rates",
			channelType:            constant.ChannelTypeOpenAI,
			model:                  "gpt-5.5",
			serviceTier:            "priority",
			priceData:              basePriceData,
			wantModelRatio:         6.25,
			wantCompletionRatio:    6,
			wantCacheRatio:         0.1,
			wantCacheCreationRatio: 1.25,
		},
		{
			name:                   "long context scales input and output independently",
			channelType:            constant.ChannelTypeOpenAI,
			model:                  "gpt-5.5",
			promptTokens:           ratio_setting.OpenAILongContextThreshold + 1,
			priceData:              basePriceData,
			wantModelRatio:         5,
			wantCompletionRatio:    4.5,
			wantCacheRatio:         0.1,
			wantCacheCreationRatio: 1.25,
		},
		{
			name:                   "threshold itself remains standard price",
			channelType:            constant.ChannelTypeOpenAI,
			model:                  "gpt-5.5",
			promptTokens:           ratio_setting.OpenAILongContextThreshold,
			priceData:              basePriceData,
			wantModelRatio:         2.5,
			wantCompletionRatio:    6,
			wantCacheRatio:         0.1,
			wantCacheCreationRatio: 1.25,
		},
		{
			name:        "administrator ratio remains authoritative",
			channelType: constant.ChannelTypeOpenAI,
			model:       "gpt-5.5",
			serviceTier: "priority",
			priceData: types.PriceData{
				ModelRatio:         3,
				CompletionRatio:    7,
				CacheRatio:         0.2,
				CacheCreationRatio: 1.4,
			},
			wantModelRatio:         3,
			wantCompletionRatio:    7,
			wantCacheRatio:         0.2,
			wantCacheCreationRatio: 1.4,
		},
		{
			name:        "administrator completion ratio remains authoritative",
			channelType: constant.ChannelTypeOpenAI,
			model:       "gpt-5.5",
			serviceTier: "priority",
			priceData: types.PriceData{
				ModelRatio:         2.5,
				CompletionRatio:    7,
				CacheRatio:         0.1,
				CacheCreationRatio: 1.25,
			},
			wantModelRatio:         2.5,
			wantCompletionRatio:    7,
			wantCacheRatio:         0.1,
			wantCacheCreationRatio: 1.25,
		},
		{
			name:                   "compatible provider is not repriced",
			channelType:            constant.ChannelTypeAzure,
			model:                  "gpt-5.5",
			serviceTier:            "priority",
			priceData:              basePriceData,
			wantModelRatio:         2.5,
			wantCompletionRatio:    6,
			wantCacheRatio:         0.1,
			wantCacheCreationRatio: 1.25,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: tt.channelType},
				OriginModelName: tt.model,
				PriceData:       tt.priceData,
			}

			applyOpenAIUsagePricing(info, &dto.Usage{PromptTokens: tt.promptTokens}, tt.serviceTier)

			assert.Equal(t, tt.wantModelRatio, info.PriceData.ModelRatio)
			assert.Equal(t, tt.wantCompletionRatio, info.PriceData.CompletionRatio)
			assert.Equal(t, tt.wantCacheRatio, info.PriceData.CacheRatio)
			assert.Equal(t, tt.wantCacheCreationRatio, info.PriceData.CacheCreationRatio)
		})
	}
}

func TestAdvertisedOpenAIModelsHaveBuiltInPricing(t *testing.T) {
	modelRatios := ratio_setting.GetDefaultModelRatioMap()
	modelPrices := ratio_setting.GetDefaultModelPriceMap()
	for _, model := range ModelList {
		_, hasRatio := modelRatios[model]
		_, hasPrice := modelPrices[model]
		assert.Truef(t, hasRatio || hasPrice, "advertised model %q has no built-in billing configuration", model)
	}
}

func TestOaiResponsesStreamHandlerBillsIncompleteUsage(t *testing.T) {
	oldMode := gin.Mode()
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() { gin.SetMode(oldMode) })

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "incomplete-usage-test")

	body := strings.Join([]string{
		`data: {"type":"response.incomplete","response":{"service_tier":"default","usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}}`,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true,
		DisablePing: true,
	}

	usage, err := OaiResponsesStreamHandler(c, info, resp)
	require.Nil(t, err)
	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)
	assert.Equal(t, 7, usage.CompletionTokens)
	assert.Equal(t, 18, usage.TotalTokens)
}
