package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	perfmetrics "github.com/QuantumNous/new-api/pkg/perf_metrics"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type textQuotaSummary struct {
	PromptTokens             int
	CompletionTokens         int
	TotalTokens              int
	CacheTokens              int
	CacheCreationTokens      int
	CacheCreationTokens5m    int
	CacheCreationTokens1h    int
	ImageTokens              int
	AudioTokens              int
	CachedAudioTokens        int
	CachedImageTokens        int
	ModelName                string
	TokenName                string
	UsePrice                 bool
	UseTimeSeconds           int64
	CompletionRatio          float64
	CacheRatio               float64
	ImageRatio               float64
	ModelRatio               float64
	GroupRatio               float64
	ModelPrice               float64
	CacheCreationRatio       float64
	CacheCreationRatio5m     float64
	CacheCreationRatio1h     float64
	Quota                    int
	IsClaudeUsageSemantic    bool
	UsageSemantic            string
	GeminiServiceTier        string
	ActualServiceTier        string
	WebSearchPrice           float64
	WebSearchCallCount       int
	ClaudeWebSearchPrice     float64
	ClaudeWebSearchCallCount int
	FileSearchPrice          float64
	FileSearchCallCount      int
	GeminiSearchPrice        float64
	GeminiSearchCallCount    int
	GeminiSearchTool         string
	AudioInputPrice          float64
	AudioInputQuota          decimal.Decimal
	ImageGenerationCallPrice float64
	ImageGenerationCallCount int
	ImageGenerationCostUSD   float64
	ImageGenerationModel     string
	ImageGenerationPartials  int
	ToolCallSurchargeQuota   decimal.Decimal
	ProviderRequestFeeUSD    float64
	ProviderRequestFeeQuota  decimal.Decimal
	ProviderReportedCost     *providerReportedCost
	BillingError             error
}

func cacheWriteTokensTotal(summary textQuotaSummary) int {
	if summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0 {
		splitCacheWriteTokens := summary.CacheCreationTokens5m + summary.CacheCreationTokens1h
		if summary.CacheCreationTokens > splitCacheWriteTokens {
			return summary.CacheCreationTokens
		}
		return splitCacheWriteTokens
	}
	return summary.CacheCreationTokens
}

func validNonNegativeBillingFactor(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateTextQuotaBillingFactors(summary textQuotaSummary) error {
	factors := []struct {
		name  string
		value float64
	}{
		{name: "completion_ratio", value: summary.CompletionRatio},
		{name: "cache_ratio", value: summary.CacheRatio},
		{name: "image_ratio", value: summary.ImageRatio},
		{name: "model_ratio", value: summary.ModelRatio},
		{name: "group_ratio", value: summary.GroupRatio},
		{name: "model_price", value: summary.ModelPrice},
		{name: "cache_creation_ratio", value: summary.CacheCreationRatio},
		{name: "cache_creation_ratio_5m", value: summary.CacheCreationRatio5m},
		{name: "cache_creation_ratio_1h", value: summary.CacheCreationRatio1h},
		{name: "quota_per_unit", value: common.CurrentQuotaPerUnit()},
	}
	for _, factor := range factors {
		if !validNonNegativeBillingFactor(factor.value) {
			return fmt.Errorf("billing factor %s is negative or non-finite", factor.name)
		}
	}
	return nil
}

func isLegacyClaudeDerivedOpenAIUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) bool {
	if relayInfo == nil || usage == nil {
		return false
	}
	if relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return false
	}
	if usage.UsageSource != "" || usage.UsageSemantic != "" {
		return false
	}
	return usage.ClaudeCacheCreation5mTokens > 0 || usage.ClaudeCacheCreation1hTokens > 0
}

