package xai

import (
	"strings"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
)

// ApplyXAIUsagePricing applies xAI's dynamic token multipliers only to the
// built-in first-party prices. Provider-reported exact cost, when present, is
// used later during settlement and supersedes this safe fallback.
func ApplyXAIUsagePricing(info *relaycommon.RelayInfo, usage *dto.Usage) {
	if info == nil || usage == nil || info.PriceData.UsePrice || info.TieredBillingSnapshot != nil {
		return
	}
	defaultRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	if !ok || info.PriceData.ModelRatio != defaultRatio ||
		info.PriceData.CompletionRatio != ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) ||
		info.PriceData.CacheRatio != ratio_setting.GetDefaultCacheRatio(info.OriginModelName) ||
		info.PriceData.CacheCreationRatio != ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName) {
		return
	}

	if strings.EqualFold(usage.ActualServiceTier, "priority") && !info.PriceData.HasOtherRatio("xai_priority") {
		info.PriceData.AddOtherRatio("xai_priority", 2)
	}
	if usage.PromptTokens > ratio_setting.XAILongContextThreshold &&
		ratio_setting.IsXAILongContextModel(info.OriginModelName) &&
		!info.PriceData.HasOtherRatio("xai_long_context") {
		info.PriceData.AddOtherRatio("xai_long_context", 2)
	}
}
