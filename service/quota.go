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
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/bytedance/gopkg/util/gopool"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

type TokenDetails struct {
	TextTokens  int
	AudioTokens int
}

type QuotaInfo struct {
	InputDetails  TokenDetails
	OutputDetails TokenDetails
	ModelName     string
	UsePrice      bool
	ModelPrice    float64
	ModelRatio    float64
	GroupRatio    float64
}

func validateRealtimeBillingUsage(usage *dto.RealtimeUsage) error {
	if usage == nil {
		return errors.New("realtime billing usage is missing")
	}
	counters := []struct {
		name  string
		value int
	}{
		{"total_tokens", usage.TotalTokens},
		{"input_tokens", usage.InputTokens},
		{"output_tokens", usage.OutputTokens},
		{"input_token_details.text_tokens", usage.InputTokenDetails.TextTokens},
		{"input_token_details.audio_tokens", usage.InputTokenDetails.AudioTokens},
		{"output_token_details.text_tokens", usage.OutputTokenDetails.TextTokens},
		{"output_token_details.audio_tokens", usage.OutputTokenDetails.AudioTokens},
	}
	for _, counter := range counters {
		if err := validateUsageCounter("realtime_usage."+counter.name, counter.value); err != nil {
			return err
		}
	}
	if err := validateUsageCounterSum("realtime_usage.input_tokens+output_tokens", usage.InputTokens, usage.OutputTokens); err != nil {
		return err
	}
	if err := validateUsageBreakdownTotal("realtime total", usage.TotalTokens, usage.InputTokens, usage.OutputTokens); err != nil {
		return err
	}
	if err := validateUsageBreakdownTotal("realtime input", usage.InputTokens,
		usage.InputTokenDetails.TextTokens, usage.InputTokenDetails.AudioTokens); err != nil {
		return err
	}
	return validateUsageBreakdownTotal("realtime output", usage.OutputTokens,
		usage.OutputTokenDetails.TextTokens, usage.OutputTokenDetails.AudioTokens)
}

// ValidateRealtimeBillingUsage validates provider-controlled realtime counters
// before an adapter merges them into the cumulative billable usage.
func ValidateRealtimeBillingUsage(usage *dto.RealtimeUsage) error {
	return validateRealtimeBillingUsage(usage)
}

func hasCustomModelRatio(modelName string, currentRatio float64) bool {
	defaultRatio, exists := ratio_setting.GetDefaultModelRatioMap()[modelName]
	if !exists {
		return true
	}
	return currentRatio != defaultRatio
}