func calculateTextToolCallSurcharge(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, summary *textQuotaSummary) decimal.Decimal {
	quotaPerUnit := common.CurrentQuotaPerUnit()
	if !validNonNegativeBillingFactor(summary.GroupRatio) || !validNonNegativeBillingFactor(quotaPerUnit) {
		summary.BillingError = errors.New("server-tool billing multiplier is negative or non-finite")
		return decimal.Zero
	}
	dGroupRatio := decimal.NewFromFloat(summary.GroupRatio)
	dQuotaPerUnit := decimal.NewFromFloat(quotaPerUnit)

	var surcharge decimal.Decimal

	if relayInfo.ResponsesUsageInfo != nil {
		webSearchType, webSearchTool, err := relayInfo.ResponsesUsageInfo.WebSearchTool()
		if err == nil && webSearchTool != nil && webSearchTool.CallCount > 0 {
			priceKey, _ := dto.ResponsesWebSearchPricingKey(webSearchType)
			summary.WebSearchCallCount = webSearchTool.CallCount
			summary.WebSearchPrice = operation_setting.GetToolPriceForModel(priceKey, summary.ModelName)
			if !validNonNegativeBillingFactor(summary.WebSearchPrice) {
				summary.BillingError = errors.New("web-search billing price is negative or non-finite")
				return decimal.Zero
			}
			surcharge = surcharge.Add(decimal.NewFromFloat(summary.WebSearchPrice).
				Mul(decimal.NewFromInt(int64(webSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).
				Mul(dGroupRatio).
				Mul(dQuotaPerUnit))
		}
	} else if strings.HasSuffix(summary.ModelName, "search-preview") {
		summary.WebSearchCallCount = 1
		summary.WebSearchPrice = operation_setting.GetToolPriceForModel("web_search_preview", summary.ModelName)
		if !validNonNegativeBillingFactor(summary.WebSearchPrice) {
			summary.BillingError = errors.New("web-search billing price is negative or non-finite")
			return decimal.Zero
		}
		surcharge = surcharge.Add(decimal.NewFromFloat(summary.WebSearchPrice).
			Div(decimal.NewFromInt(1000)).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit))
	}

	summary.ClaudeWebSearchCallCount = ctx.GetInt("claude_web_search_requests")
	if summary.ClaudeWebSearchCallCount > 0 {
		summary.ClaudeWebSearchPrice = operation_setting.GetToolPrice("web_search")
		if !validNonNegativeBillingFactor(summary.ClaudeWebSearchPrice) {
			summary.BillingError = errors.New("Claude web-search billing price is negative or non-finite")
			return decimal.Zero
		}
		surcharge = surcharge.Add(decimal.NewFromFloat(summary.ClaudeWebSearchPrice).
			Div(decimal.NewFromInt(1000)).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit).
			Mul(decimal.NewFromInt(int64(summary.ClaudeWebSearchCallCount))))
	}

	if relayInfo.ResponsesUsageInfo != nil {
		if fileSearchTool, exists := relayInfo.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]; exists && fileSearchTool.CallCount > 0 {
			summary.FileSearchCallCount = fileSearchTool.CallCount
			summary.FileSearchPrice = operation_setting.GetToolPriceForModel("file_search", summary.ModelName)
			if !validNonNegativeBillingFactor(summary.FileSearchPrice) {
				summary.BillingError = errors.New("file-search billing price is negative or non-finite")
				return decimal.Zero
			}
			surcharge = surcharge.Add(decimal.NewFromFloat(summary.FileSearchPrice).
				Mul(decimal.NewFromInt(int64(fileSearchTool.CallCount))).
				Div(decimal.NewFromInt(1000)).
				Mul(dGroupRatio).
				Mul(dQuotaPerUnit))
		}
	}

	summary.GeminiSearchCallCount = common.GetContextKeyInt(ctx, constant.ContextKeyGeminiGroundingSearchCount)
	if summary.GeminiSearchCallCount > 0 {
		summary.GeminiSearchTool = common.GetContextKeyString(ctx, constant.ContextKeyGeminiGroundingTool)
		if summary.GeminiSearchTool == "" {
			summary.GeminiSearchTool = "google_search"
		}
		summary.GeminiSearchPrice = operation_setting.GetToolPriceForModel(summary.GeminiSearchTool, summary.ModelName)
		if !validNonNegativeBillingFactor(summary.GeminiSearchPrice) {
			summary.BillingError = errors.New("Gemini grounding billing price is negative or non-finite")
			return decimal.Zero
		}
		surcharge = surcharge.Add(decimal.NewFromFloat(summary.GeminiSearchPrice).
			Mul(decimal.NewFromInt(int64(summary.GeminiSearchCallCount))).
			Div(decimal.NewFromInt(1000)).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit))
	}

	imageGenerationCallCount := 0
	var imageGenerationTool *relaycommon.BuildInToolInfo
	if relayInfo.ResponsesUsageInfo != nil {
		if imageGenerationTool = relayInfo.ResponsesUsageInfo.BuiltInTools["image_generation"]; imageGenerationTool != nil {
			imageGenerationCallCount = imageGenerationTool.CallCount
		}
	}
	// Preserve the legacy one-call signal for non-Responses integrations while
	// Responses settlement uses exact output-item counters.
	if imageGenerationCallCount == 0 && relayInfo.ResponsesUsageInfo == nil && ctx.GetBool("image_generation_call") {
		imageGenerationCallCount = 1
	}
	if imageGenerationCallCount > 0 {
		summary.ImageGenerationCallCount = imageGenerationCallCount
		if imageGenerationTool == nil {
			summary.ImageGenerationModel = "gpt-image-1"
			summary.ImageGenerationCallPrice = operation_setting.GetGPTImage1PriceOnceCall(
				ctx.GetString("image_generation_call_quality"),
				ctx.GetString("image_generation_call_size"),
			)
			summary.ImageGenerationCostUSD = summary.ImageGenerationCallPrice
		} else {
			modelName := imageGenerationTool.ImageModel
			if modelName == "" {
				modelName = "gpt-image-1"
			}
			summary.ImageGenerationModel = modelName
			for callIndex := 0; callIndex < imageGenerationCallCount; callIndex++ {
				quality := imageGenerationTool.ImageQuality
				size := imageGenerationTool.ImageSize
				if callIndex < len(imageGenerationTool.ImageOutputs) {
					if imageGenerationTool.ImageOutputs[callIndex].Quality != "" {
						quality = imageGenerationTool.ImageOutputs[callIndex].Quality
					}
					if imageGenerationTool.ImageOutputs[callIndex].Size != "" {
						size = imageGenerationTool.ImageOutputs[callIndex].Size
					}
				}
				if quality == "" {
					quality = ctx.GetString("image_generation_call_quality")
				}
				if size == "" {
					size = ctx.GetString("image_generation_call_size")
				}
				price, ok := dto.OpenAIImageOutputCostUSD(modelName, quality, size)
				if !ok {
					summary.BillingError = fmt.Errorf("unsupported image generation pricing for model %s", modelName)
					return decimal.Zero
				}
				summary.ImageGenerationCostUSD += price
			}
			summary.ImageGenerationPartials = imageGenerationTool.ImagePartialCount
			maxPartials := imageGenerationTool.ImagePartialImages * imageGenerationCallCount
			if summary.ImageGenerationPartials > maxPartials {
				summary.ImageGenerationPartials = maxPartials
			}
			partialPrice, ok := dto.OpenAIImagePartialOutputCostUSD(
				modelName,
				summary.ImageGenerationPartials,
			)
			if !ok {
				summary.BillingError = fmt.Errorf("unsupported image partial pricing for model %s", modelName)
				return decimal.Zero
			}
			summary.ImageGenerationCostUSD += partialPrice
			summary.ImageGenerationCallPrice = summary.ImageGenerationCostUSD / float64(imageGenerationCallCount)
		}
		if !validNonNegativeBillingFactor(summary.ImageGenerationCallPrice) ||
			!validNonNegativeBillingFactor(summary.ImageGenerationCostUSD) {
			summary.BillingError = errors.New("image generation call price is negative or non-finite")
			return decimal.Zero
		}
		surcharge = surcharge.Add(decimal.NewFromFloat(summary.ImageGenerationCostUSD).
			Mul(dGroupRatio).
			Mul(dQuotaPerUnit))
	}

	if surcharge.IsNegative() {
		summary.BillingError = errors.New("server-tool billing surcharge is negative")
		return decimal.Zero
	}
	return surcharge
}

