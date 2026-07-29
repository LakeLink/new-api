package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type preflightBillingStub struct {
	reserved int
}

func (s *preflightBillingStub) Settle(int) error              { return nil }
func (s *preflightBillingStub) Refund(*gin.Context)           {}
func (s *preflightBillingStub) NeedsRefund() bool             { return false }
func (s *preflightBillingStub) GetPreConsumedQuota() int      { return s.reserved }
func (s *preflightBillingStub) Reserve(targetQuota int) error { s.reserved = targetQuota; return nil }

func TestFinalResponsesToolReservationUsesOutboundCap(t *testing.T) {
	maxToolCalls := uint(3)
	request := &dto.OpenAIResponsesRequest{
		Model:        "gpt-5",
		Input:        []byte(`"hello"`),
		MaxToolCalls: &maxToolCalls,
		Tools: []byte(`[
			{"type":"web_search"},
			{"type":"file_search"}
		]`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		PriceData: types.PriceData{
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1.5},
		},
	}

	quota, err := finalServerToolReservationQuota(info, request)
	require.NoError(t, err)
	maxPrice := operation_setting.GetToolPriceForModel("web_search", "gpt-5")
	if filePrice := operation_setting.GetToolPriceForModel("file_search", "gpt-5"); filePrice > maxPrice {
		maxPrice = filePrice
	}
	expected := common.QuotaFromFloat(maxPrice * 3 / 1000 * common.QuotaPerUnit * 1.5)
	assert.Equal(t, expected, quota)
}

func TestFinalResponsesToolReservationPreservesExplicitZeroCap(t *testing.T) {
	zero := uint(0)
	request := &dto.OpenAIResponsesRequest{
		Model:        "gpt-5",
		Input:        []byte(`"hello"`),
		MaxToolCalls: &zero,
		Tools:        []byte(`[{"type":"web_search"},{"type":"image_generation"}]`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		PriceData:       types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}

	quota, err := finalServerToolReservationQuota(info, request)
	require.NoError(t, err)
	assert.Zero(t, quota)
}

func TestFinalOutboundResponsesMaterializesEnforceableToolCap(t *testing.T) {
	materialized, err := materializeFinalProviderToolCaps(types.RelayFormatOpenAIResponses, []byte(`{
		"model":"gpt-5",
		"input":"hello",
		"tools":[{"type":"web_search"}]
	}`))
	require.NoError(t, err)
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal(materialized, &request))
	require.NotNil(t, request.MaxToolCalls)
	assert.Equal(t, uint(common.MaxTextToolCallCount), *request.MaxToolCalls)
}

func TestFinalOutboundResponsesPreservesGlobalToolCapWithImageGeneration(t *testing.T) {
	materialized, err := materializeFinalProviderToolCaps(types.RelayFormatOpenAIResponses, []byte(`{
		"model":"gpt-5",
		"input":"hello",
		"max_tool_calls":7,
		"tools":[{"type":"web_search"},{"type":"image_generation"}]
	}`))
	require.NoError(t, err)
	var request dto.OpenAIResponsesRequest
	require.NoError(t, common.Unmarshal(materialized, &request))
	require.NotNil(t, request.MaxToolCalls)
	assert.Equal(t, uint(7), *request.MaxToolCalls)

	zeroMaterialized, err := materializeFinalProviderToolCaps(types.RelayFormatOpenAIResponses, []byte(`{
		"model":"gpt-5",
		"input":"hello",
		"max_tool_calls":0,
		"tools":[{"type":"image_generation"}]
	}`))
	require.NoError(t, err)
	require.NoError(t, common.Unmarshal(zeroMaterialized, &request))
	require.NotNil(t, request.MaxToolCalls)
	assert.Equal(t, uint(0), *request.MaxToolCalls)
}

