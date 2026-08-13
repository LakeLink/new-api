package service

import (
	"fmt"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

const (
	ViolationFeeCodePrefix     = "violation_fee."
	CSAMViolationMarker        = "Failed check: SAFETY_CHECK_TYPE"
	ContentViolatesUsageMarker = "Content violates usage guidelines"
)

func IsViolationFeeCode(code types.ErrorCode) bool {
	return strings.HasPrefix(string(code), ViolationFeeCodePrefix)
}

func HasCSAMViolationMarker(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), CSAMViolationMarker) || strings.Contains(err.Error(), ContentViolatesUsageMarker) {
		return true
	}
	msg := err.ToOpenAIError().Message
	return strings.Contains(msg, CSAMViolationMarker) || strings.Contains(err.Error(), ContentViolatesUsageMarker)
}

func WrapAsViolationFeeGrokCSAM(err *types.NewAPIError) *types.NewAPIError {
	if err == nil {
		return nil
	}
	oai := err.ToOpenAIError()
	oai.Type = string(types.ErrorCodeViolationFeeGrokCSAM)
	oai.Code = string(types.ErrorCodeViolationFeeGrokCSAM)
	return types.WithOpenAIError(oai, err.StatusCode, types.ErrOptionWithSkipRetry())
}

// NormalizeViolationFeeError ensures:
// - if the CSAM marker is present, error.code is set to a stable violation-fee code and skip-retry is enabled.
// - if error.code already has the violation-fee prefix, skip-retry is enabled.
//
// It must be called before retry decision logic.
func NormalizeViolationFeeError(err *types.NewAPIError) *types.NewAPIError {
	if err == nil {
		return nil
	}

	if HasCSAMViolationMarker(err) {
		return WrapAsViolationFeeGrokCSAM(err)
	}

	if IsViolationFeeCode(err.GetErrorCode()) {
		oai := err.ToOpenAIError()
		return types.WithOpenAIError(oai, err.StatusCode, types.ErrOptionWithSkipRetry())
	}

	return err
}

func shouldChargeViolationFee(err *types.NewAPIError) bool {
	if err == nil {
		return false
	}
	if err.GetErrorCode() == types.ErrorCodeViolationFeeGrokCSAM {
		return true
	}
	// In case some callers didn't normalize, keep a safety net.
	return HasCSAMViolationMarker(err)
}

func calcViolationFeeQuota(amount, groupRatio float64) (int, *common.QuotaClamp) {
	if amount <= 0 {
		return 0, nil
	}
	if groupRatio <= 0 {
		return 0, nil
	}
	quota, clamp := common.QuotaRoundChecked(amount * common.CurrentQuotaPerUnit() * groupRatio)
	if clamp != nil {
		return 0, clamp
	}
	if quota <= 0 {
		return 0, nil
	}
	return quota, nil
}

// ChargeViolationFeeIfNeeded charges an additional fee after the normal flow finishes (including refund).
// It uses Grok fee settings as the fee policy.
func ChargeViolationFeeIfNeeded(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, apiErr *types.NewAPIError) bool {
	if ctx == nil || relayInfo == nil || apiErr == nil {
		return false
	}
	//if relayInfo.IsPlayground {
	//	return false
	//}
	if !shouldChargeViolationFee(apiErr) {
		return false
	}

	settings := model_setting.GetGrokSettings()
	if settings == nil || !settings.ViolationDeductionEnabled {
		return false
	}

	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	feeQuota, clamp := calcViolationFeeQuota(settings.ViolationDeductionAmount, groupRatio)
	if clamp != nil {
		noteQuotaClamp(relayInfo, clamp)
		logger.LogError(ctx, "refusing saturated violation fee: "+clamp.Error())
		return false
	}
	if feeQuota <= 0 {
		return false
	}

	adjustmentRequestID, _, err := durableQuotaAdjustmentRequestID(relayInfo, "violation-fee:grok-csam")
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to build violation fee: %s", err.Error()))
		return false
	}
	fundingSource := durableQuotaAdjustmentFundingSource(relayInfo)
	tokenDelta := feeQuota
	if relayInfo.IsPlayground {
		tokenDelta = 0
	}

	useTimeSeconds := time.Now().Unix() - relayInfo.StartTime.Unix()
	tokenName := ctx.GetString("token_name")
	oai := apiErr.ToOpenAIError()

	other := map[string]any{
		"violation_fee":        true,
		"violation_fee_code":   string(types.ErrorCodeViolationFeeGrokCSAM),
		"fee_quota":            feeQuota,
		"base_amount":          settings.ViolationDeductionAmount,
		"group_ratio":          groupRatio,
		"status_code":          apiErr.StatusCode,
		"upstream_error_type":  oai.Type,
		"upstream_error_code":  fmt.Sprintf("%v", oai.Code),
		"violation_fee_marker": CSAMViolationMarker,
	}
	ip := ""
	if userSetting, settingErr := model.GetUserSetting(relayInfo.UserId, false); settingErr == nil && userSetting.RecordIpLog {
		ip = ctx.ClientIP()
	}
	channelCreatedTime := int64(0)
	if relayInfo.ChannelMeta != nil {
		channelCreatedTime = relayInfo.ChannelMeta.ChannelCreateTime
	}
	payload := model.TaskBillingFinalizationPayload{
		Adjustment: model.BillingAdjustment{
			RequestID:      adjustmentRequestID,
			Kind:           model.BillingAdjustmentSettle,
			FundingSource:  fundingSource,
			UserID:         relayInfo.UserId,
			SubscriptionID: relayInfo.SubscriptionId,
			TokenID:        relayInfo.TokenId,
			TokenKeyHash:   model.BillingTokenKeyHash(relayInfo.TokenKey),
			FundingDelta:   feeQuota,
			TokenDelta:     tokenDelta,
		},
		UserUsedQuotaDelta:        feeQuota,
		IncrementUserRequestCount: true,
		ChannelUsedQuotaDelta:     feeQuota,
		ChannelCreatedTime:        channelCreatedTime,
		Log: model.TaskBillingFinalizationLog{
			UserID:            relayInfo.UserId,
			LogType:           model.LogTypeConsume,
			Content:           "Violation fee charged",
			ChannelID:         relayInfo.ChannelId,
			ModelName:         relayInfo.OriginModelName,
			Quota:             feeQuota,
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
		},
	}
	result, err := processTaskBillingFinalization(payload)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to charge violation fee: %s", err.Error()))
		return false
	}
	if !result.AlreadyProcessed {
		if fundingSource == model.BillingAdjustmentSubscription {
			relayInfo.SubscriptionPostDelta += int64(result.SubscriptionDelta)
			relayInfo.SubscriptionWalletOverflow += result.WalletDelta
			checkAndSendSubscriptionQuotaNotify(relayInfo)
			if result.WalletDelta > 0 {
				checkAndSendQuotaNotify(relayInfo, result.WalletDelta, 0)
			}
		} else {
			checkAndSendQuotaNotify(relayInfo, feeQuota, 0)
		}
	}

	return true
}