func calculateAudioQuota(info QuotaInfo) (int, *common.QuotaClamp) {
	if info.UsePrice {
		modelPrice := decimal.NewFromFloat(info.ModelPrice)
		quotaPerUnit := decimal.NewFromFloat(common.CurrentQuotaPerUnit())
		groupRatio := decimal.NewFromFloat(info.GroupRatio)

		quota := modelPrice.Mul(quotaPerUnit).Mul(groupRatio)
		return common.QuotaFromDecimalChecked(quota)
	}

	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(info.ModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(info.ModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(info.ModelName))

	groupRatio := decimal.NewFromFloat(info.GroupRatio)
	modelRatio := decimal.NewFromFloat(info.ModelRatio)
	ratio := groupRatio.Mul(modelRatio)

	inputTextTokens := decimal.NewFromInt(int64(info.InputDetails.TextTokens))
	outputTextTokens := decimal.NewFromInt(int64(info.OutputDetails.TextTokens))
	inputAudioTokens := decimal.NewFromInt(int64(info.InputDetails.AudioTokens))
	outputAudioTokens := decimal.NewFromInt(int64(info.OutputDetails.AudioTokens))

	quota := decimal.Zero
	quota = quota.Add(inputTextTokens)
	quota = quota.Add(outputTextTokens.Mul(completionRatio))
	quota = quota.Add(inputAudioTokens.Mul(audioRatio))
	quota = quota.Add(outputAudioTokens.Mul(audioRatio).Mul(audioCompletionRatio))

	quota = quota.Mul(ratio)

	// If ratio is not zero and quota is less than or equal to zero, set quota to 1
	if !ratio.IsZero() && quota.LessThanOrEqual(decimal.Zero) {
		quota = decimal.NewFromInt(1)
	}

	return common.QuotaFromDecimalChecked(quota)
}

// PreWssConsumeQuota extends the request's reservation to cover cumulative
// realtime usage. Charging each response independently here would be charged
// again by the final BillingSession settlement.
func PreWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.RealtimeUsage) error {
	if err := validateRealtimeBillingUsage(usage); err != nil {
		return fmt.Errorf("invalid realtime billing usage: %w", err)
	}
	if relayInfo.UsePrice {
		return nil
	}
	token, err := model.GetTokenByKey(strings.TrimPrefix(relayInfo.TokenKey, "sk-"), false)
	if err != nil {
		return err
	}

	modelName := relayInfo.OriginModelName
	textInputTokens := usage.InputTokenDetails.TextTokens
	textOutTokens := usage.OutputTokenDetails.TextTokens
	audioInputTokens := usage.InputTokenDetails.AudioTokens
	audioOutTokens := usage.OutputTokenDetails.AudioTokens
	groupRatio := ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
	modelRatio, _, _ := ratio_setting.GetModelRatio(modelName)

	autoGroup, exists := common.GetContextKey(ctx, constant.ContextKeyAutoGroup)
	if exists {
		autoGroupName, ok := autoGroup.(string)
		if !ok || autoGroupName == "" {
			return errors.New("invalid automatic billing group in request context")
		}
		groupRatio = ratio_setting.GetGroupRatio(autoGroupName)
		logger.LogDebug(ctx, "final group ratio: %f", groupRatio)
		relayInfo.UsingGroup = autoGroupName
	}

	actualGroupRatio := groupRatio
	userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
	if ok {
		actualGroupRatio = userGroupRatio
	}

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  modelName,
		UsePrice:   relayInfo.UsePrice,
		ModelRatio: modelRatio,
		GroupRatio: actualGroupRatio,
	}

	quota, clamp := calculateAudioQuota(quotaInfo)
	noteQuotaClamp(relayInfo, clamp)
	if clamp != nil {
		return clamp
	}
	reservedQuota := relayInfo.FinalPreConsumedQuota
	if relayInfo.Billing != nil {
		reservedQuota = relayInfo.Billing.GetPreConsumedQuota()
	}
	quotaToReserve := quota - reservedQuota
	if quotaToReserve <= 0 {
		return nil
	}

	if relayInfo.BillingSource != BillingSourceSubscription {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return err
		}
		if userQuota < quotaToReserve {
			return fmt.Errorf("user quota is not enough, user quota: %s, need quota: %s", logger.FormatQuota(userQuota), logger.FormatQuota(quotaToReserve))
		}
	}

	if !token.UnlimitedQuota && token.RemainQuota < quotaToReserve {
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(token.RemainQuota), logger.FormatQuota(quotaToReserve))
	}

	if relayInfo.Billing != nil {
		if err := relayInfo.Billing.Reserve(quota); err != nil {
			return err
		}
	} else {
		purpose := fmt.Sprintf("realtime-reserve:%d", quota)
		if _, _, err := ApplyDurableQuotaAdjustment(relayInfo, purpose, quotaToReserve, 0, false); err != nil {
			return err
		}
		relayInfo.FinalPreConsumedQuota = quota
	}
	logger.LogInfo(ctx, "realtime streaming reserve quota success, quota: "+fmt.Sprintf("%d", quota))
	return nil
}

func PostWssConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, modelName string,
	usage *dto.RealtimeUsage, extraContent string) {

	usageValidationErr := validateRealtimeBillingUsage(usage)
	billingUsage := usage
	if usageValidationErr != nil {
		billingUsage = &dto.RealtimeUsage{}
		logger.LogError(ctx, "invalid upstream realtime billing usage: "+usageValidationErr.Error())
	}
	var tieredResult *billingexpr.TieredResult
	tieredOk := false
	tieredQuota := 0
	var tieredRes *billingexpr.TieredResult
	if usageValidationErr == nil {
		tieredOk, tieredQuota, tieredRes = TryTieredSettle(relayInfo, billingexpr.TokenParams{
			P:   float64(billingUsage.InputTokens),
			C:   float64(billingUsage.OutputTokens),
			Len: float64(billingUsage.InputTokens),
		})
	}
	if tieredOk {
		tieredResult = tieredRes
	}

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens := billingUsage.InputTokenDetails.TextTokens
	textOutTokens := billingUsage.OutputTokenDetails.TextTokens

	audioInputTokens := billingUsage.InputTokenDetails.AudioTokens
	audioOutTokens := billingUsage.OutputTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(modelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(modelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice
	fixedPriceBilling := usePrice && modelPrice > 0

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  modelName,
		UsePrice:   usePrice,
		ModelPrice: modelPrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota := invalidUsageSettlementQuota(relayInfo)
	if usageValidationErr == nil {
		var clamp *common.QuotaClamp
		quota, clamp = calculateAudioQuota(quotaInfo)
		noteQuotaClamp(relayInfo, clamp)
		if tieredOk {
			quota = tieredQuota
		}
	}

	totalTokens := billingUsage.TotalTokens
	var logContent string
	if !usePrice {
		logContent = fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，音频倍率 %.2f，音频补全倍率 %.2f，分组倍率 %.2f",
			modelRatio, completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), groupRatio)
	} else {
		logContent = fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
	}

	// record all the consume log even if quota is 0
	if usageValidationErr != nil {
		logContent += "（上游返回无效计费信息，按预扣额度结算）"
	} else if totalTokens == 0 && !fixedPriceBilling {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += "（可能是上游超时）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, modelName, relayInfo.FinalPreConsumedQuota))
	}

	logModel := modelName
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateWssOtherInfo(ctx, relayInfo, billingUsage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	if usageValidationErr != nil {
		adminInfo, ok := other["admin_info"].(map[string]interface{})
		if !ok || adminInfo == nil {
			adminInfo = make(map[string]interface{})
			other["admin_info"] = adminInfo
		}
		adminInfo["invalid_billing_usage"] = map[string]interface{}{
			"error":         usageValidationErr.Error(),
			"settled_quota": quota,
		}
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	ip := ""
	if userSetting, err := model.GetUserSetting(relayInfo.UserId, false); err == nil && userSetting.RecordIpLog {
		ip = ctx.ClientIP()
	}
	hasUsage := usageValidationErr != nil || totalTokens != 0 || fixedPriceBilling
	userUsedQuotaDelta := 0
	channelUsedQuotaDelta := 0
	if hasUsage {
		userUsedQuotaDelta = quota
		channelUsedQuotaDelta = quota
	}
	log := model.TaskBillingFinalizationLog{
		UserID:            relayInfo.UserId,
		LogType:           model.LogTypeConsume,
		Content:           logContent,
		ChannelID:         relayInfo.ChannelId,
		ModelName:         logModel,
		Quota:             quota,
		PromptTokens:      billingUsage.InputTokens,
		CompletionTokens:  billingUsage.OutputTokens,
		TokenID:           relayInfo.TokenId,
		TokenName:         tokenName,
		Group:             relayInfo.UsingGroup,
		UseTime:           int(useTimeSeconds),
		IsStream:          relayInfo.IsStream,
		IP:                ip,
		RequestID:         ctx.GetString(common.RequestIdKey),
		UpstreamRequestID: ctx.GetString(common.UpstreamRequestIdKey),
		Username:          ctx.GetString("username"),
		Other:             other,
		NodeName:          common.NodeName,
		CreatedAt:         common.GetTimestamp(),
	}
	if err := FinalizeBilling(ctx, relayInfo, quota, userUsedQuotaDelta, channelUsedQuotaDelta, hasUsage, log); err != nil {
		logger.LogError(ctx, "error finalizing billing: "+err.Error())
	}
}

func CalcOpenRouterCacheCreateTokens(usage dto.Usage, priceData types.PriceData) int {
	if priceData.CacheCreationRatio == 1 {
		return 0
	}
	quotaPrice := priceData.ModelRatio / common.CurrentQuotaPerUnit()
	promptCacheCreatePrice := quotaPrice * priceData.CacheCreationRatio
	promptCacheReadPrice := quotaPrice * priceData.CacheRatio
	completionPrice := quotaPrice * priceData.CompletionRatio

	cost, ok := usage.Cost.(float64)
	if !ok || cost <= 0 || math.IsNaN(cost) || math.IsInf(cost, 0) {
		return -1
	}
	totalPromptTokens := float64(usage.PromptTokens)
	completionTokens := float64(usage.CompletionTokens)
	promptCacheReadTokens := float64(usage.PromptTokensDetails.CachedTokens)

	denominator := promptCacheCreatePrice - quotaPrice
	if denominator == 0 || math.IsNaN(denominator) || math.IsInf(denominator, 0) {
		return -1
	}
	inferred := (cost -
		totalPromptTokens*quotaPrice +
		promptCacheReadTokens*(quotaPrice-promptCacheReadPrice) -
		completionTokens*completionPrice) /
		denominator
	tokens, clamp := common.QuotaRoundChecked(inferred)
	if clamp != nil {
		return -1
	}
	return tokens
}

func PostAudioConsumeQuota(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, usage *dto.Usage, extraContent string) {

	billingUsage, usageValidationErr := effectiveBillingUsage(usage)
	if usageValidationErr == nil && billingUsage == nil {
		usageValidationErr = errors.New("audio billing usage is missing")
	}
	if usageValidationErr == nil {
		usageValidationErr = validateUsageBreakdownTotal("audio total", billingUsage.TotalTokens,
			billingUsage.PromptTokens, billingUsage.CompletionTokens)
	}
	if usageValidationErr != nil {
		billingUsage = &dto.Usage{}
		logger.LogError(ctx, "invalid upstream audio billing usage: "+usageValidationErr.Error())
	}
	var tieredUsedVars map[string]bool
	if snap := relayInfo.TieredBillingSnapshot; snap != nil {
		tieredUsedVars = billingexpr.UsedVars(snap.ExprString)
	}
	var tieredResult *billingexpr.TieredResult
	tieredOk := false
	tieredQuota := 0
	var tieredRes *billingexpr.TieredResult
	if usageValidationErr == nil {
		tieredOk, tieredQuota, tieredRes = TryTieredSettle(relayInfo, BuildTieredTokenParams(billingUsage, false, tieredUsedVars))
	}
	if tieredOk {
		tieredResult = tieredRes
	}

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	textInputTokens := billingUsage.PromptTokensDetails.TextTokens
	textOutTokens := billingUsage.CompletionTokenDetails.TextTokens

	audioInputTokens := billingUsage.PromptTokensDetails.AudioTokens
	audioOutTokens := billingUsage.CompletionTokenDetails.AudioTokens

	tokenName := ctx.GetString("token_name")
	completionRatio := decimal.NewFromFloat(ratio_setting.GetCompletionRatio(relayInfo.OriginModelName))
	audioRatio := decimal.NewFromFloat(ratio_setting.GetAudioRatio(relayInfo.OriginModelName))
	audioCompletionRatio := decimal.NewFromFloat(ratio_setting.GetAudioCompletionRatio(relayInfo.OriginModelName))

	modelRatio := relayInfo.PriceData.ModelRatio
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	modelPrice := relayInfo.PriceData.ModelPrice
	usePrice := relayInfo.PriceData.UsePrice
	fixedPriceBilling := usePrice && modelPrice > 0

	quotaInfo := QuotaInfo{
		InputDetails: TokenDetails{
			TextTokens:  textInputTokens,
			AudioTokens: audioInputTokens,
		},
		OutputDetails: TokenDetails{
			TextTokens:  textOutTokens,
			AudioTokens: audioOutTokens,
		},
		ModelName:  relayInfo.OriginModelName,
		UsePrice:   usePrice,
		ModelPrice: modelPrice,
		ModelRatio: modelRatio,
		GroupRatio: groupRatio,
	}

	quota := invalidUsageSettlementQuota(relayInfo)
	if usageValidationErr == nil {
		var clamp *common.QuotaClamp
		quota, clamp = calculateAudioQuota(quotaInfo)
		noteQuotaClamp(relayInfo, clamp)
		if tieredOk {
			quota = tieredQuota
		}
	}

	totalTokens := billingUsage.TotalTokens
	var logContent string
	if !usePrice {
		logContent = fmt.Sprintf("模型倍率 %.2f，补全倍率 %.2f，音频倍率 %.2f，音频补全倍率 %.2f，分组倍率 %.2f",
			modelRatio, completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), groupRatio)
	} else {
		logContent = fmt.Sprintf("模型价格 %.2f，分组倍率 %.2f", modelPrice, groupRatio)
	}

	// record all the consume log even if quota is 0
	if usageValidationErr != nil {
		logContent += "（上游返回无效计费信息，按预扣额度结算）"
	} else if totalTokens == 0 && !fixedPriceBilling {
		// in this case, must be some error happened
		// we cannot just return, because we may have to return the pre-consumed quota
		quota = 0
		logContent += "（可能是上游超时）"
		logger.LogError(ctx, fmt.Sprintf("total tokens is 0, cannot consume quota, userId %d, channelId %d, "+
			"tokenId %d, model %s， pre-consumed quota %d", relayInfo.UserId, relayInfo.ChannelId, relayInfo.TokenId, relayInfo.OriginModelName, relayInfo.FinalPreConsumedQuota))
	}

	logModel := relayInfo.OriginModelName
	if extraContent != "" {
		logContent += ", " + extraContent
	}
	other := GenerateAudioOtherInfo(ctx, relayInfo, billingUsage, modelRatio, groupRatio,
		completionRatio.InexactFloat64(), audioRatio.InexactFloat64(), audioCompletionRatio.InexactFloat64(), modelPrice, relayInfo.PriceData.GroupRatioInfo.GroupSpecialRatio)
	if tieredResult != nil {
		InjectTieredBillingInfo(other, relayInfo, tieredResult)
	}
	if usageValidationErr != nil {
		adminInfo, ok := other["admin_info"].(map[string]interface{})
		if !ok || adminInfo == nil {
			adminInfo = make(map[string]interface{})
			other["admin_info"] = adminInfo
		}
		adminInfo["invalid_billing_usage"] = map[string]interface{}{
			"error":         usageValidationErr.Error(),
			"settled_quota": quota,
		}
	}
	attachQuotaSaturation(ctx, relayInfo, other)
	ip := ""
	if userSetting, err := model.GetUserSetting(relayInfo.UserId, false); err == nil && userSetting.RecordIpLog {
		ip = ctx.ClientIP()
	}
	hasUsage := usageValidationErr != nil || totalTokens != 0 || fixedPriceBilling
	userUsedQuotaDelta := 0
	channelUsedQuotaDelta := 0
	if hasUsage {
		userUsedQuotaDelta = quota
		channelUsedQuotaDelta = quota
	}
	log := model.TaskBillingFinalizationLog{
		UserID:            relayInfo.UserId,
		LogType:           model.LogTypeConsume,
		Content:           logContent,
		ChannelID:         relayInfo.ChannelId,
		ModelName:         logModel,
		Quota:             quota,
		PromptTokens:      billingUsage.PromptTokens,
		CompletionTokens:  billingUsage.CompletionTokens,
		TokenID:           relayInfo.TokenId,
		TokenName:         tokenName,
		Group:             relayInfo.UsingGroup,
		UseTime:           int(useTimeSeconds),
		IsStream:          relayInfo.IsStream,
		IP:                ip,
		RequestID:         ctx.GetString(common.RequestIdKey),
		UpstreamRequestID: ctx.GetString(common.UpstreamRequestIdKey),
		Username:          ctx.GetString("username"),
		Other:             other,
		NodeName:          common.NodeName,
		CreatedAt:         common.GetTimestamp(),
	}
	if err := FinalizeBilling(ctx, relayInfo, quota, userUsedQuotaDelta, channelUsedQuotaDelta, hasUsage, log); err != nil {
		logger.LogError(ctx, "error finalizing billing: "+err.Error())
	}
	perfmetrics.RecordRelaySampleAsync(relayInfo, true, int64(billingUsage.CompletionTokens))
}

func PreConsumeTokenQuota(relayInfo *relaycommon.RelayInfo, quota int) error {
	if quota < 0 {
		return errors.New("quota 不能为负数！")
	}
	if relayInfo.IsPlayground {
		return nil
	}
	//if relayInfo.TokenUnlimited {
	//	return nil
	//}
	token, err := model.GetTokenByKey(relayInfo.TokenKey, false)
	if err != nil {
		return err
	}
	if !relayInfo.TokenUnlimited && token.RemainQuota < quota {
		return fmt.Errorf("token quota is not enough, token remain quota: %s, need quota: %s", logger.FormatQuota(token.RemainQuota), logger.FormatQuota(quota))
	}
	err = model.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota)
	if err != nil {
		return err
	}
	return nil
}