func TestFinalResponsesToolReservationSkipsDisabledTools(t *testing.T) {
	request := &dto.OpenAIResponsesRequest{
		Model:      "gpt-5",
		Input:      []byte(`"hello"`),
		ToolChoice: []byte(`"none"`),
		Tools:      []byte(`[{"type":"web_search"},{"type":"image_generation"}]`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		PriceData:       types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}

	quota, err := finalServerToolReservationQuota(info, request)
	require.NoError(t, err)
	assert.Zero(t, quota)
}

func TestFinalResponsesToolReservationHonorsAllowedTools(t *testing.T) {
	request := &dto.OpenAIResponsesRequest{
		Model: "gpt-5",
		Input: []byte(`"hello"`),
		ToolChoice: []byte(`{
			"type":"allowed_tools",
			"mode":"auto",
			"tools":[{"type":"file_search"}]
		}`),
		Tools: []byte(`[{"type":"web_search"},{"type":"file_search"}]`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		PriceData:       types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}

	quota, err := finalServerToolReservationQuota(info, request)
	require.NoError(t, err)
	expected := common.QuotaFromFloat(
		operation_setting.GetToolPriceForModel("file_search", "gpt-5") *
			common.MaxTextToolCallCount / 1000 * common.QuotaPerUnit,
	)
	assert.Equal(t, expected, quota)
}

func TestClaudeWebSearchReservationUsesExactFinalLimit(t *testing.T) {
	first := uint(2)
	second := uint(3)
	tools := []any{
		dto.ClaudeWebSearchTool{Type: "web_search_20250305", Name: "web_search", MaxUses: &first},
		dto.ClaudeWebSearchTool{Type: "web_search_20250305", Name: "web_search", MaxUses: &second},
	}

	limit, configured, err := helper.ClaudeWebSearchLimit(tools)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, uint(5), limit)

	limit, configured, err = helper.ClaudeWebSearchLimit([]any{
		dto.ClaudeWebSearchTool{Type: "web_search_20250305", Name: "web_search"},
	})
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, uint(common.MaxTextToolCallCount), limit)
}

func TestFinalOutboundClaudeMaterializesOmittedWebSearchLimit(t *testing.T) {
	materialized, err := materializeFinalProviderToolCaps(types.RelayFormatClaude, []byte(`{
		"model":"claude-sonnet-4",
		"max_tokens":100,
		"messages":[{"role":"user","content":"hello"}],
		"tools":[{"type":"web_search_20250305","name":"web_search"}]
	}`))
	require.NoError(t, err)
	var request dto.ClaudeRequest
	require.NoError(t, common.Unmarshal(materialized, &request))
	limit, configured, err := helper.ClaudeWebSearchLimit(request.Tools)
	require.NoError(t, err)
	assert.True(t, configured)
	assert.Equal(t, uint(common.MaxTextToolCallCount), limit)
	encoded, err := common.Marshal(request.Tools)
	require.NoError(t, err)
	var tools []struct {
		MaxUses *uint `json:"max_uses"`
	}
	require.NoError(t, common.Unmarshal(encoded, &tools))
	require.Len(t, tools, 1)
	require.NotNil(t, tools[0].MaxUses)
	assert.Equal(t, uint(common.MaxTextToolCallCount), *tools[0].MaxUses)
}

func TestFinalImageToolReservationUsesGlobalCapAtMaximumSupportedPrice(t *testing.T) {
	three := uint(3)
	request := &dto.OpenAIResponsesRequest{
		Model:        "gpt-5",
		Input:        []byte(`"hello"`),
		MaxToolCalls: &three,
		Tools:        []byte(`[{"type":"image_generation"}]`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		PriceData:       types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}

	quota, err := finalServerToolReservationQuota(info, request)
	require.NoError(t, err)
	unitPrice, ok := dto.OpenAIImageOutputCostUSD("gpt-image-1", "auto", "auto")
	require.True(t, ok)
	expected := common.QuotaFromDecimal(
		decimal.NewFromFloat(unitPrice).
			Mul(decimal.NewFromInt(3)).
			Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())),
	)
	assert.Equal(t, expected, quota)
}

