package service

import (
	"fmt"
	"math"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createMidjourneyBillingOutboxTask(t *testing.T, db *gorm.DB, suffix string) (model.User, model.Token, model.Channel, *model.Midjourney) {
	t.Helper()
	user, token := createDurableBillingBalances(t, db, "midjourney-outbox-"+suffix)
	channel := model.Channel{Name: "midjourney-outbox-" + suffix, Key: "channel-key"}
	require.NoError(t, db.Create(&channel).Error)
	info := &relaycommon.RelayInfo{RequestId: "midjourney-outbox-request-" + suffix, UserId: user.Id, TokenId: token.Id}
	const purpose = "midjourney-submit:IMAGINE"
	billingTaskID, err := DurableQuotaAdjustmentTaskID(info, purpose)
	require.NoError(t, err)
	task := &model.Midjourney{
		UserId:            user.Id,
		Action:            "IMAGINE",
		MjId:              "midjourney-outbox-task-" + suffix,
		Status:            "SUBMITTED",
		Progress:          "0%",
		ChannelId:         channel.Id,
		Quota:             30,
		BillingRequestId:  info.RequestId,
		BillingPurpose:    purpose,
		BillingSource:     BillingSourceWallet,
		BillingTaskId:     billingTaskID,
		BillingTokenId:    token.Id,
		BillingModelName:  "mj_imagine",
		BillingTokenName:  "test-token",
		BillingGroup:      "default",
		BillingNodeName:   "midjourney-origin-node",
		BillingLogContent: fmt.Sprintf("Midjourney billing reconciliation %s", suffix),
		BillingLogOther:   `{"model_price":1,"group_ratio":1}`,
		SubmitTime:        1_785_230_400_000,
	}
	require.NoError(t, db.Create(task).Error)
	return user, token, channel, task
}

func TestReconcileMidjourneyBillingCreatesMissingChargeAndFinalizesOnce(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Midjourney{}, &model.Log{}))
	oldLogConsumeEnabled := common.LogConsumeEnabled
	oldDataExportEnabled := common.DataExportEnabled
	common.LogConsumeEnabled = true
	common.DataExportEnabled = true
	model.CacheQuotaDataLock.Lock()
	oldQuotaData := model.CacheQuotaData
	model.CacheQuotaData = make(map[string]*model.QuotaData)
	model.CacheQuotaDataLock.Unlock()
	t.Cleanup(func() {
		common.LogConsumeEnabled = oldLogConsumeEnabled
		common.DataExportEnabled = oldDataExportEnabled
		model.CacheQuotaDataLock.Lock()
		model.CacheQuotaData = oldQuotaData
		model.CacheQuotaDataLock.Unlock()
	})
	user, token, channel, task := createMidjourneyBillingOutboxTask(t, db, "missing-charge")

	var taskCount int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&taskCount).Error)
	assert.Zero(t, taskCount)
	require.NoError(t, ReconcileMidjourneyBilling(task, false))
	require.NoError(t, ReconcileMidjourneyBilling(task, false))

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	require.NoError(t, db.First(task, task.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 370, token.RemainQuota)
	assert.Equal(t, 130, token.UsedQuota)
	assert.Equal(t, 30, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, int64(30), channel.UsedQuota)
	assert.True(t, task.BillingFinalized)

	var logs []model.Log
	require.NoError(t, db.Where("request_id = ? AND type = ?", task.BillingTaskId, model.LogTypeConsume).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, 30, logs[0].Quota)
	var exportedData []model.QuotaData
	require.NoError(t, db.Where("billing_event_id = ?", task.BillingTaskId).Find(&exportedData).Error)
	require.Len(t, exportedData, 1)
	assert.Equal(t, 1, exportedData[0].Count)
	assert.Equal(t, 30, exportedData[0].Quota)
	assert.Equal(t, int64(1_785_229_200), exportedData[0].CreatedAt)
	assert.Equal(t, "midjourney-origin-node", exportedData[0].NodeName)
}

func TestReconcileMidjourneyBillingDoesNotFinalizeBeforeAnalyticsPersist(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Midjourney{}, &model.Log{}))
	oldLogConsumeEnabled := common.LogConsumeEnabled
	oldDataExportEnabled := common.DataExportEnabled
	common.LogConsumeEnabled = true
	common.DataExportEnabled = true
	t.Cleanup(func() {
		common.LogConsumeEnabled = oldLogConsumeEnabled
		common.DataExportEnabled = oldDataExportEnabled
	})
	_, _, _, task := createMidjourneyBillingOutboxTask(t, db, "analytics-retry")
	require.NoError(t, db.Migrator().DropTable(&model.QuotaData{}))

	require.Error(t, ReconcileMidjourneyBilling(task, false))
	require.NoError(t, db.First(task, task.Id).Error)
	assert.False(t, task.BillingFinalized)

	var logCount int64
	require.NoError(t, db.Model(&model.Log{}).Where("request_id = ?", task.BillingTaskId).Count(&logCount).Error)
	assert.Zero(t, logCount)

	require.NoError(t, db.AutoMigrate(&model.QuotaData{}))
	require.NoError(t, ReconcileMidjourneyBilling(task, false))
	require.NoError(t, db.First(task, task.Id).Error)
	assert.True(t, task.BillingFinalized)

	var exportedData []model.QuotaData
	require.NoError(t, db.Where("billing_event_id = ?", task.BillingTaskId).Find(&exportedData).Error)
	require.Len(t, exportedData, 1)
}