func validateTextToolCallCounters(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) error {
	responsesTotal := 0
	if relayInfo != nil && relayInfo.ResponsesUsageInfo != nil {
		if _, _, err := relayInfo.ResponsesUsageInfo.WebSearchTool(); err != nil {
			return fmt.Errorf("validate Responses web-search billing configuration: %w", err)
		}
		for name, tool := range relayInfo.ResponsesUsageInfo.BuiltInTools {
			if tool == nil {
				continue
			}
			if err := validateTextToolCallCount("responses."+name, tool.CallCount); err != nil {
				return err
			}
			if tool.CallCount > common.MaxTextToolCallCount-responsesTotal {
				return fmt.Errorf("billing tool counters for Responses exceed limit %d", common.MaxTextToolCallCount)
			}
			responsesTotal += tool.CallCount
			if name == "image_generation" {
				if tool.ImagePartialImages < 0 || tool.ImagePartialImages > dto.MaxOpenAIImagePartialImages {
					return fmt.Errorf("billing partial_images for Responses image generation is invalid: %d", tool.ImagePartialImages)
				}
				if tool.ImagePartialCount < 0 ||
					tool.ImagePartialCount > tool.ImagePartialImages*common.MaxTextToolCallCount {
					return fmt.Errorf(
						"billing partial image counter for Responses exceeds limit %d",
						tool.ImagePartialImages*common.MaxTextToolCallCount,
					)
				}
			}
		}
		maxToolCalls := relayInfo.ResponsesUsageInfo.MaxToolCalls
		if maxToolCalls == nil {
			if request, ok := relayInfo.Request.(*dto.OpenAIResponsesRequest); ok {
				maxToolCalls = request.MaxToolCalls
			}
		}
		if maxToolCalls != nil && uint(responsesTotal) > *maxToolCalls {
			return fmt.Errorf("billing tool counters for Responses exceed effective max_tool_calls %d", *maxToolCalls)
		}
	}
	claudeSearchCount := ctx.GetInt("claude_web_search_requests")
	if err := validateTextToolCallCount("claude.web_search_requests", claudeSearchCount); err != nil {
		return err
	}
	if relayInfo != nil {
		if relayInfo.EffectiveClaudeWebSearchMaxUses != nil {
			if uint(claudeSearchCount) > *relayInfo.EffectiveClaudeWebSearchMaxUses {
				return fmt.Errorf("billing Claude web search count exceeds effective outbound max_uses %d", *relayInfo.EffectiveClaudeWebSearchMaxUses)
			}
		} else if request, ok := relayInfo.Request.(*dto.ClaudeRequest); ok {
			maxUses, found, err := dto.ClaudeWebSearchMaxUses(request.Tools)
			if err != nil {
				return fmt.Errorf("validate Claude web search max_uses: %w", err)
			}
			if found && uint(claudeSearchCount) > maxUses {
				return fmt.Errorf("billing Claude web search count exceeds requested max_uses %d", maxUses)
			}
		}
	}
	return validateTextToolCallCount("gemini.grounding_search_count", common.GetContextKeyInt(ctx, constant.ContextKeyGeminiGroundingSearchCount))
}

func validateTextToolCallCount(name string, count int) error {
	if count < 0 {
		return fmt.Errorf("billing tool counter %s cannot be negative: %d", name, count)
	}
	if count > common.MaxTextToolCallCount {
		return fmt.Errorf("billing tool counter %s exceeds limit %d: %d", name, common.MaxTextToolCallCount, count)
	}
	return nil
}

func normalizeToolCallSurchargeForSharedRatios(surcharge decimal.Decimal, priceData types.PriceData) decimal.Decimal {
	// These provider multipliers apply to token categories, not separately
	// metered server-tool calls. Divide them out before the shared OtherRatios
	// set is applied to the combined subtotal.
	for _, ratioName := range []string{"anthropic_inference_geo", "xai_priority", "xai_long_context"} {
		if ratio, ok := priceData.OtherRatios()[ratioName]; ok && ratio > 0 {
			surcharge = surcharge.Div(decimal.NewFromFloat(ratio))
		}
	}
	return surcharge
}

// noteQuotaClamp records the first quota saturation event onto relayInfo so it
// can later be attached to the consume/task log for admin auditing. First
// non-nil clamp wins (a single request may hit multiple conversions).
func noteQuotaClamp(relayInfo *relaycommon.RelayInfo, clamp *common.QuotaClamp) {
	if clamp == nil || relayInfo == nil {
		return
	}
	if relayInfo.QuotaClamp == nil {
		relayInfo.QuotaClamp = clamp
	}
}

func composeTieredTextQuota(relayInfo *relaycommon.RelayInfo, summary textQuotaSummary, tieredQuota int, tieredResult *billingexpr.TieredResult) int {
	if summary.ToolCallSurchargeQuota.IsZero() {
		return tieredQuota
	}

	if tieredResult != nil {
		if snap := relayInfo.TieredBillingSnapshot; snap != nil {
			quota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromFloat(tieredResult.ActualQuotaBeforeGroup).
				Mul(decimal.NewFromFloat(snap.GroupRatio)).
				Add(summary.ToolCallSurchargeQuota))
			noteQuotaClamp(relayInfo, clamp)
			return quota
		}
	}

	// Saturate the final sum, not just the surcharge: tieredQuota can be near
	// MaxQuota and adding the surcharge could push the total past the int32
	// quota policy bound (persisted quota columns are 32-bit).
	total, clamp := common.QuotaFromDecimalChecked(
		decimal.NewFromInt(int64(tieredQuota)).Add(summary.ToolCallSurchargeQuota),
	)
	noteQuotaClamp(relayInfo, clamp)
	return total
}

