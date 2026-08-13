package helper

import (
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/billing_setting"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
)

func modelPriceNotConfiguredError(modelName string, userId int) error {
	if model.IsAdmin(userId) {
		return fmt.Errorf(
			"模型 %s 的价格未配置。请前往「系统设置 → 运营设置」开启自用模式，或在「系统设置 → 分组与模型定价设置」中为该模型配置价格；"+
				"Model %s price not configured. Go to System Settings → Operation Settings to enable self-use mode, or configure the model price in System Settings → Group & Model Pricing.",
			modelName, modelName,
		)
	}
	return fmt.Errorf(
		"模型 %s 的价格尚未由管理员配置，暂时无法使用，请联系站点管理员开启该模型；"+
			"Model %s has not been priced by the administrator yet. Please contact the site administrator to enable this model.",
		modelName, modelName,
	)
}

// https://docs.claude.com/en/docs/build-with-claude/prompt-caching#1-hour-cache-duration
const claudeCacheCreation1hMultiplier = 6 / 3.75

// defaultTieredPreConsumeMaxTokens is the fallback completion-token estimate
// used for tiered expression pre-consume when the client omits max_tokens, so
// the pre-consumed quota still reflects a plausible output cost in paid groups.
const defaultTieredPreConsumeMaxTokens = 8192

func applyGroupRatio(groupRatioInfo *hosttypes.GroupRatioInfo, group string) {
	groupRatioInfo.GroupRatio = ratio_setting.GetGroupRatio(group)
}

func applySpecialGroupRatio(groupRatioInfo *hosttypes.GroupRatioInfo, userGroup, group string) bool {
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(userGroup, group)
	if !ok {
		return false
	}
	groupRatioInfo.GroupSpecialRatio = userGroupRatio
	groupRatioInfo.GroupRatio = userGroupRatio
	groupRatioInfo.HasSpecialRatio = true
	return true
}

func applyNormalGroupRatio(groupRatioInfo *hosttypes.GroupRatioInfo, group string) {
	groupRatioInfo.GroupRatio = ratio_setting.GetGroupRatio(group)
	groupRatioInfo.GroupSpecialRatio = -1
	groupRatioInfo.HasSpecialRatio = false
}

func applyStandardGroupRatio(groupRatioInfo *hosttypes.GroupRatioInfo, userGroup, usingGroup string) {
	if !applySpecialGroupRatio(groupRatioInfo, userGroup, usingGroup) {
		applyGroupRatio(groupRatioInfo, usingGroup)
	}
}

func applyFallbackOriginGroupRatio(groupRatioInfo *hosttypes.GroupRatioInfo, relayInfo *relaycommon.RelayInfo, rule setting.GroupFallbackRule, sourceGroup string) {
	relayInfo.UsingGroup = sourceGroup
	if rule.ShouldUseOriginPricingSpecialRatio() {
		applyStandardGroupRatio(groupRatioInfo, relayInfo.UserGroup, sourceGroup)
		return
	}
	applyNormalGroupRatio(groupRatioInfo, sourceGroup)
}

func applyFallbackTargetGroupRatio(groupRatioInfo *hosttypes.GroupRatioInfo, relayInfo *relaycommon.RelayInfo, rule setting.GroupFallbackRule, sourceGroup, targetGroup string) {
	relayInfo.UsingGroup = targetGroup
	switch rule.EffectiveTargetPricingRatioMode() {
	case setting.GroupFallbackTargetRatioModeOriginSpecial:
		if applySpecialGroupRatio(groupRatioInfo, relayInfo.UserGroup, sourceGroup) {
			return
		}
	case setting.GroupFallbackTargetRatioModeTargetSpecial:
		if applySpecialGroupRatio(groupRatioInfo, relayInfo.UserGroup, targetGroup) {
			return
		}
	case setting.GroupFallbackTargetRatioModePreferOriginSpecial:
		if applySpecialGroupRatio(groupRatioInfo, relayInfo.UserGroup, sourceGroup) ||
			applySpecialGroupRatio(groupRatioInfo, relayInfo.UserGroup, targetGroup) {
			return
		}
	case setting.GroupFallbackTargetRatioModePreferTargetSpecial:
		if applySpecialGroupRatio(groupRatioInfo, relayInfo.UserGroup, targetGroup) ||
			applySpecialGroupRatio(groupRatioInfo, relayInfo.UserGroup, sourceGroup) {
			return
		}
	case setting.GroupFallbackTargetRatioModeNormalOnly:
		// Intentionally ignore all special ratios during target fallback pricing.
	}
	applyNormalGroupRatio(groupRatioInfo, targetGroup)
}