func TestFinalMixedResponsesToolReservationUsesGlobalWorstCase(t *testing.T) {
	three := uint(3)
	request := &dto.OpenAIResponsesRequest{
		Model:        "gpt-5",
		Input:        []byte(`"hello"`),
		MaxToolCalls: &three,
		Tools:        []byte(`[{"type":"web_search"},{"type":"image_generation"}]`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		PriceData:       types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}

	quota, err := finalServerToolReservationQuota(info, request)
	require.NoError(t, err)
	unitPrice, ok := dto.OpenAIImageOutputCostUSD("gpt-image-1", "auto", "auto")
	require.True(t, ok)
	expected := common.QuotaFromDecimal(
		decimal.NewFromFloat(unitPrice).
			Mul(decimal.NewFromInt(3)).
			Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())),
	)
	assert.Equal(t, expected, quota)
}

func TestFinalImageToolReservationIncludesModelSpecificPartials(t *testing.T) {
	two := uint(2)
	request := &dto.OpenAIResponsesRequest{
		Model:        "gpt-5",
		Input:        []byte(`"hello"`),
		MaxToolCalls: &two,
		Stream:       common.GetPointer(true),
		Tools: []byte(`[{
			"type":"image_generation",
			"model":"gpt-image-2",
			"quality":"low",
			"size":"1024x1024",
			"partial_images":3
		}]`),
	}
	info := &relaycommon.RelayInfo{
		OriginModelName: "gpt-5",
		PriceData:       types.PriceData{GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
	}

	quota, err := finalServerToolReservationQuota(info, request)
	require.NoError(t, err)
	basePrice, ok := dto.OpenAIImageOutputCostUSD("gpt-image-2", "low", "1024x1024")
	require.True(t, ok)
	partialPrice, ok := dto.OpenAIImagePartialOutputCostUSD("gpt-image-2", 3)
	require.True(t, ok)
	expected := common.QuotaFromFloat((basePrice + partialPrice) * 2 * common.CurrentQuotaPerUnit())
	assert.Equal(t, expected, quota)
}

func TestGeminiGroundingReservationOnlyIncludesConfiguredServerTools(t *testing.T) {
	tests := []struct {
		name string
		tool dto.GeminiChatTool
		want string
	}{
		{name: "search", tool: dto.GeminiChatTool{GoogleSearch: map[string]any{}}, want: "google_search"},
		{name: "maps", tool: dto.GeminiChatTool{GoogleMaps: map[string]any{}}, want: "google_maps"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &dto.GeminiChatRequest{}
			request.SetTools([]dto.GeminiChatTool{
				{FunctionDeclarations: []any{map[string]any{"name": "local"}}},
				test.tool,
			})

			tools, err := geminiGroundingReservationTools(request)
			require.NoError(t, err)
			assert.Equal(t, []string{test.want}, tools)
		})
	}
}

func TestValidateFinalBillableScalarsRejectsOverrideOverflow(t *testing.T) {
	require.Error(t, validateFinalBillableScalars([]byte(`{"max_tokens":2147483648}`)))
	require.Error(t, validateFinalBillableScalars([]byte(`{"n":0}`)))
	require.Error(t, validateFinalBillableScalars([]byte(`{"n":1.5}`)))
	require.NoError(t, validateFinalBillableScalars([]byte(`{"max_tokens":0,"n":1}`)))
}

func TestUnknownFinalRequestExtendsMaxTokenReservation(t *testing.T) {
	initialMax := uint(10)
	billing := &preflightBillingStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		Request: &dto.GeneralOpenAIRequest{
			Model:     "custom-model",
			MaxTokens: &initialMax,
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelRatio:        1,
			CompletionRatio:   1,
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{"max_tokens":20}`)))
	assert.Equal(t, 120, billing.reserved)
	assert.Equal(t, 120, info.PriceData.QuotaToPreConsume)
}

func TestUnknownFinalNativeCountScalesCompletionReservationOnce(t *testing.T) {
	initialMax := uint(10)
	billing := &preflightBillingStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		Request: &dto.GeneralOpenAIRequest{
			Model:     "custom-model",
			MaxTokens: &initialMax,
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelRatio:        1,
			CompletionRatio:   1,
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	// Native converters may already record the same count for settlement.
	info.PriceData.AddOtherRatio("n", 4)

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{"max_tokens":20,"n":4}`)))

	// 100 existing + (20 max tokens * 4 outputs), not another *4 from OtherRatios.
	assert.Equal(t, 180, billing.reserved)
	assert.Equal(t, 180, info.PriceData.QuotaToPreConsume)
}

