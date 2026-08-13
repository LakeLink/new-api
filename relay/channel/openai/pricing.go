package openai

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// applyOpenAIUsagePricing reconciles dynamic OpenAI prices using the tier that
// actually processed the request. This is intentionally limited to the
// built-in OpenAI prices: administrator-defined fixed or token ratios remain
// authoritative.
func applyOpenAIUsagePricing(info *relaycommon.RelayInfo, usage *dto.Usage, actualServiceTier string) {
	if usage != nil {
		usage.ActualServiceTier = actualServiceTier
	}
	if info == nil || usage == nil || info.ChannelType != constant.ChannelTypeOpenAI || info.PriceData.UsePrice || info.TieredBillingSnapshot != nil {
		return
	}

	defaultRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	if !ok || info.PriceData.ModelRatio != defaultRatio ||
		info.PriceData.CompletionRatio != ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) ||
		info.PriceData.CacheRatio != ratio_setting.GetDefaultCacheRatio(info.OriginModelName) ||
		info.PriceData.CacheCreationRatio != ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName) {
		return
	}

	if strings.EqualFold(actualServiceTier, "priority") {
		if prices, ok := ratio_setting.GetOpenAIPriorityPriceRatios(info.OriginModelName); ok {
			info.PriceData.ModelRatio = prices.ModelRatio
			info.PriceData.CompletionRatio = prices.CompletionRatio
			info.PriceData.CacheRatio = prices.CacheRatio
			if prices.CacheCreationRatio > 0 {
				info.PriceData.CacheCreationRatio = prices.CacheCreationRatio
				info.PriceData.CacheCreation5mRatio = info.PriceData.CacheCreationRatio
			}
		}
		// OpenAI does not support Priority processing for long-context
		// requests, so the >272K multiplier cannot compose with this tier.
		return
	}

	if strings.EqualFold(actualServiceTier, "flex") {
		// Flex token prices match Batch prices: half the standard rate.
		// Relative output and cache ratios stay unchanged.
		info.PriceData.ModelRatio *= 0.5
	}

	if usage.PromptTokens <= ratio_setting.OpenAILongContextThreshold || !ratio_setting.IsOpenAILongContextModel(info.OriginModelName) {
		return
	}

	// OpenAI charges the full session at 2x input and 1.5x output when the
	// prompt crosses 272K tokens. Doubling the base ratio and scaling the
	// completion ratio by 0.75 expresses those two independent multipliers.
	info.PriceData.ModelRatio *= 2
	info.PriceData.CompletionRatio *= 0.75
}

func preserveProviderUsageMetadata(destination, source *dto.Usage) {
	if destination == nil || source == nil {
		return
	}
	destination.Cost = source.Cost
	destination.CostInUSDTicks = source.CostInUSDTicks
	destination.ActualServiceTier = source.ActualServiceTier
}

func lastStreamResponseServiceTier(data string) string {
	if data == "" {
		return ""
	}
	var response dto.ChatCompletionsStreamResponse
	if err := common.UnmarshalJsonStr(data, &response); err != nil {
		return ""
	}
	return response.ServiceTier
}
