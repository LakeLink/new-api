package service

import (
	"errors"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
)

// ReconcileMidjourneyBilling treats the persisted Midjourney row as a durable
// billing outbox. It recreates a missing deterministic settlement after a
// process crash, finishes a worker-applied settlement's log and aggregate side
// effects, and is safe to call repeatedly from both submission and polling.
func ReconcileMidjourneyBilling(task *model.Midjourney, sendNotify bool) error {
	if task == nil {
		return errors.New("Midjourney task is nil")
	}
	if task.BillingPurpose == "" {
		return nil
	}
	if task.Quota < 0 {
		return errors.New("Midjourney billing quota is negative")
	}
	if task.BillingSource != BillingSourceWallet && task.BillingSource != BillingSourceSubscription {
		return errors.New("Midjourney billing source is invalid")
	}
	relayInfo := &relaycommon.RelayInfo{
		RequestId:      task.BillingRequestId,
		UserId:         task.UserId,
		TokenId:        task.BillingTokenId,
		BillingSource:  task.BillingSource,
		SubscriptionId: task.BillingSubscriptionId,
		IsPlayground:   task.BillingIsPlayground,
	}
	expectedTaskID, err := DurableQuotaAdjustmentTaskID(relayInfo, task.BillingPurpose)
	if err != nil {
		return err
	}
	if task.BillingTaskId == "" || task.BillingTaskId != expectedTaskID {
		return errors.New("Midjourney settlement identity is invalid")
	}
	if _, _, err := ApplyDurableQuotaAdjustment(relayInfo, task.BillingPurpose, task.Quota, 0, sendNotify); err != nil {
		return err
	}
	if common.GetLegacyOptionBool("LogConsumeEnabled", &common.LogConsumeEnabled) &&
		common.GetLegacyOptionBool("DataExportEnabled", &common.DataExportEnabled) {
		username, err := model.GetUsernameById(task.UserId, false)
		if err != nil {
			return err
		}
		createdAt := task.SubmitTime / int64(1000)
		nodeName := task.BillingNodeName
		if nodeName == "" {
			nodeName = common.NodeName
		}
		if err := model.RecordQuotaDataEvent(task.BillingTaskId, model.QuotaDataLogParams{
			UserID:    task.UserId,
			Username:  username,
			ModelName: task.BillingModelName,
			Quota:     task.Quota,
			CreatedAt: createdAt,
			UseGroup:  task.BillingGroup,
			TokenID:   task.BillingTokenId,
			ChannelID: task.ChannelId,
			NodeName:  nodeName,
		}); err != nil {
			return err
		}
	}
	_, err = model.FinalizeMidjourneyBilling(task.Id, task.BillingTaskId)
	if err != nil {
		return err
	}
	task.BillingFinalized = true
	return nil
}