func PostConsumeQuota(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int, sendEmail bool) (err error) {

	// 1) Consume from wallet quota OR subscription item
	if relayInfo != nil && relayInfo.BillingSource == BillingSourceSubscription {
		if relayInfo.SubscriptionId == 0 {
			return errors.New("subscription id is missing")
		}
		delta := int64(quota)
		if delta != 0 {
			if err := model.PostConsumeUserSubscriptionDelta(relayInfo.SubscriptionId, delta); err != nil {
				return err
			}
			relayInfo.SubscriptionPostDelta += delta
		}
	} else {
		// Wallet
		if quota > 0 {
			err = model.DecreaseUserQuota(relayInfo.UserId, quota, false)
		} else {
			err = model.IncreaseUserQuota(relayInfo.UserId, -quota, false)
		}
		if err != nil {
			return err
		}
	}

	if !relayInfo.IsPlayground {
		if quota > 0 {
			err = model.DecreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, quota)
		} else {
			err = model.IncreaseTokenQuota(relayInfo.TokenId, relayInfo.TokenKey, -quota)
		}
		if err != nil {
			return err
		}
	}

	if sendEmail {
		if (quota + preConsumedQuota) != 0 {
			checkAndSendQuotaNotify(relayInfo, quota, preConsumedQuota)
		}
	}

	return nil
}

