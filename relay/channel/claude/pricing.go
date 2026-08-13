package claude

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/gin-gonic/gin"
)

// applyClaudeRefusalBilling preserves Anthropic's reported usage for clients,
// but replaces the internal settlement usage when Fable refuses before
// producing output. Anthropic reports input tokens for these HTTP 200
// responses, while explicitly documenting that those tokens are not charged.
func applyClaudeRefusalBilling(c *gin.Context, responseModel string, usage *dto.Usage) {
	if c == nil || responseModel != "claude-fable-5" || usage == nil || usage.CompletionTokens != 0 ||
		common.GetContextKeyString(c, constant.ContextKeyAdminRejectReason) != "claude_stop_reason=refusal" {
		return
	}
	usage.BillingUsage = &dto.BillingUsage{
		Source:       dto.BillingUsageSourceClaudeMessages,
		Semantic:     dto.BillingUsageSemanticAnthropic,
		VerifiedZero: true,
		ClaudeUsage:  &dto.ClaudeUsage{},
	}
}

// applyClaudeUsagePricing uses the speed that Anthropic reports, rather than
// the requested speed, because unsupported models can run and bill at standard
// speed. Administrator-defined fixed, ratio, and expression pricing remains
// authoritative.
func applyClaudeUsagePricing(info *relaycommon.RelayInfo, usage *dto.Usage) {
	if info == nil || usage == nil || info.ChannelType != constant.ChannelTypeAnthropic ||
		info.PriceData.UsePrice || info.TieredBillingSnapshot != nil ||
		!strings.EqualFold(usage.ClaudeSpeed, "fast") {
		return
	}

	defaultRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	if !ok || info.PriceData.ModelRatio != defaultRatio ||
		info.PriceData.CompletionRatio != ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) ||
		info.PriceData.CacheRatio != ratio_setting.GetDefaultCacheRatio(info.OriginModelName) ||
		info.PriceData.CacheCreationRatio != ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName) {
		return
	}

	fastRatios, ok := ratio_setting.GetClaudeFastPriceRatios(info.OriginModelName)
	if !ok {
		return
	}
	info.PriceData.ModelRatio = fastRatios.ModelRatio
	info.PriceData.CompletionRatio = fastRatios.CompletionRatio
}