// HandleGroupRatio checks for "auto_group" in the context and updates the group ratio and relayInfo.UsingGroup if present
func HandleGroupRatio(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) hosttypes.GroupRatioInfo {
	groupRatioInfo := hosttypes.GroupRatioInfo{
		GroupRatio:        1.0, // default ratio
		GroupSpecialRatio: -1,
	}

	// check auto group
	autoGroup, exists := common.GetContextKeyType[string](ctx, constant.ContextKeyAutoGroup)
	if exists {
		logger.LogDebug(ctx, "final group: %s", autoGroup)
		relayInfo.UsingGroup = autoGroup
	}

	if !exists {
		if fbGroupName := common.GetContextKeyString(ctx, constant.ContextKeyFallbackGroup); fbGroupName != "" {
			sourceGroup := common.GetContextKeyString(ctx, constant.ContextKeyFallbackSourceGroup)
			if sourceGroup == "" {
				sourceGroup = relayInfo.UsingGroup
			}
			if rule, ok := setting.GetGroupFallback(sourceGroup); ok {
				switch rule.PricingMode {
				case setting.GroupFallbackPricingModeTarget:
					applyFallbackTargetGroupRatio(&groupRatioInfo, relayInfo, rule, sourceGroup, fbGroupName)
				default:
					applyFallbackOriginGroupRatio(&groupRatioInfo, relayInfo, rule, sourceGroup)
				}
				return groupRatioInfo
			}
		}
	}

	applyStandardGroupRatio(&groupRatioInfo, relayInfo.UserGroup, relayInfo.UsingGroup)

	return groupRatioInfo
}

