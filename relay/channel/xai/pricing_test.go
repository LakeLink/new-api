package xai

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
)

func defaultXAIPriceData(model string) types.PriceData {
	return types.PriceData{
		ModelRatio:         ratio_setting.GetDefaultModelRatioMap()[model],
		CompletionRatio:    ratio_setting.GetDefaultCompletionRatio(model),
		CacheRatio:         ratio_setting.GetDefaultCacheRatio(model),
		CacheCreationRatio: ratio_setting.GetDefaultCreateCacheRatio(model),
	}
}

func TestApplyXAIUsagePricingStacksDynamicFallbacks(t *testing.T) {
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai},
		OriginModelName: "grok-4.5",
		PriceData:       defaultXAIPriceData("grok-4.5"),
	}
	usage := &dto.Usage{
		PromptTokens:      ratio_setting.XAILongContextThreshold + 1,
		ActualServiceTier: "priority",
	}

	ApplyXAIUsagePricing(info, usage)
	ApplyXAIUsagePricing(info, usage)

	assert.Equal(t, 2.0, info.PriceData.OtherRatios()["xai_priority"])
	assert.Equal(t, 2.0, info.PriceData.OtherRatios()["xai_long_context"])
	assert.Equal(t, 4.0, info.PriceData.OtherRatioMultiplier())
}

func TestApplyXAIUsagePricingKeepsCustomRatios(t *testing.T) {
	priceData := defaultXAIPriceData("grok-4.5")
	priceData.ModelRatio = 1.1
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai},
		OriginModelName: "grok-4.5",
		PriceData:       priceData,
	}

	ApplyXAIUsagePricing(info, &dto.Usage{ActualServiceTier: "priority"})

	assert.Empty(t, info.PriceData.OtherRatios())
}

func TestNormalizeXAIUsagePreservesProviderBillingMetadata(t *testing.T) {
	ticks := int64(123456789)
	usage := &dto.Usage{
		PromptTokens:   10,
		TotalTokens:    18,
		CostInUSDTicks: &ticks,
		CompletionTokenDetails: dto.OutputTokenDetails{
			ReasoningTokens: 3,
		},
	}

	normalizeXAIUsage(usage, "priority")

	assert.Equal(t, 8, usage.CompletionTokens)
	assert.Equal(t, 5, usage.CompletionTokenDetails.TextTokens)
	assert.Equal(t, "priority", usage.ActualServiceTier)
	assert.Equal(t, ticks, *usage.CostInUSDTicks)
}

func TestNormalizeXAIUsageKeepsExplicitCompletionWithoutTotal(t *testing.T) {
	usage := &dto.Usage{CompletionTokens: 7}

	normalizeXAIUsage(usage, "")

	assert.Equal(t, 7, usage.CompletionTokens)
	assert.Equal(t, 7, usage.CompletionTokenDetails.TextTokens)
}