// calculateTextQuotaSummary expects a usage already remapped by
// effectiveBillingUsage; PostTextConsumeQuota performs that remap once and shares
// the result with tiered billing, affinity observation and logging.
func calculateTextQuotaSummary(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage) textQuotaSummary {
	return calculateTextQuotaSummaryWithToolUsage(ctx, relayInfo, usage, true)
}

func calculateTextQuotaSummaryWithToolUsage(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, includeToolUsage bool) textQuotaSummary {
	summary := textQuotaSummary{
		ModelName:            relayInfo.OriginModelName,
		TokenName:            ctx.GetString("token_name"),
		UseTimeSeconds:       time.Now().Unix() - relayInfo.StartTime.Unix(),
		CompletionRatio:      relayInfo.PriceData.CompletionRatio,
		CacheRatio:           relayInfo.PriceData.CacheRatio,
		ImageRatio:           relayInfo.PriceData.ImageRatio,
		ModelRatio:           relayInfo.PriceData.ModelRatio,
		GroupRatio:           relayInfo.PriceData.GroupRatioInfo.GroupRatio,
		ModelPrice:           relayInfo.PriceData.ModelPrice,
		UsePrice:             relayInfo.PriceData.UsePrice,
		CacheCreationRatio:   relayInfo.PriceData.CacheCreationRatio,
		CacheCreationRatio5m: relayInfo.PriceData.CacheCreation5mRatio,
		CacheCreationRatio1h: relayInfo.PriceData.CacheCreation1hRatio,
		UsageSemantic:        usageSemanticFromUsage(relayInfo, usage),
	}
	summary.IsClaudeUsageSemantic = summary.UsageSemantic == "anthropic"

	if usage == nil {
		usage = &dto.Usage{
			PromptTokens:     relayInfo.GetEstimatePromptTokens(),
			CompletionTokens: 0,
			TotalTokens:      relayInfo.GetEstimatePromptTokens(),
		}
	}
	summary.GeminiServiceTier = usage.GeminiServiceTier
	summary.ActualServiceTier = usage.ActualServiceTier

	summary.PromptTokens = usage.PromptTokens
	summary.CompletionTokens = usage.CompletionTokens
	summary.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	summary.CacheTokens = usage.PromptTokensDetails.CachedTokens
	summary.CacheCreationTokens = usage.PromptTokensDetails.CacheCreationTokensTotal()
	summary.CacheCreationTokens5m = usage.ClaudeCacheCreation5mTokens
	summary.CacheCreationTokens1h = usage.ClaudeCacheCreation1hTokens
	summary.ImageTokens = usage.PromptTokensDetails.ImageTokens
	summary.AudioTokens = usage.PromptTokensDetails.AudioTokens
	summary.CachedAudioTokens = usage.GeminiCachedAudioInputTokens
	summary.CachedImageTokens = usage.GeminiCachedImageInputTokens
	if summary.CachedAudioTokens < 0 {
		summary.CachedAudioTokens = 0
	}
	if summary.CachedAudioTokens > summary.CacheTokens {
		summary.CachedAudioTokens = summary.CacheTokens
	}
	if summary.CachedAudioTokens > summary.AudioTokens {
		summary.CachedAudioTokens = summary.AudioTokens
	}
	if summary.CachedImageTokens < 0 {
		summary.CachedImageTokens = 0
	}
	remainingCacheTokens := summary.CacheTokens - summary.CachedAudioTokens
	if summary.CachedImageTokens > remainingCacheTokens {
		summary.CachedImageTokens = remainingCacheTokens
	}
	if summary.CachedImageTokens > summary.ImageTokens {
		summary.CachedImageTokens = summary.ImageTokens
	}
	legacyClaudeDerived := isLegacyClaudeDerivedOpenAIUsage(relayInfo, usage)
	isOpenRouterClaudeBilling := relayInfo.ChannelMeta != nil &&
		relayInfo.ChannelType == constant.ChannelTypeOpenRouter &&
		summary.IsClaudeUsageSemantic

	if isOpenRouterClaudeBilling {
		summary.PromptTokens -= summary.CacheTokens
		isUsingCustomSettings := relayInfo.PriceData.UsePrice || hasCustomModelRatio(summary.ModelName, relayInfo.PriceData.ModelRatio)
		if summary.CacheCreationTokens == 0 && relayInfo.PriceData.CacheCreationRatio != 1 && !isUsingCustomSettings {
			maybeCacheCreationTokens := CalcOpenRouterCacheCreateTokens(*usage, relayInfo.PriceData)
			if maybeCacheCreationTokens >= 0 && summary.PromptTokens >= maybeCacheCreationTokens {
				summary.CacheCreationTokens = maybeCacheCreationTokens
			}
		}
		summary.PromptTokens -= summary.CacheCreationTokens
	}
	if err := validateTextQuotaBillingFactors(summary); err != nil {
		summary.BillingError = err
		return summary
	}

	dPromptTokens := decimal.NewFromInt(int64(summary.PromptTokens))
	dCacheTokens := decimal.NewFromInt(int64(summary.CacheTokens))
	dCachedAudioTokens := decimal.NewFromInt(int64(summary.CachedAudioTokens))
	dCachedImageTokens := decimal.NewFromInt(int64(summary.CachedImageTokens))
	dImageTokens := decimal.NewFromInt(int64(summary.ImageTokens))
	dAudioTokens := decimal.NewFromInt(int64(summary.AudioTokens))
	dCompletionTokens := decimal.NewFromInt(int64(summary.CompletionTokens))
	dCachedCreationTokens := decimal.NewFromInt(int64(summary.CacheCreationTokens))
	dCompletionRatio := decimal.NewFromFloat(summary.CompletionRatio)
	dCacheRatio := decimal.NewFromFloat(summary.CacheRatio)
	dImageRatio := decimal.NewFromFloat(summary.ImageRatio)
	dModelRatio := decimal.NewFromFloat(summary.ModelRatio)
	dGroupRatio := decimal.NewFromFloat(summary.GroupRatio)
	dModelPrice := decimal.NewFromFloat(summary.ModelPrice)
	dCacheCreationRatio := decimal.NewFromFloat(summary.CacheCreationRatio)
	dCacheCreationRatio5m := decimal.NewFromFloat(summary.CacheCreationRatio5m)
	dCacheCreationRatio1h := decimal.NewFromFloat(summary.CacheCreationRatio1h)
	dQuotaPerUnit := decimal.NewFromFloat(common.CurrentQuotaPerUnit())

	ratio := dModelRatio.Mul(dGroupRatio)
	if includeToolUsage {
		summary.ToolCallSurchargeQuota = calculateTextToolCallSurcharge(ctx, relayInfo, &summary)
	}
	if feeUSD, feeQuota, ok := perplexityRequestFee(relayInfo); ok {
		summary.ProviderRequestFeeUSD = feeUSD
		summary.ProviderRequestFeeQuota = feeQuota
	}

	var audioInputQuota decimal.Decimal
	if !relayInfo.PriceData.UsePrice {
		baseTokens := dPromptTokens
		if !dAudioTokens.IsZero() {
			summary.AudioInputPrice = operation_setting.GetGeminiInputAudioPriceForServiceTier(summary.ModelName, summary.GeminiServiceTier)
			if !validNonNegativeBillingFactor(summary.AudioInputPrice) {
				summary.BillingError = errors.New("audio input billing price is negative or non-finite")
				return summary
			}
		}

		var cachedTokensWithRatio decimal.Decimal
		if !dCacheTokens.IsZero() {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCacheTokens)
			}
			genericCachedTokens := dCacheTokens
			if summary.AudioInputPrice > 0 {
				genericCachedTokens = genericCachedTokens.Sub(dCachedAudioTokens)
			}
			cachedTokensWithRatio = genericCachedTokens.Mul(dCacheRatio)
		}

		var cachedCreationTokensWithRatio decimal.Decimal
		hasSplitCacheCreationTokens := summary.CacheCreationTokens5m > 0 || summary.CacheCreationTokens1h > 0
		if !dCachedCreationTokens.IsZero() || hasSplitCacheCreationTokens {
			if !summary.IsClaudeUsageSemantic && !legacyClaudeDerived {
				baseTokens = baseTokens.Sub(dCachedCreationTokens)
				cachedCreationTokensWithRatio = dCachedCreationTokens.Mul(dCacheCreationRatio)
			} else {
				remaining := summary.CacheCreationTokens - summary.CacheCreationTokens5m - summary.CacheCreationTokens1h
				if remaining < 0 {
					remaining = 0
				}
				cachedCreationTokensWithRatio = decimal.NewFromInt(int64(remaining)).Mul(dCacheCreationRatio)
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens5m)).Mul(dCacheCreationRatio5m))
				cachedCreationTokensWithRatio = cachedCreationTokensWithRatio.Add(decimal.NewFromInt(int64(summary.CacheCreationTokens1h)).Mul(dCacheCreationRatio1h))
			}
		}

		var imageTokensWithRatio decimal.Decimal
		if !dImageTokens.IsZero() {
			nonCachedImageTokens := dImageTokens.Sub(dCachedImageTokens)
			baseTokens = baseTokens.Sub(nonCachedImageTokens)
			imageTokensWithRatio = nonCachedImageTokens.Mul(dImageRatio)
		}

		if summary.AudioInputPrice > 0 {
			nonCachedAudioTokens := dAudioTokens.Sub(dCachedAudioTokens)
			if !nonCachedAudioTokens.IsZero() || !dCachedAudioTokens.IsZero() {
				baseTokens = baseTokens.Sub(nonCachedAudioTokens)
				billableAudioUnits := nonCachedAudioTokens.Add(dCachedAudioTokens.Mul(dCacheRatio))
				audioInputQuota = decimal.NewFromFloat(summary.AudioInputPrice).
					Div(decimal.NewFromInt(1000000)).Mul(billableAudioUnits).Mul(dGroupRatio).Mul(dQuotaPerUnit)
				summary.AudioInputQuota = audioInputQuota
			}
		}

		// OpenAI cache-write usage reports unadjusted prefix counts, so
		// cached_tokens + cache_write_tokens can exceed prompt_tokens and the
		// remainder can go negative. Clamp at zero so overlap never turns into
		// a negative base charge.
		if baseTokens.IsNegative() {
			baseTokens = decimal.Zero
		}

		promptQuota := baseTokens.Add(cachedTokensWithRatio).Add(imageTokensWithRatio).Add(cachedCreationTokensWithRatio)
		completionQuota := dCompletionTokens.Mul(dCompletionRatio)
		toolCallSurchargeQuota := normalizeToolCallSurchargeForSharedRatios(summary.ToolCallSurchargeQuota, relayInfo.PriceData)
		quotaCalculateDecimal := promptQuota.Add(completionQuota).Mul(ratio)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(toolCallSurchargeQuota)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)
		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ProviderRequestFeeQuota)

		if !ratio.IsZero() && quotaCalculateDecimal.LessThanOrEqual(decimal.Zero) {
			quotaCalculateDecimal = decimal.NewFromInt(1)
		}
		quota, clamp := common.QuotaFromDecimalChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	} else {
		quotaCalculateDecimal := dModelPrice.Mul(dQuotaPerUnit).Mul(dGroupRatio)
		toolCallSurchargeQuota := normalizeToolCallSurchargeForSharedRatios(summary.ToolCallSurchargeQuota, relayInfo.PriceData)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(toolCallSurchargeQuota)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(audioInputQuota)
		quotaCalculateDecimal = relayInfo.PriceData.ApplyOtherRatiosToDecimal(quotaCalculateDecimal)
		quotaCalculateDecimal = quotaCalculateDecimal.Add(summary.ProviderRequestFeeQuota)
		quota, clamp := common.QuotaFromDecimalChecked(quotaCalculateDecimal)
		summary.Quota = quota
		noteQuotaClamp(relayInfo, clamp)
	}

	if !hasBillableTextUsage(summary, relayInfo) {
		summary.Quota = 0
	} else if !ratio.IsZero() && summary.Quota == 0 {
		summary.Quota = 1
	}
	if summary.Quota < 0 {
		summary.BillingError = errors.New("computed text billing quota is negative")
		summary.Quota = 0
	}

	return summary
}

