package service

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

var errUnsupportedBillingFinalizationSession = errors.New("billing settler does not support durable finalization")

const (
	BillingSourceWallet       = "wallet"
	BillingSourceSubscription = "subscription"
)

// PreConsumeBilling 根据用户计费偏好创建 BillingSession 并执行预扣费。
// 会话存储在 relayInfo.Billing 上，供后续 Settle / Refund 使用。
func PreConsumeBilling(c *gin.Context, preConsumedQuota int, relayInfo *relaycommon.RelayInfo) *types.NewAPIError {
	if relayInfo != nil && relayInfo.RequestId == "" {
		if c != nil {
			relayInfo.RequestId = c.GetString(common.RequestIdKey)
		}
		if relayInfo.RequestId == "" {
			relayInfo.RequestId = common.NewRequestId()
		}
	}
	if relayInfo != nil && relayInfo.QuotaClamp != nil {
		return types.NewErrorWithStatusCode(
			relayInfo.QuotaClamp,
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	if preConsumedQuota < 0 {
		return types.NewErrorWithStatusCode(
			fmt.Errorf("pre-consume quota cannot be negative: %d", preConsumedQuota),
			types.ErrorCodeModelPriceError,
			http.StatusBadRequest,
			types.ErrOptionWithSkipRetry(),
		)
	}
	session, apiErr := NewBillingSession(c, relayInfo, preConsumedQuota)
	if apiErr != nil {
		return apiErr
	}
	relayInfo.Billing = session
	return nil
}

// ---------------------------------------------------------------------------
// SettleBilling — 后结算辅助函数
// ---------------------------------------------------------------------------

// SettleBilling 执行计费结算。如果 RelayInfo 上有 BillingSession 则通过 session 结算，
// 否则回退到旧的 PostConsumeQuota 路径（兼容按次计费等场景）。
func SettleBilling(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int) error {
	if relayInfo.Billing != nil {
		preConsumed := relayInfo.Billing.GetPreConsumedQuota()
		delta := actualQuota - preConsumed

		if delta > 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后补扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else if delta < 0 {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费后返还扣费：%s（实际消耗：%s，预扣费：%s）",
				logger.FormatQuota(-delta),
				logger.FormatQuota(actualQuota),
				logger.FormatQuota(preConsumed),
			))
		} else {
			logger.LogInfo(ctx, fmt.Sprintf("预扣费与实际消耗一致，无需调整：%s（按次计费）",
				logger.FormatQuota(actualQuota),
			))
		}

		if err := relayInfo.Billing.Settle(actualQuota); err != nil {
			return err
		}

		// 发送额度通知（订阅计费使用订阅剩余额度）
		if actualQuota != 0 {
			if relayInfo.BillingSource == BillingSourceSubscription {
				checkAndSendSubscriptionQuotaNotify(relayInfo)
				if relayInfo.SubscriptionWalletOverflow > 0 {
					checkAndSendQuotaNotify(relayInfo, relayInfo.SubscriptionWalletOverflow, 0)
				}
			} else {
				checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
			}
		}
		return nil
	}

	// 回退：无 BillingSession 时使用旧路径
	quotaDelta := actualQuota - relayInfo.FinalPreConsumedQuota
	if quotaDelta != 0 {
		_, _, err := ApplyDurableQuotaAdjustment(relayInfo, "request-settlement", quotaDelta, relayInfo.FinalPreConsumedQuota, true)
		return err
	}
	return nil
}