func checkAndSendQuotaNotify(relayInfo *relaycommon.RelayInfo, quota int, preConsumedQuota int) {
	if relayInfo == nil {
		return
	}
	userSetting := relayInfo.UserSetting
	threshold := common.GetLegacyOptionInt("QuotaRemindThreshold", &common.QuotaRemindThreshold)
	if userSetting.QuotaWarningThreshold != 0 {
		threshold = common.QuotaFromFloat(userSetting.QuotaWarningThreshold)
	}
	consumeQuota := quota + preConsumedQuota
	shouldNotify, remainingQuota := shouldSendWalletQuotaNotify(relayInfo.UserQuota, consumeQuota, threshold)
	if !shouldNotify {
		return
	}

	prompt := "您的额度即将用尽"
	topUpLink := PaymentReturnURL("/console/topup")
	var content string
	var values []interface{}
	notifyType := userSetting.NotifyType
	if notifyType == "" {
		notifyType = dto.NotifyTypeEmail
	}
	if notifyType == dto.NotifyTypeBark {
		content = "{{value}}，剩余额度：{{value}}，请及时充值"
		values = []interface{}{prompt, logger.FormatQuota(remainingQuota)}
	} else if notifyType == dto.NotifyTypeGotify {
		content = "{{value}}，当前剩余额度为 {{value}}，请及时充值。"
		values = []interface{}{prompt, logger.FormatQuota(remainingQuota)}
	} else {
		content = "{{value}}，当前剩余额度为 {{value}}，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='{{value}}'>{{value}}</a>"
		values = []interface{}{prompt, logger.FormatQuota(remainingQuota), topUpLink, topUpLink}
	}
	userID := relayInfo.UserId
	userEmail := relayInfo.UserEmail
	notification := dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values)
	gopool.Go(func() {
		if err := NotifyUser(userID, userEmail, userSetting, notification); err != nil {
			common.SysError(fmt.Sprintf("failed to send quota notify to user %d: %s", userID, err.Error()))
		}
	})
}