func ModelPriceHelper(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta) (hosttypes.PriceData, error) {
	if meta == nil {
		return hosttypes.PriceData{}, errors.New("token count metadata is required")
	}
	if meta.ImageInputTokens < 0 || meta.ImageOutputTokens < 0 ||
		(meta.MaxTokens >= 0 && meta.ImageOutputTokens > meta.MaxTokens) {
		return hosttypes.PriceData{}, errors.New("image token reservation metadata is invalid")
	}
	modelPrice, usePrice := ratio_setting.GetModelPrice(info.OriginModelName, false)

	groupRatioInfo := HandleGroupRatio(c, info)

	// Check if this model uses tiered_expr billing
	if billing_setting.GetBillingMode(info.OriginModelName) == billing_setting.BillingModeTieredExpr {
		return modelPriceHelperTiered(c, info, promptTokens, meta, groupRatioInfo)
	}

	var preConsumedQuota int
	var modelRatio float64
	var completionRatio float64
	var cacheRatio float64
	var imageRatio float64
	var cacheCreationRatio float64
	var cacheCreationRatio5m float64
	var cacheCreationRatio1h float64
	var audioRatio float64
	var audioCompletionRatio float64
	preConsumeMaxTokens := meta.MaxTokens
	var freeModel bool
	regionalProcessingRatio := 1.0
	anthropicInferenceGeoRatio := 1.0
	hasParamOverride := info.ChannelMeta != nil && len(info.ChannelMeta.ParamOverride) > 0
	if !usePrice {
		var success bool
		var matchName string
		modelRatio, success, matchName = ratio_setting.GetModelRatio(info.OriginModelName)
		if !success {
			acceptUnsetRatio := false
			if info.UserSetting.AcceptUnsetRatioModel {
				acceptUnsetRatio = true
			}
			if !acceptUnsetRatio {
				return hosttypes.PriceData{}, modelPriceNotConfiguredError(matchName, info.UserId)
			}
		}
		completionRatio = ratio_setting.GetCompletionRatio(info.OriginModelName)
		cacheRatio, _ = ratio_setting.GetCacheRatio(info.OriginModelName)
		cacheCreationRatio, _ = ratio_setting.GetCreateCacheRatio(info.OriginModelName)
		cacheCreationRatio5m = cacheCreationRatio
		// 固定1h和5min缓存写入价格的比例
		cacheCreationRatio1h = cacheCreationRatio * claudeCacheCreation1hMultiplier
		imageRatio, _ = ratio_setting.GetImageRatio(info.OriginModelName)
		audioRatio = ratio_setting.GetAudioRatio(info.OriginModelName)
		audioCompletionRatio = ratio_setting.GetAudioCompletionRatio(info.OriginModelName)
		preConsumeModelRatio := modelRatio
		preConsumeCompletionRatio := completionRatio
		inputMultiplier := 1.0
		outputMultiplier := 1.0
		channelType := common.GetContextKeyInt(c, constant.ContextKeyChannelType)
		defaultRatio, usesBuiltInOpenAIPricing := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
		usesBuiltInOpenAIPricing = usesBuiltInOpenAIPricing && modelRatio == defaultRatio &&
			channelType == constant.ChannelTypeOpenAI

		if usesBuiltInOpenAIPricing {
			channelOtherSettings, ok := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
			if ok && (channelOtherSettings.AllowServiceTier || hasParamOverride) {
				requestedServiceTier := ""
				switch request := info.Request.(type) {
				case *dto.GeneralOpenAIRequest:
					if len(request.ServiceTier) > 0 {
						_ = common.Unmarshal(request.ServiceTier, &requestedServiceTier)
					}
				case *dto.OpenAIResponsesRequest:
					requestedServiceTier = request.ServiceTier
				case *dto.OpenAIResponsesCompactionRequest:
					requestedServiceTier = request.ServiceTier
				}
				if strings.EqualFold(requestedServiceTier, "priority") {
					if priorityRatios, ok := ratio_setting.GetOpenAIPriorityPriceRatios(info.OriginModelName); ok {
						preConsumeModelRatio = priorityRatios.ModelRatio
						preConsumeCompletionRatio = priorityRatios.CompletionRatio
					}
				}
			}

			if preConsumeModelRatio == modelRatio && promptTokens > ratio_setting.OpenAILongContextThreshold &&
				ratio_setting.IsOpenAILongContextModel(info.OriginModelName) {
				inputMultiplier = 2
				outputMultiplier = 1.5
			}

			if ratio_setting.IsOpenAIRegionalProcessingUpliftModel(info.OriginModelName) {
				if parsedBaseURL, err := url.Parse(common.GetContextKeyString(c, constant.ContextKeyChannelBaseUrl)); err == nil {
					host := strings.ToLower(parsedBaseURL.Hostname())
					if host == "us.api.openai.com" || host == "eu.api.openai.com" {
						regionalProcessingRatio = 1.1
					}
				}
			}
		}

		defaultRatio, usesBuiltInGeminiPricing := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
		usesBuiltInGeminiPricing = usesBuiltInGeminiPricing &&
			(channelType == constant.ChannelTypeGemini || channelType == constant.ChannelTypeVertexAi) &&
			modelRatio == defaultRatio && completionRatio == ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) &&
			cacheRatio == ratio_setting.GetDefaultCacheRatio(info.OriginModelName) &&
			cacheCreationRatio == ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName)
		if usesBuiltInGeminiPricing {
			if channelType == constant.ChannelTypeGemini {
				channelOtherSettings, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
				channelSettings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
				passesRawBody := model_setting.GetGlobalSettings().PassThroughRequestEnabled ||
					channelSettings.PassThroughBodyEnabled ||
					(info.ChannelMeta != nil && info.ChannelSetting.PassThroughBodyEnabled)
				if channelOtherSettings.AllowServiceTier || passesRawBody || hasParamOverride {
					requestedServiceTier := ""
					switch request := info.Request.(type) {
					case *dto.GeminiChatRequest:
						if request.ServiceTier != nil {
							requestedServiceTier = *request.ServiceTier
						}
					case *dto.GeneralOpenAIRequest:
						if len(request.ServiceTier) > 0 {
							_ = common.Unmarshal(request.ServiceTier, &requestedServiceTier)
						}
					case *dto.OpenAIResponsesRequest:
						requestedServiceTier = request.ServiceTier
					}
					if tierPricing, found := ratio_setting.GetGeminiServiceTierPriceRatios(info.OriginModelName, requestedServiceTier); found {
						preConsumeModelRatio = modelRatio * tierPricing.ModelMultiplier
					}
				}
			}

			if promptTokens > ratio_setting.GeminiLongContextThreshold &&
				ratio_setting.IsGeminiLongContextModel(info.OriginModelName) {
				inputMultiplier = 2
				outputMultiplier = 1.5
			}
			if imageOutputRatio, ok := ratio_setting.GetGeminiImageOutputRatio(info.OriginModelName); ok {
				preConsumeCompletionRatio = imageOutputRatio
				imageOutputs := 1
				switch request := info.Request.(type) {
				case *dto.GeminiChatRequest:
					if request.GenerationConfig.CandidateCount != nil {
						imageOutputs = *request.GenerationConfig.CandidateCount
					}
				case *dto.GeneralOpenAIRequest:
					if request.N != nil && *request.N <= dto.MaxChatCompletionsN {
						imageOutputs = *request.N
					}
				}
				if imageOutputs < 1 {
					imageOutputs = 1
				} else if imageOutputs > dto.MaxChatCompletionsN {
					imageOutputs = dto.MaxChatCompletionsN
				}
				if maxImageTokens, found := ratio_setting.GetGeminiMaxImageOutputTokens(info.OriginModelName); found {
					maxImageTokens *= imageOutputs
					if preConsumeMaxTokens < maxImageTokens {
						preConsumeMaxTokens = maxImageTokens
					}
				}
			}
		}

		defaultRatio, usesBuiltInClaudePricing := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
		usesBuiltInClaudePricing = usesBuiltInClaudePricing && channelType == constant.ChannelTypeAnthropic &&
			modelRatio == defaultRatio && completionRatio == ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) &&
			cacheRatio == ratio_setting.GetDefaultCacheRatio(info.OriginModelName) &&
			cacheCreationRatio == ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName)
		if usesBuiltInClaudePricing {
			channelOtherSettings, ok := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
			request, requestOk := info.Request.(*dto.ClaudeRequest)
			if ok && requestOk {
				channelSettings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
				passesRawBody := model_setting.GetGlobalSettings().PassThroughRequestEnabled ||
					channelSettings.PassThroughBodyEnabled ||
					(info.ChannelMeta != nil && info.ChannelSetting.PassThroughBodyEnabled)
				if (channelOtherSettings.AllowSpeed || passesRawBody || hasParamOverride) && len(request.Speed) > 0 {
					requestedSpeed := ""
					_ = common.Unmarshal(request.Speed, &requestedSpeed)
					if strings.EqualFold(requestedSpeed, "fast") {
						if fastRatios, found := ratio_setting.GetClaudeFastPriceRatios(info.OriginModelName); found {
							preConsumeModelRatio = fastRatios.ModelRatio
							preConsumeCompletionRatio = fastRatios.CompletionRatio
						}
					}
				}
				if (channelOtherSettings.AllowInferenceGeo || passesRawBody || hasParamOverride) &&
					request.InferenceGeo != nil && strings.EqualFold(*request.InferenceGeo, "us") &&
					ratio_setting.IsClaudeInferenceGeoPricingModel(info.OriginModelName) {
					anthropicInferenceGeoRatio = 1.1
				}
			}
		}

		preConsumeModelRatio *= xaiPreConsumeMultiplier(c, info, promptTokens, modelRatio, completionRatio, cacheRatio, cacheCreationRatio)

		preConsumedQuotaSetting := common.GetLegacyOptionInt("PreConsumedQuota", &common.PreConsumedQuota)
		preConsumedPromptTokens := common.Max(promptTokens, preConsumedQuotaSetting)
		preConsumeUnits := float64(preConsumedPromptTokens)*inputMultiplier +
			float64(meta.ImageInputTokens)*imageRatio*inputMultiplier +
			float64(preConsumeMaxTokens)*preConsumeCompletionRatio*outputMultiplier
		preConsumeRatio := preConsumeModelRatio * groupRatioInfo.GroupRatio * regionalProcessingRatio * anthropicInferenceGeoRatio
		quota, err := common.QuotaFromFloatStrict(preConsumeUnits * preConsumeRatio)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		preConsumedQuota = quota
	} else {
		if meta.ImagePriceRatio != 0 {
			modelPrice = modelPrice * meta.ImagePriceRatio
		}
	}

	// check if free model pre-consume is disabled
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		// if model price or ratio is 0, do not pre-consume quota
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		} else if usePrice {
			if modelPrice == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		} else {
			if modelRatio == 0 {
				preConsumedQuota = 0
				freeModel = true
			}
		}
	}

	priceData := hosttypes.PriceData{
		FreeModel:            freeModel,
		ModelPrice:           modelPrice,
		ModelRatio:           modelRatio,
		CompletionRatio:      completionRatio,
		GroupRatioInfo:       groupRatioInfo,
		UsePrice:             usePrice,
		CacheRatio:           cacheRatio,
		ImageRatio:           imageRatio,
		AudioRatio:           audioRatio,
		AudioCompletionRatio: audioCompletionRatio,
		CacheCreationRatio:   cacheCreationRatio,
		CacheCreation5mRatio: cacheCreationRatio5m,
		CacheCreation1hRatio: cacheCreationRatio1h,
		QuotaToPreConsume:    preConsumedQuota,
	}
	if regionalProcessingRatio != 1 {
		priceData.AddOtherRatio("openai_regional_processing", regionalProcessingRatio)
	}
	if anthropicInferenceGeoRatio != 1 {
		priceData.AddOtherRatio("anthropic_inference_geo", anthropicInferenceGeoRatio)
	}
	if usePrice {
		for name, ratio := range meta.BillingRatios {
			priceData.AddOtherRatio(name, ratio)
		}
		quotaToPreConsume := priceData.ApplyOtherRatiosToFloat(modelPrice * common.CurrentQuotaPerUnit() * groupRatioInfo.GroupRatio)
		quota, err := common.QuotaFromFloatStrict(quotaToPreConsume)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		priceData.QuotaToPreConsume = quota
	}
	if err := applyCohereRerankSearchUnitPreConsume(c, info, &priceData); err != nil {
		return hosttypes.PriceData{}, err
	}
	if err := applyPerplexityRequestFeePreConsume(c, info, &priceData); err != nil {
		return hosttypes.PriceData{}, err
	}

	if common.DebugEnabled {
		logger.LogDebug(c, "model_price_helper result: %s", priceData.ToSetting())
	}
	info.PriceData = priceData
	return priceData, nil
}