func TestReconcileMidjourneyBillingFinalizesWorkerAppliedCharge(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Midjourney{}, &model.Log{}))
	oldLogConsumeEnabled := common.LogConsumeEnabled
	common.LogConsumeEnabled = true
	t.Cleanup(func() { common.LogConsumeEnabled = oldLogConsumeEnabled })
	user, token, channel, task := createMidjourneyBillingOutboxTask(t, db, "worker-applied")
	adjustmentRequestID, _, err := durableQuotaAdjustmentRequestID(&relaycommon.RelayInfo{
		RequestId: task.BillingRequestId,
		UserId:    task.UserId,
		TokenId:   task.BillingTokenId,
	}, task.BillingPurpose)
	require.NoError(t, err)
	_, err = model.EnqueueBillingAdjustment(model.BillingAdjustment{
		RequestID:     adjustmentRequestID,
		Kind:          model.BillingAdjustmentSettle,
		FundingSource: model.BillingAdjustmentWallet,
		UserID:        task.UserId,
		TokenID:       task.BillingTokenId,
		FundingDelta:  task.Quota,
		TokenDelta:    task.Quota,
	})
	require.NoError(t, err)
	require.NoError(t, model.ProcessPendingBillingAdjustments(10))

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Zero(t, user.UsedQuota)
	assert.Zero(t, user.RequestCount)
	var logCount int64
	require.NoError(t, db.Model(&model.Log{}).Count(&logCount).Error)
	assert.Zero(t, logCount)

	require.NoError(t, ReconcileMidjourneyBilling(task, false))
	require.NoError(t, ReconcileMidjourneyBilling(task, false))
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 370, token.RemainQuota)
	assert.Equal(t, 130, token.UsedQuota)
	assert.Equal(t, 30, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	assert.Equal(t, int64(30), channel.UsedQuota)
	require.NoError(t, db.Model(&model.Log{}).Count(&logCount).Error)
	assert.Equal(t, int64(1), logCount)
}

func TestReconcileMidjourneyBillingSaturatesAggregateOverflowAndFinalizes(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(t *testing.T, db *gorm.DB, user *model.User, channel *model.Channel)
	}{
		{
			name: "user used quota",
			mutate: func(t *testing.T, db *gorm.DB, user *model.User, _ *model.Channel) {
				require.NoError(t, db.Model(user).Update("used_quota", common.MaxQuota).Error)
			},
		},
		{
			name: "user request count",
			mutate: func(t *testing.T, db *gorm.DB, user *model.User, _ *model.Channel) {
				require.NoError(t, db.Model(user).Update("request_count", common.MaxQuota).Error)
			},
		},
		{
			name: "channel used quota",
			mutate: func(t *testing.T, db *gorm.DB, _ *model.User, channel *model.Channel) {
				require.NoError(t, db.Model(channel).Update("used_quota", int64(math.MaxInt64)).Error)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupDurableBillingSessionTest(t)
			require.NoError(t, db.AutoMigrate(&model.Channel{}, &model.Midjourney{}, &model.Log{}))
			oldLogConsumeEnabled := common.LogConsumeEnabled
			common.LogConsumeEnabled = true
			t.Cleanup(func() { common.LogConsumeEnabled = oldLogConsumeEnabled })
			user, _, channel, task := createMidjourneyBillingOutboxTask(t, db, "aggregate-overflow-"+test.name)
			test.mutate(t, db, &user, &channel)
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			usedQuotaBefore := user.UsedQuota
			requestCountBefore := user.RequestCount
			channelUsedQuotaBefore := channel.UsedQuota

			require.NoError(t, ReconcileMidjourneyBilling(task, false))
			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			require.NoError(t, db.First(task, task.Id).Error)
			if usedQuotaBefore == common.MaxQuota {
				assert.Equal(t, common.MaxQuota, user.UsedQuota)
			} else {
				assert.Equal(t, usedQuotaBefore+task.Quota, user.UsedQuota)
			}
			if requestCountBefore == common.MaxQuota {
				assert.Equal(t, common.MaxQuota, user.RequestCount)
			} else {
				assert.Equal(t, requestCountBefore+1, user.RequestCount)
			}
			if channelUsedQuotaBefore == math.MaxInt64 {
				assert.Equal(t, int64(math.MaxInt64), channel.UsedQuota)
			} else {
				assert.Equal(t, channelUsedQuotaBefore+int64(task.Quota), channel.UsedQuota)
			}
			assert.True(t, task.BillingFinalized)
			var logCount int64
			require.NoError(t, db.Model(&model.Log{}).Count(&logCount).Error)
			assert.Equal(t, int64(1), logCount)
		})
	}
}
