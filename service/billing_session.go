package service

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/types"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/gin-gonic/gin"
)

// ---------------------------------------------------------------------------
// BillingSession — 统一计费会话
// ---------------------------------------------------------------------------

// BillingSession 封装单次请求的预扣费/结算/退款生命周期。
// 实现 relaycommon.BillingSettler 接口。
type BillingSession struct {
	relayInfo         *relaycommon.RelayInfo
	funding           FundingSource
	preConsumedQuota  int // 实际预扣额度（信任用户可能为 0）
	tokenConsumed     int // 令牌额度实际扣减量
	tokenKeyHash      string
	extraReserved     int  // 发送前补充预扣的额度（订阅退款时需要单独回滚）
	reservationExists bool // durable reservation exists; retries must return its canonical totals
	fundingSettled    bool // funding.Settle 已成功，资金来源已提交
	settlementQueued  bool // 结算差额已持久化，不能再走失败退款
	settled           bool // Settle 全部完成（资金 + 令牌）
	refunded          bool // Refund 已持久化（可能仍在等待重试）
	mu                sync.Mutex
}

var billingAdjustmentWorkerOnce sync.Once

// StartBillingAdjustmentWorker retries durable settlement/refund and affiliate
// reward records. Application startup owns this process-lifetime worker; relay
// requests must not create hidden background workers tied to mutable test or
// shutdown database state.
func StartBillingAdjustmentWorker() {
	billingAdjustmentWorkerOnce.Do(func() {
		gopool.Go(func() {
			for {
				if err := model.ProcessPendingBillingAdjustments(100); err != nil {
					common.SysLog("error retrying pending billing adjustments: " + err.Error())
				}
				if err := model.ProcessPendingTaskBillingFinalizations(100); err != nil {
					common.SysLog("error retrying pending task billing finalizations: " + err.Error())
				}
				if err := model.ProcessPendingAffiliateRewards(100); err != nil {
					common.SysLog("error retrying pending affiliate rewards: " + err.Error())
				}
				time.Sleep(5 * time.Second)
			}
		})
	})
}

// Settle 根据实际消耗额度进行结算。差额会先写入持久化调整队列，再在
// 同一数据库事务中更新资金来源、令牌额度和完成标记。
func (s *BillingSession) Settle(actualQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return nil
	}
	if actualQuota < 0 || actualQuota > common.MaxQuota {
		return fmt.Errorf("actual quota is outside storage range: %d", actualQuota)
	}
	delta := actualQuota - s.preConsumedQuota
	if delta == 0 {
		if err := model.MarkBillingReservationTerminal(s.relayInfo.RequestId, model.BillingAdjustmentSettle); err != nil {
			return fmt.Errorf("persist zero-delta billing settlement: %w", err)
		}
		s.fundingSettled = true
		s.settled = true
		return nil
	}
	tokenDelta := delta
	if s.relayInfo.IsPlayground {
		tokenDelta = 0
	}
	adjustment := model.BillingAdjustment{
		RequestID:      s.relayInfo.RequestId,
		Kind:           model.BillingAdjustmentSettle,
		FundingSource:  s.funding.Source(),
		UserID:         s.relayInfo.UserId,
		SubscriptionID: s.relayInfo.SubscriptionId,
		TokenID:        s.relayInfo.TokenId,
		TokenKeyHash:   s.tokenKeyHash,
		FundingDelta:   delta,
		TokenDelta:     tokenDelta,
	}
	if adjustment.FundingSource == BillingSourceSubscription {
		adjustment.SubscriptionRequestID = s.relayInfo.RequestId
	}
	var taskID string
	err := retryBillingOperation(func() error {
		var enqueueErr error
		taskID, enqueueErr = model.EnqueueBillingAdjustment(adjustment)
		return enqueueErr
	})
	if err != nil {
		return fmt.Errorf("persist billing settlement: %w", err)
	}
	s.settlementQueued = true
	result, err := model.ProcessBillingAdjustmentWithResult(taskID)
	if err != nil {
		return fmt.Errorf("billing settlement queued for retry: %w", err)
	}

	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(result.SubscriptionDelta)
		s.relayInfo.SubscriptionWalletOverflow += result.WalletDelta
	}
	s.fundingSettled = true
	s.settled = true
	return nil
}