func shouldSendWalletQuotaNotify(userQuota int, consumeQuota int, threshold int) (bool, int) {
	remainingQuota := userQuota - consumeQuota
	if consumeQuota <= 0 {
		return false, remainingQuota
	}
	return userQuota >= threshold && remainingQuota < threshold, remainingQuota
}

func checkAndSendSubscriptionQuotaNotify(relayInfo *relaycommon.RelayInfo) {
	if relayInfo == nil || relayInfo.SubscriptionId == 0 || relayInfo.SubscriptionAmountTotal <= 0 {
		return
	}
	userSetting := relayInfo.UserSetting
	threshold := common.GetLegacyOptionInt("QuotaRemindThreshold", &common.QuotaRemindThreshold)
	if userSetting.QuotaWarningThreshold != 0 {
		threshold = common.QuotaFromFloat(userSetting.QuotaWarningThreshold)
	}
	usedAfter := relayInfo.SubscriptionAmountUsedAfterPreConsume + relayInfo.SubscriptionPostDelta
	remaining := relayInfo.SubscriptionAmountTotal - usedAfter
	if remaining >= int64(threshold) {
		return
	}
	remainingQuota := common.QuotaFromDecimal(decimal.NewFromInt(remaining))

	prompt := "您的订阅额度即将用尽"
	topUpLink := PaymentReturnURL("/console/topup")
	var content string
	var values []interface{}
	notifyType := userSetting.NotifyType
	if notifyType == "" {
		notifyType = dto.NotifyTypeEmail
	}
	if notifyType == dto.NotifyTypeBark {
		content = "{{value}}，剩余额度：{{value}}，请及时充值"
		values = []interface{}{prompt, logger.FormatQuota(remainingQuota)}
	} else if notifyType == dto.NotifyTypeGotify {
		content = "{{value}}，当前剩余额度为 {{value}}，请及时充值。"
		values = []interface{}{prompt, logger.FormatQuota(remainingQuota)}
	} else {
		content = "{{value}}，当前剩余额度为 {{value}}，为了不影响您的使用，请及时充值。<br/>充值链接：<a href='{{value}}'>{{value}}</a>"
		values = []interface{}{prompt, logger.FormatQuota(remainingQuota), topUpLink, topUpLink}
	}
	userID := relayInfo.UserId
	userEmail := relayInfo.UserEmail
	notification := dto.NewNotify(dto.NotifyTypeQuotaExceed, prompt, content, values)
	gopool.Go(func() {
		if err := NotifyUser(userID, userEmail, userSetting, notification); err != nil {
			common.SysError(fmt.Sprintf("failed to send subscription quota notify to user %d: %s", userID, err.Error()))
		}
	})
}