// RefreshModelPriceForFinalRequest recomputes pricing from the exact outbound
// request after disabled-field filtering and channel parameter overrides. The
// original request object must stay intact because a later channel retry starts
// conversion from it again, but tiered billing must retain the final request
// snapshot used for the successful attempt.
func RefreshModelPriceForFinalRequest(c *gin.Context, info *relaycommon.RelayInfo, request dto.Request, promptTokens int, meta *types.TokenCountMeta) (hosttypes.PriceData, error) {
	if info == nil {
		return hosttypes.PriceData{}, fmt.Errorf("relay info is required")
	}
	if request == nil {
		return hosttypes.PriceData{}, fmt.Errorf("final request is required")
	}
	if meta == nil {
		return hosttypes.PriceData{}, fmt.Errorf("final token metadata is required")
	}

	requestInput, err := BuildBillingExprRequestInputFromRequest(request, info.RequestHeaders)
	if err != nil {
		return hosttypes.PriceData{}, fmt.Errorf("build final billing request input: %w", err)
	}

	originalRequest := info.Request
	originalInput := info.BillingRequestInput
	originalSnapshot := info.TieredBillingSnapshot
	originalPriceData := info.PriceData
	info.Request = request
	info.BillingRequestInput = &requestInput

	priceData, err := ModelPriceHelper(c, info, promptTokens, meta)
	info.Request = originalRequest
	if err != nil {
		info.BillingRequestInput = originalInput
		info.TieredBillingSnapshot = originalSnapshot
		info.PriceData = originalPriceData
		return hosttypes.PriceData{}, err
	}
	return priceData, nil
}