func usageSemanticFromUsage(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) string {
	if usage != nil && usage.UsageSemantic != "" {
		return usage.UsageSemantic
	}
	if relayInfo != nil && relayInfo.GetFinalRequestRelayFormat() == types.RelayFormatClaude {
		return "anthropic"
	}
	return "openai"
}

func hasCohereSearchUnitBilling(relayInfo *relaycommon.RelayInfo) bool {
	return relayInfo != nil && relayInfo.RerankerInfo != nil && relayInfo.CohereSearchUnits != nil
}

func hasFixedPriceTextBilling(relayInfo *relaycommon.RelayInfo) bool {
	return relayInfo != nil && relayInfo.PriceData.UsePrice && relayInfo.PriceData.ModelPrice > 0
}

func hasBillableTextUsage(summary textQuotaSummary, relayInfo *relaycommon.RelayInfo) bool {
	return summary.TotalTokens > 0 || (summary.UsePrice && summary.ModelPrice > 0) || hasCohereSearchUnitBilling(relayInfo) ||
		summary.ToolCallSurchargeQuota.GreaterThan(decimal.Zero) || summary.ProviderRequestFeeQuota.GreaterThan(decimal.Zero) ||
		summary.ProviderReportedCost != nil
}

func hasPositiveBillingUsage(usage *dto.Usage) bool {
	if usage == nil {
		return false
	}
	return usage.PromptTokens > 0 || usage.CompletionTokens > 0
}