func TestFinalNativeBillableScalarBounds(t *testing.T) {
	tests := []struct {
		name    string
		apiType int
		model   string
		body    string
	}{
		{name: "AWS Nova max tokens", apiType: constant.APITypeAws, body: `{"inferenceConfig":{"maxTokens":2147483648}}`},
		{name: "SiliconFlow batch size", apiType: constant.APITypeSiliconFlow, body: `{"batch_size":5}`},
		{name: "Replicate output count", apiType: constant.APITypeReplicate, body: `{"input":{"num_outputs":129}}`},
		{name: "Ali output count", apiType: constant.APITypeAli, body: `{"parameters":{"n":0}}`},
		{name: "Imagen output count", apiType: constant.APITypeGemini, model: "imagen-4.0-generate-001", body: `{"parameters":{"sampleCount":5}}`},
		{name: "Ali prompt extend type", apiType: constant.APITypeAli, body: `{"parameters":{"prompt_extend":1}}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{
				ApiType:           test.apiType,
				UpstreamModelName: test.model,
			}}
			_, err := parseFinalBillableScalars(info, []byte(test.body))
			require.Error(t, err)
		})
	}

	// Provider-native names in an unrelated payload must not affect generic
	// text validation or become an output-count multiplier.
	scalars, err := parseFinalBillableScalars(nil, []byte(`{
		"inferenceConfig":{"maxTokens":2147483648},
		"batch_size":999,
		"input":{"num_outputs":999},
		"parameters":{"n":999,"sampleCount":999,"prompt_extend":1}
	}`))
	require.NoError(t, err)
	assert.False(t, scalars.hasMaxTokens)
	assert.False(t, scalars.hasCount)
}

func TestUnknownFinalAWSNovaExtendsNestedMaxTokenReservation(t *testing.T) {
	initialMax := uint(10)
	billing := &preflightBillingStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		Request: &dto.GeneralOpenAIRequest{
			Model:     "amazon.nova-pro-v1:0",
			MaxTokens: &initialMax,
		},
		ChannelMeta: &relaycommon.ChannelMeta{ApiType: constant.APITypeAws},
		Billing:     billing,
		PriceData: types.PriceData{
			ModelRatio:        1,
			CompletionRatio:   1,
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{
		"schemaVersion":"messages-v1",
		"inferenceConfig":{"maxTokens":20}
	}`)))
	assert.Equal(t, 120, billing.reserved)
	assert.Equal(t, 120, info.PriceData.QuotaToPreConsume)

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{
		"schemaVersion":"messages-v1",
		"inferenceConfig":{"maxTokens":0}
	}`)))
	assert.Equal(t, 120, billing.reserved)
}

func TestUnknownFinalAliImageReservesNestedCountAndPromptExtend(t *testing.T) {
	modelPrice := 0.01
	baseQuota := common.QuotaFromFloat(modelPrice * common.QuotaPerUnit)
	billing := &preflightBillingStub{reserved: baseQuota}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeAli,
			UpstreamModelName: "z-image-turbo",
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelPrice:        modelPrice,
			UsePrice:          true,
			QuotaToPreConsume: baseQuota,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	info.PriceData.AddOtherRatio("n", 1)

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"z-image-turbo",
		"input":{"prompt":"cat"},
		"parameters":{"n":4,"prompt_extend":true}
	}`)))

	expected := common.QuotaFromFloat(modelPrice * common.QuotaPerUnit * 4 * 2)
	assert.Equal(t, expected, billing.reserved)
	assert.Equal(t, expected, info.PriceData.QuotaToPreConsume)
	assert.Equal(t, float64(4), info.PriceData.OtherRatios()["n"])
	assert.Equal(t, float64(2), info.PriceData.OtherRatios()["prompt_extend"])
}