// BuildBillingFinalization creates the canonical payload used to atomically
// finalize a synchronous request. It does not persist anything, which lets an
// async task submit transaction store its task row and this payload together.
func BuildBillingFinalization(
	relayInfo *relaycommon.RelayInfo,
	actualQuota int,
	userUsedQuotaDelta int,
	channelUsedQuotaDelta int,
	incrementRequestCount bool,
	log model.TaskBillingFinalizationLog,
) (model.TaskBillingFinalizationPayload, error) {
	if relayInfo == nil {
		return model.TaskBillingFinalizationPayload{}, errors.New("billing finalization relay info is nil")
	}
	if relayInfo.RequestId == "" {
		return model.TaskBillingFinalizationPayload{}, errors.New("billing finalization request id is empty")
	}
	if actualQuota < 0 || actualQuota > common.MaxQuota {
		return model.TaskBillingFinalizationPayload{}, fmt.Errorf("actual quota is outside storage range: %d", actualQuota)
	}
	if userUsedQuotaDelta < 0 || userUsedQuotaDelta > common.MaxQuota ||
		channelUsedQuotaDelta < 0 || channelUsedQuotaDelta > common.MaxQuota {
		return model.TaskBillingFinalizationPayload{}, errors.New("billing finalization aggregate quota is outside storage range")
	}
	if log.PromptTokens < 0 || log.PromptTokens > common.MaxTokensLimit ||
		log.CompletionTokens < 0 || log.CompletionTokens > common.MaxTokensLimit ||
		log.PromptTokens > common.MaxTokensLimit-log.CompletionTokens {
		return model.TaskBillingFinalizationPayload{}, errors.New("billing finalization token count is outside supported range")
	}
	if log.UseTime < 0 {
		log.UseTime = 0
	}

	channelID := 0
	if relayInfo.ChannelMeta != nil {
		channelID = relayInfo.ChannelMeta.ChannelId
	}
	log.UserID = relayInfo.UserId
	log.LogType = model.LogTypeConsume
	log.ChannelID = channelID
	log.Quota = actualQuota
	log.TokenID = relayInfo.TokenId
	log.Group = relayInfo.UsingGroup
	log.RequestID = relayInfo.RequestId
	log.SubscriptionPreConsumed = relayInfo.SubscriptionPreConsumed
	log.SubscriptionAmountTotal = relayInfo.SubscriptionAmountTotal
	log.SubscriptionAmountUsedAfterPreConsume = relayInfo.SubscriptionAmountUsedAfterPreConsume
	if log.NodeName == "" {
		log.NodeName = common.NodeName
	}
	if log.CreatedAt <= 0 {
		log.CreatedAt = common.GetTimestamp()
	}

	if relayInfo.Billing != nil {
		session, ok := relayInfo.Billing.(*BillingSession)
		if !ok {
			return model.TaskBillingFinalizationPayload{Log: log}, errUnsupportedBillingFinalizationSession
		}
		return session.buildBillingFinalization(actualQuota, userUsedQuotaDelta, channelUsedQuotaDelta, incrementRequestCount, log)
	}

	quotaDelta := actualQuota - relayInfo.FinalPreConsumedQuota
	tokenDelta := quotaDelta
	if relayInfo.IsPlayground {
		tokenDelta = 0
	}
	adjustment := model.BillingAdjustment{
		RequestID:      relayInfo.RequestId,
		Kind:           model.BillingAdjustmentSettle,
		FundingSource:  durableQuotaAdjustmentFundingSource(relayInfo),
		UserID:         relayInfo.UserId,
		SubscriptionID: relayInfo.SubscriptionId,
		TokenID:        relayInfo.TokenId,
		TokenKeyHash:   model.BillingTokenKeyHash(relayInfo.TokenKey),
		FundingDelta:   quotaDelta,
		TokenDelta:     tokenDelta,
	}
	if adjustment.FundingSource == model.BillingAdjustmentSubscription {
		adjustment.SubscriptionRequestID = relayInfo.RequestId
	}
	channelCreatedTime := int64(0)
	if relayInfo.ChannelMeta != nil {
		channelCreatedTime = relayInfo.ChannelMeta.ChannelCreateTime
	}
	return model.TaskBillingFinalizationPayload{
		Adjustment:                adjustment,
		UserUsedQuotaDelta:        userUsedQuotaDelta,
		IncrementUserRequestCount: incrementRequestCount,
		ChannelUsedQuotaDelta:     channelUsedQuotaDelta,
		ChannelCreatedTime:        channelCreatedTime,
		Log:                       log,
	}, nil
}

