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
	if relayInfo == nil {
		return model.BillingAdjustmentResult{}, false, errors.New("relay info is nil")
	}
	purpose = strings.TrimSpace(purpose)
	if purpose == "" {
		return model.BillingAdjustmentResult{}, false, errors.New("billing adjustment purpose is empty")
	}
	if relayInfo.RequestId == "" {
		return model.BillingAdjustmentResult{}, false, errors.New("billing adjustment request id is empty")
	}
	fundingSource := model.BillingAdjustmentWallet
	if relayInfo.BillingSource == BillingSourceSubscription {
		fundingSource = model.BillingAdjustmentSubscription
	}
	tokenDelta := quotaDelta
	if relayInfo.IsPlayground {
		tokenDelta = 0
	}
	adjustment := model.BillingAdjustment{
		RequestID:      fmt.Sprintf("%s\x00purpose:%s", relayInfo.RequestId, purpose),
		Kind:           model.BillingAdjustmentSettle,
		FundingSource:  fundingSource,
		UserID:         relayInfo.UserId,
		SubscriptionID: relayInfo.SubscriptionId,
		TokenID:        relayInfo.TokenId,
		FundingDelta:   quotaDelta,
		TokenDelta:     tokenDelta,
	}

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
	applied := !result.AlreadyProcessed
	if !applied {
		return result, false, nil
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