func TestFinalXAIImageEditReservesOutputsResolutionAndInputs(t *testing.T) {
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
	})
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"grok-imagine-image-quality":0.05}`))

	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("group", "default")
	baseQuota := common.QuotaFromFloat(0.05 * common.CurrentQuotaPerUnit())
	billing := &preflightBillingStub{reserved: baseQuota}
	info := &relaycommon.RelayInfo{
		OriginModelName: "grok-imagine-image-quality",
		UserGroup:       "default",
		UsingGroup:      "default",
		RelayFormat:     types.RelayFormatOpenAIImage,
		RelayMode:       relayconstant.RelayModeImagesEdits,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeXai,
			ChannelType:       constant.ChannelTypeXai,
			UpstreamModelName: "grok-imagine-image-quality",
		},
		Billing: billing,
	}

	request, err := refreshFinalOpenAIImageBilling(ctx, info, []byte(`{
		"model":"grok-imagine-image-quality",
		"prompt":"combine",
		"n":2,
		"resolution":"2k",
		"images":[
			{"url":"https://example.com/one.png"},
			{"file_id":"file_two"},
			{"url":"data:image/png;base64,YQ=="}
		]
	}`))

	require.NoError(t, err)
	assert.Equal(t, 3, request.InputImageCount)
	assert.InDelta(t, 3.4, info.PriceData.OtherRatios()[dto.XAIImageBillingRatioKey], 1e-12)
	expected := common.QuotaFromFloat(0.17 * common.CurrentQuotaPerUnit())
	assert.Equal(t, expected, billing.reserved)
	assert.Equal(t, expected, info.PriceData.QuotaToPreConsume)
}

func TestUnknownFinalAliImageClearsDisabledPromptExtendRatio(t *testing.T) {
	modelPrice := 0.01
	baseQuota := common.QuotaFromFloat(modelPrice * common.QuotaPerUnit)
	billing := &preflightBillingStub{reserved: baseQuota * 2}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeAli,
			UpstreamModelName: "z-image-turbo",
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelPrice:        modelPrice,
			UsePrice:          true,
			QuotaToPreConsume: baseQuota,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	info.PriceData.AddOtherRatio("n", 1)
	info.PriceData.AddOtherRatio("prompt_extend", 2)

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"z-image-turbo",
		"parameters":{"n":1,"prompt_extend":false}
	}`)))

	assert.Equal(t, baseQuota, info.PriceData.QuotaToPreConsume)
	assert.False(t, info.PriceData.HasOtherRatio("prompt_extend"))
}

func TestUnknownFinalReplicateReservesNestedOutputCount(t *testing.T) {
	modelPrice := 0.04
	baseQuota := common.QuotaFromFloat(modelPrice * common.QuotaPerUnit)
	billing := &preflightBillingStub{reserved: baseQuota}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType: constant.APITypeReplicate,
		},
		Billing: billing,
		PriceData: types.PriceData{
			ModelPrice:        modelPrice,
			UsePrice:          true,
			QuotaToPreConsume: baseQuota,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{
		"input":{"prompt":"cat","num_outputs":4}
	}`)))

	assert.Equal(t, baseQuota*4, billing.reserved)
	assert.Equal(t, float64(4), info.PriceData.OtherRatios()["n"])
}

func TestUnknownFinalSiliconFlowRejectsUnsupportedBatchOverride(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeSiliconFlow,
			UpstreamModelName: "Qwen/Qwen-Image-Edit-2509",
		},
	}

	err := refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"Qwen/Qwen-Image-Edit-2509",
		"prompt":"cat",
		"batch_size":2
	}`))
	require.ErrorContains(t, err, "batch_size greater than 1 is only supported")
}