func hasVerifiedZeroBillingUsage(usage *dto.Usage) bool {
	return usage != nil && usage.BillingUsage != nil && usage.BillingUsage.VerifiedZero
}

func validateTextBillingUsagePresence(relayInfo *relaycommon.RelayInfo, usage *dto.Usage, hasReportedCost bool) error {
	if hasPositiveBillingUsage(usage) || hasVerifiedZeroBillingUsage(usage) || hasReportedCost ||
		hasCohereSearchUnitBilling(relayInfo) || hasFixedPriceTextBilling(relayInfo) {
		return nil
	}
	if usage == nil {
		return fmt.Errorf("upstream billing usage is missing")
	}
	return fmt.Errorf("upstream billing usage contains no positive counters")
}

// invalidUsageSettlementQuota preserves the final outbound request's estimate.
// Malformed upstream usage is not trustworthy enough to calculate an actual
// charge, but an earlier retry's larger reservation must not leak into the
// final attempt's settlement.
func invalidUsageSettlementQuota(relayInfo *relaycommon.RelayInfo) int {
	if relayInfo == nil {
		return 0
	}
	quota := relayInfo.PriceData.QuotaToPreConsume
	if quota < 0 {
		quota = 0
	}
	if snapshot := relayInfo.TieredBillingSnapshot; snapshot != nil && snapshot.EstimatedQuotaAfterGroup > quota {
		quota = snapshot.EstimatedQuotaAfterGroup
	}
	// Legacy/non-text paths may not keep a final request estimate. Fall back to
	// the funded reservation only in that case, excluding the unverified server
	// tool allowance which is reconciled separately from response counters.
	if quota == 0 && !relayInfo.FinalRequestEstimateReady {
		toolReservationQuota := relayInfo.MaxServerToolReservationQuota
		if toolReservationQuota < 0 {
			toolReservationQuota = 0
		}
		reservedQuota := relayInfo.FinalPreConsumedQuota
		if relayInfo.Billing != nil && relayInfo.Billing.GetPreConsumedQuota() > reservedQuota {
			reservedQuota = relayInfo.Billing.GetPreConsumedQuota()
		}
		if reservedQuota > toolReservationQuota {
			quota = reservedQuota - toolReservationQuota
		}
	}
	boundedQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(int64(quota)))
	noteQuotaClamp(relayInfo, clamp)
	return boundedQuota
}

func invalidUsageSettlementWithVerifiedCharges(relayInfo *relaycommon.RelayInfo, summary textQuotaSummary, includeToolUsage bool) int {
	if relayInfo == nil {
		return 0
	}
	baseQuota := invalidUsageSettlementQuota(relayInfo)
	requestFeeQuota, clamp := common.QuotaFromDecimalChecked(summary.ProviderRequestFeeQuota)
	noteQuotaClamp(relayInfo, clamp)
	if summary.ProviderRequestFeeQuota.GreaterThan(decimal.Zero) && requestFeeQuota == 0 {
		requestFeeQuota = 1
	}
	if requestFeeQuota > baseQuota {
		baseQuota = requestFeeQuota
	}
	if !includeToolUsage || !summary.ToolCallSurchargeQuota.GreaterThan(decimal.Zero) {
		return baseQuota
	}

	// The reservation already covers estimated model/token usage and fixed
	// request fees. Response-derived server-tool calls are additional actual
	// usage, so add them instead of taking max(base, tools). Token-only provider
	// multipliers are divided out exactly as in normal settlement.
	toolQuota := normalizeToolCallSurchargeForSharedRatios(summary.ToolCallSurchargeQuota, relayInfo.PriceData)
	toolQuota = relayInfo.PriceData.ApplyOtherRatiosToDecimal(toolQuota)
	quota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(int64(baseQuota)).Add(toolQuota))
	noteQuotaClamp(relayInfo, clamp)
	return quota
}

func PostTextConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent []string) {
	originUsage := usage
	billingUsage, usageValidationErr := effectiveBillingUsage(usage)
	if billingUsage != nil && billingUsage.BillingUsage != nil &&
		billingUsage.BillingUsage.ClaudeUsage != nil &&
		billingUsage.BillingUsage.ClaudeUsage.ServerToolUse != nil {
		providerCount := billingUsage.BillingUsage.ClaudeUsage.ServerToolUse.WebSearchRequests
		if providerCount > ctx.GetInt("claude_web_search_requests") {
			ctx.Set("claude_web_search_requests", providerCount)
		}
	}
	toolUsageErr := validateTextToolCallCounters(ctx, relayInfo)
	invalidUsageErr := usageValidationErr
	if invalidUsageErr == nil && toolUsageErr != nil {
		invalidUsageErr = toolUsageErr
	}
	var reportedCost providerReportedCost
	hasReportedCost := false
	if invalidUsageErr == nil {
		reportedCost, hasReportedCost = providerReportedUsageCost(relayInfo, originUsage)
		invalidUsageErr = validateTextBillingUsagePresence(relayInfo, billingUsage, hasReportedCost)
	}
	invalidUsage := invalidUsageErr != nil
	if usage == nil {
		extraContent = append(extraContent, "上游无计费信息")
	}
	if invalidUsage {
		extraContent = append(extraContent, "上游返回无效计费信息，按预扣额度结算")
		logger.LogError(ctx, "invalid upstream billing usage: "+invalidUsageErr.Error())
	}
	if originUsage != nil && !invalidUsage {
		ObserveChannelAffinityUsageCacheByRelayFormat(ctx, billingUsage, relayInfo.GetFinalRequestRelayFormat())
	}

	adminRejectReason := common.GetContextKeyString(ctx, constant.ContextKeyAdminRejectReason)
	includeToolUsage := toolUsageErr == nil
	summary := calculateTextQuotaSummaryWithToolUsage(ctx, relayInfo, billingUsage, includeToolUsage)
	if summary.BillingError != nil {
		if !invalidUsage {
			extraContent = append(extraContent, "上游返回无效计费信息，按预扣额度结算")
			logger.LogError(ctx, "invalid billing configuration: "+summary.BillingError.Error())
		}
		invalidUsageErr = summary.BillingError
		invalidUsage = true
		includeToolUsage = false
	}

	var tieredResult *billingexpr.TieredResult
	tieredBillingApplied := false
	if originUsage != nil && !invalidUsage {
		var tieredUsedVars map[string]bool
		if snap := relayInfo.TieredBillingSnapshot; snap != nil {
			tieredUsedVars = billingexpr.UsedVars(snap.ExprString)
		}
		tieredOk, tieredQuota, tieredRes := TryTieredSettle(relayInfo, BuildTieredTokenParams(billingUsage, summary.IsClaudeUsageSemantic, tieredUsedVars))
		if tieredOk {
			tieredBillingApplied = true
			tieredResult = tieredRes
			summary.Quota = composeTieredTextQuota(relayInfo, summary, tieredQuota, tieredRes)
		}
	}
	if !invalidUsage && hasReportedCost {
		summary.Quota = reportedCost.Quota
		summary.ProviderReportedCost = &reportedCost
	}
	if invalidUsage {
		summary.Quota = invalidUsageSettlementWithVerifiedCharges(relayInfo, summary, includeToolUsage)
		summary.ProviderReportedCost = nil
	}

	if summary.WebSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Web Search 调用 %d 次，调用花费 %s", summary.WebSearchCallCount, decimal.NewFromFloat(summary.WebSearchPrice).Mul(decimal.NewFromInt(int64(summary.WebSearchCallCount))).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).String()))
	}
	if summary.ClaudeWebSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Claude Web Search 调用 %d 次，调用花费 %s", summary.ClaudeWebSearchCallCount, decimal.NewFromFloat(summary.ClaudeWebSearchPrice).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).Mul(decimal.NewFromInt(int64(summary.ClaudeWebSearchCallCount))).String()))
	}
	if summary.FileSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("File Search 调用 %d 次，调用花费 %s", summary.FileSearchCallCount, decimal.NewFromFloat(summary.FileSearchPrice).Mul(decimal.NewFromInt(int64(summary.FileSearchCallCount))).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).String()))
	}
	if summary.GeminiSearchCallCount > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Gemini %s 调用 %d 次，调用花费 %s", summary.GeminiSearchTool, summary.GeminiSearchCallCount, decimal.NewFromFloat(summary.GeminiSearchPrice).Mul(decimal.NewFromInt(int64(summary.GeminiSearchCallCount))).Div(decimal.NewFromInt(1000)).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).String()))
	}
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Audio Input 花费 %s", summary.AudioInputQuota.String()))
	}
	if summary.ImageGenerationCallPrice > 0 {
		extraContent = append(extraContent, fmt.Sprintf("Image Generation Call 调用 %d 次，调用花费 %s", summary.ImageGenerationCallCount, decimal.NewFromFloat(summary.ImageGenerationCostUSD).Mul(decimal.NewFromFloat(summary.GroupRatio)).Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).String()))
	}

	recordUsageAggregates := true
	if !invalidUsage && !hasBillableTextUsage(summary, relayInfo) && !hasVerifiedZeroBillingUsage(billingUsage) {
		recordUsageAggregates = false
		extraContent = append(extraContent, "上游没有返回计费信息，无法扣费（可能是上游超时）")
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, summary.ModelName, relayInfo.FinalPreConsumedQuota))
	}

	logModel := summary.ModelName
	if strings.HasPrefix(logModel, "gpt-4-gizmo") {
		logModel = "gpt-4-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}
	if strings.HasPrefix(logModel, "gpt-4o-gizmo") {
		logModel = "gpt-4o-gizmo-*"
		extraContent = append(extraContent, fmt.Sprintf("模型 %s", summary.ModelName))
	}

	logContent := strings.Join(extraContent, ", ")
	var other map[string]interface{}
	if summary.IsClaudeUsageSemantic {
		other = GenerateClaudeOtherInfo(ctx, relayInfo,
			summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio,
			summary.CacheTokens, summary.CacheRatio,
			summary.CacheCreationTokens, summary.CacheCreationRatio,
			summary.CacheCreationTokens5m, summary.CacheCreationRatio5m,
			summary.CacheCreationTokens1h, summary.CacheCreationRatio1h,
			summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
		other["usage_semantic"] = "anthropic"
		if billingUsage != nil && billingUsage.ClaudeSpeed != "" {
			other["speed"] = billingUsage.ClaudeSpeed
		}
	} else {
		other = GenerateTextOtherInfo(ctx, relayInfo, summary.ModelRatio, summary.GroupRatio, summary.CompletionRatio, summary.CacheTokens, summary.CacheRatio, summary.ModelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
		if summary.GeminiServiceTier != "" {
			other["service_tier"] = summary.GeminiServiceTier
		} else if summary.ActualServiceTier != "" {
			other["service_tier"] = summary.ActualServiceTier
		}
	}
	appendUsageBillingPathForLog(other, common.GetContextKeyBool(ctx, constant.ContextKeyLocalCountTokens), originUsage)
	if invalidUsage {
		adminInfo, ok := other["admin_info"].(map[string]interface{})
		if !ok || adminInfo == nil {
			adminInfo = make(map[string]interface{})
			other["admin_info"] = adminInfo
		}
		adminInfo["invalid_billing_usage"] = map[string]interface{}{
			"error":         invalidUsageErr.Error(),
			"settled_quota": summary.Quota,
		}
	}
	if adminRejectReason != "" {
		other["reject_reason"] = adminRejectReason
	}
	if summary.ImageTokens != 0 {
		other["image"] = true
		other["image_ratio"] = summary.ImageRatio
		other["image_output"] = summary.ImageTokens
	}
	if summary.WebSearchCallCount > 0 {
		other["web_search"] = true
		other["web_search_call_count"] = summary.WebSearchCallCount
		other["web_search_price"] = summary.WebSearchPrice
	} else if summary.ClaudeWebSearchCallCount > 0 {
		other["web_search"] = true
		other["web_search_call_count"] = summary.ClaudeWebSearchCallCount
		other["web_search_price"] = summary.ClaudeWebSearchPrice
	}
	if summary.FileSearchCallCount > 0 {
		other["file_search"] = true
		other["file_search_call_count"] = summary.FileSearchCallCount
		other["file_search_price"] = summary.FileSearchPrice
	}
	if summary.GeminiSearchCallCount > 0 {
		other[summary.GeminiSearchTool] = true
		other[summary.GeminiSearchTool+"_call_count"] = summary.GeminiSearchCallCount
		other[summary.GeminiSearchTool+"_price"] = summary.GeminiSearchPrice
	}
	if summary.AudioInputPrice > 0 && summary.AudioTokens > 0 {
		other["audio_input_seperate_price"] = true
		other["audio_input_token_count"] = summary.AudioTokens
		other["audio_input_price"] = summary.AudioInputPrice
		if summary.CachedAudioTokens > 0 {
			other["audio_input_cached_token_count"] = summary.CachedAudioTokens
		}
	}
	if summary.ImageGenerationCallPrice > 0 {
		other["image_generation_call"] = true
		other["image_generation_call_count"] = summary.ImageGenerationCallCount
		other["image_generation_call_price"] = summary.ImageGenerationCallPrice
		other["image_generation_cost_usd"] = summary.ImageGenerationCostUSD
		other["image_generation_model"] = summary.ImageGenerationModel
		if summary.ImageGenerationPartials > 0 {
			other["image_generation_partial_count"] = summary.ImageGenerationPartials
		}
	}
	if summary.CacheCreationTokens > 0 {
		other["cache_creation_tokens"] = summary.CacheCreationTokens
		other["cache_creation_ratio"] = summary.CacheCreationRatio
	}
	if summary.CacheCreationTokens5m > 0 {
		other["cache_creation_tokens_5m"] = summary.CacheCreationTokens5m
		other["cache_creation_ratio_5m"] = summary.CacheCreationRatio5m
	}
	if summary.CacheCreationTokens1h > 0 {
		other["cache_creation_tokens_1h"] = summary.CacheCreationTokens1h
		other["cache_creation_ratio_1h"] = summary.CacheCreationRatio1h
	}
	cacheWriteTokens := cacheWriteTokensTotal(summary)
	if cacheWriteTokens > 0 {
		// cache_write_tokens: normalized cache creation total for UI display.
		// If split 5m/1h values are present, this is their sum; otherwise it falls back
		// to cache_creation_tokens.
		other["cache_write_tokens"] = cacheWriteTokens
	}
	if relayInfo.GetFinalRequestRelayFormat() != types.RelayFormatClaude && billingUsage != nil && billingUsage.UsageSource != "" && billingUsage.InputTokens > 0 {
		// input_tokens_total: explicit normalized total input used by the usage log UI.
		// Only write this field when upstream/current conversion has already provided a
		// reliable total input value and tagged the usage source. Do not infer it from
		// prompt/cache fields here, otherwise old upstream payloads may be double-counted.
		other["input_tokens_total"] = billingUsage.InputTokens
	}
	if tieredBillingApplied {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	if summary.ProviderReportedCost != nil {
		attachProviderReportedCost(other, *summary.ProviderReportedCost)
	}

	attachQuotaSaturation(ctx, relayInfo, other)

	ip := ""
	if relayInfo.UserSetting.RecordIpLog {
		ip = ctx.ClientIP()
	}
	aggregateQuota := 0
	if recordUsageAggregates {
		aggregateQuota = summary.Quota
	}
	if err := FinalizeBilling(
		ctx,
		relayInfo,
		summary.Quota,
		aggregateQuota,
		aggregateQuota,
		recordUsageAggregates,
		model.TaskBillingFinalizationLog{
			Content:           logContent,
			ModelName:         logModel,
			PromptTokens:      summary.PromptTokens,
			CompletionTokens:  summary.CompletionTokens,
			TokenName:         summary.TokenName,
			UseTime:           int(summary.UseTimeSeconds),
			IsStream:          relayInfo.IsStream,
			IP:                ip,
			UpstreamRequestID: ctx.GetString(common.UpstreamRequestIdKey),
			Username:          ctx.GetString("username"),
			Other:             other,
		},
	); err != nil {
		logger.LogError(ctx, "error finalizing billing: "+err.Error())
	}
	perfmetrics.RecordRelaySampleAsync(relayInfo, true, int64(summary.CompletionTokens))
}