// FinalizeBilling durably settles balances, usage aggregates, and the consume
// log as one replayable operation. A persisted processing failure remains
// queued and is deliberately not refundable.
func FinalizeBilling(
	ctx *gin.Context,
	relayInfo *relaycommon.RelayInfo,
	actualQuota int,
	userUsedQuotaDelta int,
	channelUsedQuotaDelta int,
	incrementRequestCount bool,
	log model.TaskBillingFinalizationLog,
) error {
	if relayInfo != nil && relayInfo.RequestId == "" {
		relayInfo.RequestId = ctx.GetString(common.RequestIdKey)
		if relayInfo.RequestId == "" {
			relayInfo.RequestId = common.NewRequestId()
		}
	}
	payload, err := BuildBillingFinalization(relayInfo, actualQuota, userUsedQuotaDelta, channelUsedQuotaDelta, incrementRequestCount, log)
	if errors.Is(err, errUnsupportedBillingFinalizationSession) {
		// Compatibility for test/custom BillingSettler implementations. Production
		// requests use BillingSession and always take the durable path above.
		if incrementRequestCount {
			model.UpdateUserUsedQuotaAndRequestCount(relayInfo.UserId, userUsedQuotaDelta)
		}
		if channelUsedQuotaDelta != 0 {
			model.UpdateChannelUsedQuota(payload.Log.ChannelID, channelUsedQuotaDelta)
		}
		if settleErr := SettleBilling(ctx, relayInfo, actualQuota); settleErr != nil {
			return settleErr
		}
		return recordCompatibilityBillingLog(payload.Log)
	}
	if err != nil {
		return err
	}

	var result model.BillingAdjustmentResult
	if session, ok := relayInfo.Billing.(*BillingSession); ok {
		result, err = session.SettleWithFinalization(actualQuota, payload)
	} else {
		var finalizationID string
		err = retryBillingOperation(func() error {
			var enqueueErr error
			finalizationID, enqueueErr = model.EnqueueTaskBillingFinalization(payload)
			return enqueueErr
		})
		if err == nil {
			result, err = model.ProcessTaskBillingFinalization(finalizationID)
		}
	}
	if err != nil {
		return err
	}
	applyBillingFinalizationResult(relayInfo, actualQuota, result)
	return nil
}

// ProcessPersistedBillingFinalization completes an outbox that was already
// committed atomically with another domain record, such as an async task.
func ProcessPersistedBillingFinalization(ctx *gin.Context, relayInfo *relaycommon.RelayInfo, actualQuota int, finalizationID string) error {
	if relayInfo == nil {
		return errors.New("billing finalization relay info is nil")
	}
	var (
		result model.BillingAdjustmentResult
		err    error
	)
	if session, ok := relayInfo.Billing.(*BillingSession); ok {
		result, err = session.processPersistedBillingFinalization(actualQuota, finalizationID)
	} else {
		result, err = model.ProcessTaskBillingFinalization(finalizationID)
	}
	if err != nil {
		return err
	}
	applyBillingFinalizationResult(relayInfo, actualQuota, result)
	return nil
}

func applyBillingFinalizationResult(relayInfo *relaycommon.RelayInfo, actualQuota int, result model.BillingAdjustmentResult) {
	if relayInfo.BillingSource == BillingSourceSubscription && relayInfo.Billing == nil {
		relayInfo.SubscriptionPostDelta += int64(result.SubscriptionDelta)
		relayInfo.SubscriptionWalletOverflow += result.WalletDelta
	}
	if result.AlreadyProcessed || actualQuota == 0 {
		return
	}
	preConsumed := relayInfo.FinalPreConsumedQuota
	if relayInfo.BillingSource == BillingSourceSubscription {
		checkAndSendSubscriptionQuotaNotify(relayInfo)
		if relayInfo.SubscriptionWalletOverflow > 0 {
			checkAndSendQuotaNotify(relayInfo, relayInfo.SubscriptionWalletOverflow, 0)
		}
		return
	}
	checkAndSendQuotaNotify(relayInfo, actualQuota-preConsumed, preConsumed)
}

func recordCompatibilityBillingLog(log model.TaskBillingFinalizationLog) error {
	return model.RecordTaskBillingLogWithRequestID(model.RecordTaskBillingLogParams{
		UserId:            log.UserID,
		LogType:           log.LogType,
		Content:           log.Content,
		ChannelId:         log.ChannelID,
		ModelName:         log.ModelName,
		Quota:             log.Quota,
		PromptTokens:      log.PromptTokens,
		CompletionTokens:  log.CompletionTokens,
		TokenId:           log.TokenID,
		TokenName:         log.TokenName,
		Group:             log.Group,
		UseTime:           log.UseTime,
		IsStream:          log.IsStream,
		IP:                log.IP,
		RequestID:         log.RequestID,
		UpstreamRequestID: log.UpstreamRequestID,
		Username:          log.Username,
		Other:             log.Other,
		NodeName:          log.NodeName,
	}, "", log.CreatedAt)
}
