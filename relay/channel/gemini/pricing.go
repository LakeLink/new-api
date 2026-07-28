package gemini

import (
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// applyGeminiUsagePricing reconciles Gemini's service and context-length tiers
// from actual usage. Administrator-defined fixed, ratio, and expression pricing
// remains authoritative.
func applyGeminiUsagePricing(info *relaycommon.RelayInfo, usage *dto.Usage) {
	if info == nil || usage == nil ||
		(info.ChannelType != constant.ChannelTypeGemini && info.ChannelType != constant.ChannelTypeVertexAi) ||
		info.PriceData.UsePrice || info.TieredBillingSnapshot != nil {
		return
	}

	defaultRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	if !ok || info.PriceData.ModelRatio != defaultRatio ||
		info.PriceData.CompletionRatio != ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) ||
		info.PriceData.CacheRatio != ratio_setting.GetDefaultCacheRatio(info.OriginModelName) ||
		info.PriceData.CacheCreationRatio != ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName) {
		return
	}

	if info.ChannelType == constant.ChannelTypeGemini {
		if tierPricing, ok := ratio_setting.GetGeminiServiceTierPriceRatios(info.OriginModelName, usage.GeminiServiceTier); ok {
			info.PriceData.ModelRatio *= tierPricing.ModelMultiplier
			info.PriceData.CacheRatio = tierPricing.CacheRatio
		}
	}

	// Gemini Pro doubles input and cached-input prices above 200K tokens while
	// output increases by 1.5x. Service-tier-relative cache ratios stay intact.
	if usage.PromptTokens > ratio_setting.GeminiLongContextThreshold &&
		ratio_setting.IsGeminiLongContextModel(info.OriginModelName) {
		info.PriceData.ModelRatio *= 2
		info.PriceData.CompletionRatio *= 0.75
	}

	// Native image models report image-output tokens alongside text/thinking
	// output tokens, but those modalities have different prices. The shared
	// quota engine has one completion ratio, so use the exact weighted ratio
	// for this response. This preserves text/thinking pricing and charges image
	// tokens at the provider's separate image-output rate.
	imageTokens := usage.CompletionTokenDetails.ImageTokens
	if imageRatio, ok := ratio_setting.GetGeminiImageOutputRatio(info.OriginModelName); ok &&
		imageTokens > 0 && imageTokens <= usage.CompletionTokens {
		textAndThinkingTokens := usage.CompletionTokens - imageTokens
		weightedOutput := float64(textAndThinkingTokens)*info.PriceData.CompletionRatio +
			float64(imageTokens)*imageRatio
		info.PriceData.CompletionRatio = weightedOutput / float64(usage.CompletionTokens)
	}
}