func TestUnknownFinalRatioPricedAliImageScalesReservation(t *testing.T) {
	billing := &preflightBillingStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeAli,
			UpstreamModelName: "z-image-turbo",
		},
		Billing: billing,
		PriceData: types.PriceData{
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"z-image-turbo",
		"parameters":{"n":4,"prompt_extend":true}
	}`)))

	assert.Equal(t, 800, billing.reserved)
	assert.Equal(t, 800, info.PriceData.QuotaToPreConsume)
}

func TestUnknownFinalTieredImageRejectsNativeCountChange(t *testing.T) {
	initialCount := uint(1)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		Request:     &dto.ImageRequest{Model: "z-image-turbo", Prompt: "cat", N: &initialCount},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeAli,
			UpstreamModelName: "z-image-turbo",
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{},
		PriceData:             types.PriceData{QuotaToPreConsume: 100},
	}

	err := refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"z-image-turbo",
		"parameters":{"n":4,"prompt_extend":false}
	}`))
	require.ErrorContains(t, err, "count changed after tiered pre-consume")
	assert.Equal(t, 100, info.PriceData.QuotaToPreConsume)
}

func TestUnknownFinalImageRejectsMiniMaxCountOverrideAboveProviderLimit(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeMiniMax,
			UpstreamModelName: "image-01",
		},
		PriceData: types.PriceData{
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	err := refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"image-01",
		"prompt":"cat",
		"n":10
	}`))

	require.ErrorContains(t, err, "MiniMax image n")
	assert.Equal(t, 100, info.PriceData.QuotaToPreConsume)
}

func TestUnknownFinalRequestRejectsOllamaMultipleChoiceOverride(t *testing.T) {
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAI,
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeOllama,
			UpstreamModelName: "llama3",
		},
		PriceData: types.PriceData{
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	err := refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"llama3",
		"n":2
	}`))

	require.ErrorContains(t, err, "exactly one choice")
	assert.Equal(t, 100, info.PriceData.QuotaToPreConsume)
}

func TestUnknownFinalTieredImageDoesNotMultiplyUnchangedCount(t *testing.T) {
	initialCount := uint(4)
	billing := &preflightBillingStub{reserved: 100}
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		Request:     &dto.ImageRequest{Model: "z-image-turbo", Prompt: "cat", N: &initialCount},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeAli,
			UpstreamModelName: "z-image-turbo",
		},
		Billing:               billing,
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{},
		PriceData:             types.PriceData{QuotaToPreConsume: 100},
	}
	info.PriceData.AddOtherRatio("n", 4)

	require.NoError(t, refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"z-image-turbo",
		"parameters":{"n":4,"prompt_extend":false}
	}`)))
	assert.Equal(t, 100, billing.reserved)
	assert.Equal(t, 100, info.PriceData.QuotaToPreConsume)
}

func TestUnknownFinalTieredImageRejectsPromptExtendChange(t *testing.T) {
	initialCount := uint(1)
	info := &relaycommon.RelayInfo{
		RelayFormat: types.RelayFormatOpenAIImage,
		Request:     &dto.ImageRequest{Model: "z-image-turbo", Prompt: "cat", N: &initialCount},
		ChannelMeta: &relaycommon.ChannelMeta{
			ApiType:           constant.APITypeAli,
			UpstreamModelName: "z-image-turbo",
		},
		TieredBillingSnapshot: &billingexpr.BillingSnapshot{},
		PriceData:             types.PriceData{QuotaToPreConsume: 100},
	}
	// This provisional ratio reflects the pre-override converted request.
	info.PriceData.AddOtherRatio("prompt_extend", 2)

	err := refreshUnknownFinalBilling(nil, info, []byte(`{
		"model":"z-image-turbo",
		"parameters":{"n":1,"prompt_extend":false}
	}`))
	require.ErrorContains(t, err, "prompt_extend changed after tiered pre-consume")
	assert.Equal(t, 100, info.PriceData.QuotaToPreConsume)
}