func (s *BillingSession) buildBillingFinalization(
	actualQuota int,
	userUsedQuotaDelta int,
	channelUsedQuotaDelta int,
	incrementRequestCount bool,
	log model.TaskBillingFinalizationLog,
) (model.TaskBillingFinalizationPayload, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildBillingFinalizationLocked(actualQuota, userUsedQuotaDelta, channelUsedQuotaDelta, incrementRequestCount, log)
}

func (s *BillingSession) buildBillingFinalizationLocked(
	actualQuota int,
	userUsedQuotaDelta int,
	channelUsedQuotaDelta int,
	incrementRequestCount bool,
	log model.TaskBillingFinalizationLog,
) (model.TaskBillingFinalizationPayload, error) {
	if s.refunded {
		return model.TaskBillingFinalizationPayload{}, errors.New("billing session was already refunded")
	}
	if actualQuota < 0 || actualQuota > common.MaxQuota {
		return model.TaskBillingFinalizationPayload{}, fmt.Errorf("actual quota is outside storage range: %d", actualQuota)
	}
	delta := actualQuota - s.preConsumedQuota
	tokenDelta := delta
	if s.relayInfo.IsPlayground {
		tokenDelta = 0
	}
	adjustment := model.BillingAdjustment{
		RequestID:      s.relayInfo.RequestId,
		Kind:           model.BillingAdjustmentSettle,
		FundingSource:  s.funding.Source(),
		UserID:         s.relayInfo.UserId,
		SubscriptionID: s.relayInfo.SubscriptionId,
		TokenID:        s.relayInfo.TokenId,
		TokenKeyHash:   s.tokenKeyHash,
		FundingDelta:   delta,
		TokenDelta:     tokenDelta,
	}
	if adjustment.FundingSource == BillingSourceSubscription {
		adjustment.SubscriptionRequestID = s.relayInfo.RequestId
	}
	channelCreatedTime := int64(0)
	if s.relayInfo.ChannelMeta != nil {
		channelCreatedTime = s.relayInfo.ChannelMeta.ChannelCreateTime
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

// SettleWithFinalization persists the complete synchronous usage outbox before
// making the session non-refundable. Balance settlement, usage aggregates,
// and the consume log can therefore be replayed together after any crash.
func (s *BillingSession) SettleWithFinalization(actualQuota int, payload model.TaskBillingFinalizationPayload) (model.BillingAdjustmentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.settled {
		return model.BillingAdjustmentResult{AlreadyProcessed: true}, nil
	}
	expected, err := s.buildBillingFinalizationLocked(
		actualQuota,
		payload.UserUsedQuotaDelta,
		payload.ChannelUsedQuotaDelta,
		payload.IncrementUserRequestCount,
		payload.Log,
	)
	if err != nil {
		return model.BillingAdjustmentResult{}, err
	}
	if expected.Adjustment != payload.Adjustment {
		return model.BillingAdjustmentResult{}, errors.New("billing finalization adjustment does not match session")
	}

	var finalizationID string
	err = retryBillingOperation(func() error {
		var enqueueErr error
		finalizationID, enqueueErr = model.EnqueueTaskBillingFinalization(payload)
		return enqueueErr
	})
	if err != nil {
		return model.BillingAdjustmentResult{}, fmt.Errorf("persist billing finalization: %w", err)
	}
	s.settlementQueued = true
	return s.processPersistedBillingFinalizationLocked(actualQuota, finalizationID)
}

func (s *BillingSession) processPersistedBillingFinalization(actualQuota int, finalizationID string) (model.BillingAdjustmentResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processPersistedBillingFinalizationLocked(actualQuota, finalizationID)
}

func (s *BillingSession) processPersistedBillingFinalizationLocked(actualQuota int, finalizationID string) (model.BillingAdjustmentResult, error) {
	if s.settled {
		return model.BillingAdjustmentResult{AlreadyProcessed: true}, nil
	}
	if actualQuota < 0 || actualQuota > common.MaxQuota {
		return model.BillingAdjustmentResult{}, fmt.Errorf("actual quota is outside storage range: %d", actualQuota)
	}
	expectedID := model.BillingAdjustmentTaskID(s.relayInfo.RequestId, model.BillingAdjustmentSettle)
	if finalizationID == "" || finalizationID != expectedID {
		return model.BillingAdjustmentResult{}, errors.New("persisted billing finalization does not match session")
	}
	// The caller only invokes this after the outbox transaction commits. Mark it
	// queued before processing so a later error path cannot race a refund against
	// the durable settlement replay.
	s.settlementQueued = true
	result, err := model.ProcessTaskBillingFinalization(finalizationID)
	if err != nil {
		return result, fmt.Errorf("billing finalization queued for retry: %w", err)
	}
	if s.funding.Source() == BillingSourceSubscription {
		s.relayInfo.SubscriptionPostDelta += int64(result.SubscriptionDelta)
		s.relayInfo.SubscriptionWalletOverflow += result.WalletDelta
	}
	s.fundingSettled = true
	s.settled = true
	return result, nil
}

// Refund 退还所有预扣费。退款先持久化，钱包/订阅和令牌退款随后在一个
// 事务中执行；处理失败时记录保持 pending，可安全重试。
func (s *BillingSession) Refund(c *gin.Context) {
	s.mu.Lock()
	if s.settled || s.refunded || !s.needsRefundLocked() {
		s.mu.Unlock()
		return
	}

	logger.LogInfo(c, fmt.Sprintf("用户 %d 请求失败, 返还预扣费（token_quota=%s, funding=%s）",
		s.relayInfo.UserId,
		logger.FormatQuota(s.tokenConsumed),
		s.funding.Source(),
	))

	tokenDelta := -s.tokenConsumed
	if s.relayInfo.IsPlayground {
		tokenDelta = 0
	}
	adjustment := model.BillingAdjustment{
		RequestID:             s.relayInfo.RequestId,
		Kind:                  model.BillingAdjustmentRefund,
		FundingSource:         s.funding.Source(),
		UserID:                s.relayInfo.UserId,
		SubscriptionID:        s.relayInfo.SubscriptionId,
		SubscriptionRequestID: s.relayInfo.RequestId,
		TokenID:               s.relayInfo.TokenId,
		TokenKeyHash:          s.tokenKeyHash,
		FundingDelta:          -s.preConsumedQuota,
		TokenDelta:            tokenDelta,
		ExtraReserved:         int64(s.extraReserved),
	}
	var taskID string
	err := retryBillingOperation(func() error {
		var enqueueErr error
		taskID, enqueueErr = model.EnqueueBillingAdjustment(adjustment)
		return enqueueErr
	})
	if err != nil {
		s.mu.Unlock()
		common.SysLog("error persisting billing refund: " + err.Error())
		return
	}
	s.refunded = true
	s.mu.Unlock()
	if err := model.ProcessBillingAdjustment(taskID); err != nil {
		common.SysLog("billing refund queued for retry: " + err.Error())
	}
}

// NeedsRefund 返回是否存在需要退还的预扣状态。
func (s *BillingSession) NeedsRefund() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.needsRefundLocked()
}

func (s *BillingSession) needsRefundLocked() bool {
	if s.settled || s.refunded || s.fundingSettled || s.settlementQueued {
		// fundingSettled 时资金来源已提交结算，不能再退预扣费
		return false
	}
	if s.preConsumedQuota > 0 || s.tokenConsumed > 0 {
		return true
	}
	// A zero-cost wallet request still owns a durable pending reservation. On
	// failure it needs a zero-delta refund marker so cleanup and user deletion
	// are not blocked forever, even though no balance needs restoring.
	if s.reservationExists {
		return true
	}
	// 订阅可能在 tokenConsumed=0 时仍预扣了额度
	if sub, ok := s.funding.(*SubscriptionFunding); ok && sub.preConsumed > 0 {
		return true
	}
	return false
}

// GetPreConsumedQuota 返回实际预扣的额度。
func (s *BillingSession) GetPreConsumedQuota() int {
	return s.preConsumedQuota
}

func (s *BillingSession) Reserve(targetQuota int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.settled || s.refunded || targetQuota <= s.preConsumedQuota {
		return nil
	}

	result, err := model.ExtendBillingReservation(model.BillingReservationExtension{
		RequestID:      s.relayInfo.RequestId,
		UserID:         s.relayInfo.UserId,
		TokenID:        s.relayInfo.TokenId,
		TokenKey:       s.relayInfo.TokenKey,
		FundingSource:  s.funding.Source(),
		ModelName:      s.relayInfo.OriginModelName,
		IsPlayground:   s.relayInfo.IsPlayground,
		SubscriptionID: s.relayInfo.SubscriptionId,
		TargetQuota:    targetQuota,
	})
	if err != nil {
		return billingReservationAPIError(err)
	}
	s.applyBillingReservationResult(result)
	return nil
}

// ---------------------------------------------------------------------------
// PreConsume — 统一预扣费入口
// ---------------------------------------------------------------------------

// preConsume atomically reserves funding and token balances behind a durable
// request-ID idempotency record. Every accepted request keeps a funded
// reservation: delivering a streaming or non-streaming upstream response
// before a later settlement write would otherwise leave a crash window in
// which the provider cost could never be recovered.
func (s *BillingSession) preConsume(c *gin.Context, quota int) *types.NewAPIError {
	if quota > 0 {
		logger.LogInfo(c, fmt.Sprintf("用户 %d 需要预扣费 %s (funding=%s)", s.relayInfo.UserId, logger.FormatQuota(quota), s.funding.Source()))
	}

	result, err := model.CreateBillingReservation(model.BillingReservationRequest{
		RequestID:      s.relayInfo.RequestId,
		UserID:         s.relayInfo.UserId,
		TokenID:        s.relayInfo.TokenId,
		TokenKey:       s.relayInfo.TokenKey,
		FundingSource:  s.funding.Source(),
		ModelName:      s.relayInfo.OriginModelName,
		IsPlayground:   s.relayInfo.IsPlayground,
		RequestedQuota: quota,
		InitialQuota:   quota,
		TrustBypass:    false,
	})
	if err != nil {
		return billingReservationAPIError(err)
	}
	s.applyBillingReservationResult(result)
	return nil
}

func billingReservationAPIError(err error) *types.NewAPIError {
	switch {
	case errors.Is(err, model.ErrBillingReservationInsufficientToken):
		return types.NewErrorWithStatusCode(err, types.ErrorCodePreConsumeTokenQuotaFailed, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	case errors.Is(err, model.ErrBillingReservationInsufficientWallet),
		errors.Is(err, model.ErrBillingReservationNoActiveSubscription),
		errors.Is(err, model.ErrBillingReservationInsufficientSubscription):
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInsufficientUserQuota, http.StatusForbidden, types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
	case errors.Is(err, model.ErrBillingReservationContextMismatch):
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	case errors.Is(err, model.ErrBillingReservationTerminal):
		return types.NewErrorWithStatusCode(err, types.ErrorCodeInvalidRequest, http.StatusConflict, types.ErrOptionWithSkipRetry())
	default:
		return types.NewError(err, types.ErrorCodeUpdateDataError, types.ErrOptionWithSkipRetry())
	}
}

func (s *BillingSession) applyBillingReservationResult(result *model.BillingReservationResult) {
	if result == nil {
		return
	}
	s.reservationExists = true
	s.preConsumedQuota = result.ReservedQuota
	s.tokenConsumed = result.TokenReserved
	s.tokenKeyHash = result.TokenKeyHash
	s.extraReserved = 0
	switch funding := s.funding.(type) {
	case *WalletFunding:
		funding.consumed = result.ReservedQuota
	case *SubscriptionFunding:
		funding.amount = int64(result.ReservedQuota)
		funding.subscriptionId = result.SubscriptionID
		funding.preConsumed = int64(result.ReservedQuota)
		funding.AmountTotal = result.SubscriptionAmountTotal
		funding.AmountUsedAfter = result.SubscriptionAmountUsedAfter
		if planInfo, err := model.GetSubscriptionPlanInfoByUserSubscriptionId(result.SubscriptionID); err == nil && planInfo != nil {
			funding.PlanId = planInfo.PlanId
			funding.PlanTitle = planInfo.PlanTitle
		}
	}
	s.syncRelayInfo()
}

// syncRelayInfo 将 BillingSession 的状态同步到 RelayInfo 的兼容字段上。
func (s *BillingSession) syncRelayInfo() {
	info := s.relayInfo
	info.FinalPreConsumedQuota = s.preConsumedQuota
	info.BillingSource = s.funding.Source()

	if sub, ok := s.funding.(*SubscriptionFunding); ok {
		info.SubscriptionId = sub.subscriptionId
		info.SubscriptionPreConsumed = sub.preConsumed + int64(s.extraReserved)
		info.SubscriptionPostDelta = 0
		info.SubscriptionWalletOverflow = 0
		info.SubscriptionAmountTotal = sub.AmountTotal
		info.SubscriptionAmountUsedAfterPreConsume = sub.AmountUsedAfter + int64(s.extraReserved)
		info.SubscriptionPlanId = sub.PlanId
		info.SubscriptionPlanTitle = sub.PlanTitle
	} else {
		info.SubscriptionId = 0
		info.SubscriptionPreConsumed = 0
	}
}

// ---------------------------------------------------------------------------
// NewBillingSession 工厂 — 根据计费偏好创建会话并处理回退
// ---------------------------------------------------------------------------

// NewBillingSession 根据用户计费偏好创建 BillingSession，处理 subscription_first / wallet_first 的回退。
func NewBillingSession(c *gin.Context, relayInfo *relaycommon.RelayInfo, preConsumedQuota int) (*BillingSession, *types.NewAPIError) {
	if relayInfo == nil {
		return nil, types.NewError(fmt.Errorf("relayInfo is nil"), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}
	if relayInfo.RequestId == "" {
		return nil, types.NewErrorWithStatusCode(errors.New("billing request id is empty"), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}
	pref := common.NormalizeBillingPreference(relayInfo.UserSetting.BillingPreference)
	existingReservation, err := model.FindBillingReservationByRequestID(relayInfo.RequestId)
	if err != nil {
		return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
	}

	// 钱包路径需要先检查用户额度
	tryWallet := func() (*BillingSession, *types.NewAPIError) {
		userQuota, err := model.GetUserQuota(relayInfo.UserId, false)
		if err != nil {
			return nil, types.NewError(err, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if existingReservation == nil && userQuota <= 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("用户额度不足, 剩余额度: %s", logger.FormatQuota(userQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		if existingReservation == nil && userQuota-preConsumedQuota < 0 {
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("预扣费额度失败, 用户剩余额度: %s, 需要预扣费额度: %s", logger.FormatQuota(userQuota), logger.FormatQuota(preConsumedQuota)),
				types.ErrorCodeInsufficientUserQuota, http.StatusForbidden,
				types.ErrOptionWithSkipRetry(), types.ErrOptionWithNoRecordErrorLog())
		}
		relayInfo.UserQuota = userQuota
		session := &BillingSession{
			relayInfo:         relayInfo,
			funding:           &WalletFunding{userId: relayInfo.UserId},
			reservationExists: existingReservation != nil,
		}
		if apiErr := session.preConsume(c, preConsumedQuota); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}

	trySubscription := func() (*BillingSession, *types.NewAPIError) {
		subConsume := int64(preConsumedQuota)
		if subConsume <= 0 {
			subConsume = 1
		}
		session := &BillingSession{
			relayInfo:         relayInfo,
			reservationExists: existingReservation != nil,
			funding: &SubscriptionFunding{
				requestId: relayInfo.RequestId,
				userId:    relayInfo.UserId,
				modelName: relayInfo.OriginModelName,
				amount:    subConsume,
			},
		}
		// 必须传 subConsume 而非 preConsumedQuota，保证 SubscriptionFunding.amount、
		// preConsume 参数和 FinalPreConsumedQuota 三者一致，避免订阅多扣费。
		if apiErr := session.preConsume(c, int(subConsume)); apiErr != nil {
			return nil, apiErr
		}
		return session, nil
	}
	if existingReservation != nil {
		switch existingReservation.FundingSource {
		case BillingSourceWallet:
			return tryWallet()
		case BillingSourceSubscription:
			return trySubscription()
		default:
			return nil, types.NewErrorWithStatusCode(
				fmt.Errorf("invalid persisted billing source: %s", existingReservation.FundingSource),
				types.ErrorCodeInvalidRequest,
				http.StatusBadRequest,
				types.ErrOptionWithSkipRetry(),
			)
		}
	}

	switch pref {
	case "subscription_only":
		return trySubscription()
	case "wallet_only":
		return tryWallet()
	case "wallet_first":
		session, err := tryWallet()
		if err != nil {
			if err.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				return trySubscription()
			}
			return nil, err
		}
		return session, nil
	case "subscription_first":
		fallthrough
	default:
		hasSub, subCheckErr := model.HasActiveUserSubscription(relayInfo.UserId)
		if subCheckErr != nil {
			return nil, types.NewError(subCheckErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
		}
		if !hasSub {
			return tryWallet()
		}
		session, apiErr := trySubscription()
		if apiErr != nil {
			if apiErr.GetErrorCode() == types.ErrorCodeInsufficientUserQuota {
				// 仅当用户的活跃订阅允许钱包回退时才回退到钱包，否则返回订阅额度不足错误
				allowOverflow, overflowErr := model.UserActiveSubscriptionsAllowWalletOverflow(relayInfo.UserId)
				if overflowErr != nil {
					return nil, types.NewError(overflowErr, types.ErrorCodeQueryDataError, types.ErrOptionWithSkipRetry())
				}
				if allowOverflow {
					return tryWallet()
				}
				return nil, apiErr
			}
			return nil, apiErr
		}
		return session, nil
	}
}