// ModelPriceHelperPerCall 按次/按量计费的 PriceHelper (MJ、Task)
func ModelPriceHelperPerCall(c *gin.Context, info *relaycommon.RelayInfo) (hosttypes.PriceData, error) {
	groupRatioInfo := HandleGroupRatio(c, info)

	modelPrice, success := ratio_setting.GetModelPrice(info.OriginModelName, true)
	usePrice := success
	var modelRatio float64

	if !success {
		defaultPrice, ok := ratio_setting.GetDefaultModelPriceMap()[info.OriginModelName]
		if ok {
			modelPrice = defaultPrice
			usePrice = true
		} else {
			var ratioSuccess bool
			var matchName string
			modelRatio, ratioSuccess, matchName = ratio_setting.GetModelRatio(info.OriginModelName)
			acceptUnsetRatio := false
			if info.UserSetting.AcceptUnsetRatioModel {
				acceptUnsetRatio = true
			}
			if !ratioSuccess && !acceptUnsetRatio {
				return hosttypes.PriceData{}, modelPriceNotConfiguredError(matchName, info.UserId)
			}
		}
	}

	var quota int
	freeModel := false

	if usePrice {
		var err error
		quota, err = common.QuotaFromFloatStrict(modelPrice * common.CurrentQuotaPerUnit() * groupRatioInfo.GroupRatio)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelPrice == 0 {
				quota = 0
				freeModel = true
			}
		}
	} else {
		// 按量计费：以模型倍率的一半作为预扣额度
		var err error
		quota, err = common.QuotaFromFloatStrict(modelRatio / 2 * common.CurrentQuotaPerUnit() * groupRatioInfo.GroupRatio)
		if err != nil {
			return hosttypes.PriceData{}, err
		}
		modelPrice = -1
		if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
			if groupRatioInfo.GroupRatio == 0 || modelRatio == 0 {
				quota = 0
				freeModel = true
			}
		}
	}

	priceData := hosttypes.PriceData{
		FreeModel:      freeModel,
		ModelPrice:     modelPrice,
		ModelRatio:     modelRatio,
		UsePrice:       usePrice,
		Quota:          quota,
		GroupRatioInfo: groupRatioInfo,
	}
	return priceData, nil
}

