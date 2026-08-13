package service

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ApplyDurableQuotaAdjustment applies a post-response quota delta through the
// billing-adjustment queue. The purpose is part of the idempotency key so one
// request can safely have independent settlement, violation-fee, and legacy
// per-call charges.
func ApplyDurableQuotaAdjustment(relayInfo *relaycommon.RelayInfo, purpose string, quotaDelta int, notificationPreConsumedQuota int, sendNotify bool) (model.BillingAdjustmentResult, bool, error) {
	adjustmentRequestID, purpose, err := durableQuotaAdjustmentRequestID(relayInfo, purpose)
	if err != nil {
		return model.BillingAdjustmentResult{}, false, err
	}
	fundingSource := durableQuotaAdjustmentFundingSource(relayInfo)
	tokenDelta := quotaDelta
	if relayInfo.IsPlayground {
		tokenDelta = 0
	}
	adjustment := model.BillingAdjustment{
		RequestID:      adjustmentRequestID,
		Kind:           model.BillingAdjustmentSettle,
		FundingSource:  fundingSource,
		UserID:         relayInfo.UserId,
		SubscriptionID: relayInfo.SubscriptionId,
		TokenID:        relayInfo.TokenId,
		TokenKeyHash:   model.BillingTokenKeyHash(relayInfo.TokenKey),
		FundingDelta:   quotaDelta,
		TokenDelta:     tokenDelta,
	}

	result, applied, err := persistAndProcessDurableQuotaAdjustment(adjustment, purpose)
	if err != nil || !applied {
		return result, applied, err
	}

	if fundingSource == model.BillingAdjustmentSubscription {
		relayInfo.SubscriptionPostDelta += int64(result.SubscriptionDelta)
		relayInfo.SubscriptionWalletOverflow += result.WalletDelta
	}
	if sendNotify && int64(quotaDelta)+int64(notificationPreConsumedQuota) != 0 {
		if fundingSource == model.BillingAdjustmentSubscription {
			checkAndSendSubscriptionQuotaNotify(relayInfo)
			if result.WalletDelta > 0 {
				checkAndSendQuotaNotify(relayInfo, result.WalletDelta, 0)
			}
		} else {
			checkAndSendQuotaNotify(relayInfo, quotaDelta, notificationPreConsumedQuota)
		}
	}
	return result, true, nil
}

func durableQuotaAdjustmentRequestID(relayInfo *relaycommon.RelayInfo, purpose string) (string, string, error) {
	if relayInfo == nil {
		return "", "", errors.New("relay info is nil")
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return "", "", errors.New("billing adjustment purpose is empty")
	}
	if relayInfo.RequestId == "" {
		return "", "", errors.New("billing adjustment request id is empty")
	}
	return fmt.Sprintf("%s\x00purpose:%s", relayInfo.RequestId, purpose), purpose, nil
}

func durableQuotaAdjustmentFundingSource(relayInfo *relaycommon.RelayInfo) string {
	if relayInfo.BillingSource == BillingSourceSubscription {
		return model.BillingAdjustmentSubscription
	}
	return model.BillingAdjustmentWallet
}

// DurableQuotaAdjustmentTaskID returns the task ID used by a per-call durable
// charge. It lets an asynchronous failure persist a reversal that waits for
// the original charge to complete.
func DurableQuotaAdjustmentTaskID(relayInfo *relaycommon.RelayInfo, purpose string) (string, error) {
	requestID, _, err := durableQuotaAdjustmentRequestID(relayInfo, purpose)
	if err != nil {
		return "", err
	}
	return model.BillingAdjustmentTaskID(requestID, model.BillingAdjustmentSettle), nil
}

// ReverseDurableQuotaAdjustment reverses the exact persisted result of an
// earlier per-call charge. This preserves subscription/wallet overflow splits
// and token quota in one idempotent transaction. If the charge has not yet
// completed, the reversal remains pending for the background worker.
func ReverseDurableQuotaAdjustment(relayInfo *relaycommon.RelayInfo, originalPurpose string, reversalPurpose string) (model.BillingAdjustmentResult, bool, error) {
	originalTaskID, err := DurableQuotaAdjustmentTaskID(relayInfo, originalPurpose)
	if err != nil {
		return model.BillingAdjustmentResult{}, false, err
	}
	_, reversalPurpose, err = durableQuotaAdjustmentRequestID(relayInfo, reversalPurpose)
	if err != nil {
		return model.BillingAdjustmentResult{}, false, err
	}
	fundingSource := durableQuotaAdjustmentFundingSource(relayInfo)
	adjustment := model.BillingAdjustment{
		RequestID:        fmt.Sprintf("reversal-of:%s", originalTaskID),
		Kind:             model.BillingAdjustmentRefund,
		FundingSource:    fundingSource,
		UserID:           relayInfo.UserId,
		SubscriptionID:   relayInfo.SubscriptionId,
		TokenID:          relayInfo.TokenId,
		ReversalOfTaskID: originalTaskID,
	}
	result, applied, err := persistAndProcessDurableQuotaAdjustment(adjustment, reversalPurpose)
	if err != nil || !applied {
		return result, applied, err
	}
	if fundingSource == model.BillingAdjustmentSubscription {
		relayInfo.SubscriptionPostDelta += int64(result.SubscriptionDelta)
		relayInfo.SubscriptionWalletOverflow += result.WalletDelta
	}
	return result, true, nil
}

func persistAndProcessDurableQuotaAdjustment(adjustment model.BillingAdjustment, purpose string) (model.BillingAdjustmentResult, bool, error) {
	var taskID string
	if err := retryBillingOperation(func() error {
		var enqueueErr error
		taskID, enqueueErr = model.EnqueueBillingAdjustment(adjustment)
		return enqueueErr
	}); err != nil {
		return model.BillingAdjustmentResult{}, false, fmt.Errorf("persist %s billing adjustment: %w", purpose, err)
	}
	result, err := model.ProcessBillingAdjustmentWithResult(taskID)
	if err != nil {
		return model.BillingAdjustmentResult{}, false, fmt.Errorf("%s billing adjustment queued for retry: %w", purpose, err)
	}
	if result.AlreadyProcessed {
		return result, false, nil
	}
	return result, true, nil
}