func HasModelBillingConfig(modelName string) bool {
	if _, ok := ratio_setting.GetModelPrice(modelName, false); ok {
		return true
	}
	if _, ok, _ := ratio_setting.GetModelRatio(modelName); ok {
		return true
	}
	if billing_setting.GetBillingMode(modelName) != billing_setting.BillingModeTieredExpr {
		return false
	}
	expr, ok := billing_setting.GetBillingExpr(modelName)
	return ok && strings.TrimSpace(expr) != ""
}

func modelPriceHelperTiered(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, meta *types.TokenCountMeta, groupRatioInfo hosttypes.GroupRatioInfo) (hosttypes.PriceData, error) {
	exprStr, ok := billing_setting.GetBillingExpr(info.OriginModelName)
	if !ok {
		return hosttypes.PriceData{}, fmt.Errorf("model %s is configured as tiered_expr but has no billing expression", info.OriginModelName)
	}

	estimatedCompletionTokens := meta.MaxTokens
	if estimatedCompletionTokens == 0 && groupRatioInfo.GroupRatio != 0 {
		estimatedCompletionTokens = defaultTieredPreConsumeMaxTokens
	}
	if meta.ImageInputTokens < 0 || meta.ImageOutputTokens < 0 ||
		meta.ImageOutputTokens > estimatedCompletionTokens {
		return hosttypes.PriceData{}, errors.New("image token reservation metadata is invalid")
	}

	requestInput, err := ResolveIncomingBillingExprRequestInput(c, info)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	usedVars := billingexpr.UsedVars(exprStr)
	tokenParams := billingexpr.TokenParams{
		P:   float64(promptTokens),
		C:   float64(estimatedCompletionTokens),
		Len: float64(promptTokens + meta.ImageInputTokens),
	}
	if usedVars["img"] {
		tokenParams.Img = float64(meta.ImageInputTokens)
	} else {
		tokenParams.P += float64(meta.ImageInputTokens)
	}
	if usedVars["img_o"] {
		tokenParams.C -= float64(meta.ImageOutputTokens)
		tokenParams.ImgO = float64(meta.ImageOutputTokens)
	}

	rawCost, trace, err := billingexpr.RunExprWithRequest(exprStr, tokenParams, requestInput)
	if err != nil {
		return hosttypes.PriceData{}, fmt.Errorf("model %s tiered expr run failed: %w", info.OriginModelName, err)
	}

	// Expression coefficients are $/1M tokens prices; convert to quota the same way per-call billing does.
	quotaBeforeGroup := rawCost / 1_000_000 * common.CurrentQuotaPerUnit()
	preConsumedQuota, err := billingexpr.QuotaRoundStrict(quotaBeforeGroup * groupRatioInfo.GroupRatio)
	if err != nil {
		return hosttypes.PriceData{}, err
	}

	freeModel := false
	if !operation_setting.GetQuotaSetting().EnableFreeModelPreConsume {
		if groupRatioInfo.GroupRatio == 0 {
			preConsumedQuota = 0
			freeModel = true
		}
	}

	exprHash := billingexpr.ExprHashString(exprStr)
	snapshot := &billingexpr.BillingSnapshot{
		BillingMode:               billing_setting.BillingModeTieredExpr,
		ModelName:                 info.OriginModelName,
		ExprString:                exprStr,
		ExprHash:                  exprHash,
		GroupRatio:                groupRatioInfo.GroupRatio,
		EstimatedPromptTokens:     promptTokens + meta.ImageInputTokens,
		EstimatedCompletionTokens: estimatedCompletionTokens,
		EstimatedQuotaBeforeGroup: quotaBeforeGroup,
		EstimatedQuotaAfterGroup:  preConsumedQuota,
		EstimatedTier:             trace.MatchedTier,
		QuotaPerUnit:              common.CurrentQuotaPerUnit(),
		ExprVersion:               billingexpr.ExprVersion(exprStr),
	}
	info.TieredBillingSnapshot = snapshot
	info.BillingRequestInput = &requestInput

	priceData := hosttypes.PriceData{
		FreeModel:         freeModel,
		GroupRatioInfo:    groupRatioInfo,
		QuotaToPreConsume: preConsumedQuota,
	}

	logger.LogDebug(c, "model_price_helper_tiered result: model=%s preConsume=%d quotaBeforeGroup=%.2f groupRatio=%.2f tier=%s", info.OriginModelName, preConsumedQuota, quotaBeforeGroup, groupRatioInfo.GroupRatio, trace.MatchedTier)

	info.PriceData = priceData
	return priceData, nil
}
