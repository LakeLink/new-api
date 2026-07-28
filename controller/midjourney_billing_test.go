package controller

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupMidjourneyBillingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldRedisEnabled := common.RedisEnabled
	oldRedisClient := common.RDB
	oldMemoryCacheEnabled := common.MemoryCacheEnabled

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", url.QueryEscape(t.Name()))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	model.DB = db
	model.LOG_DB = db
	common.RedisEnabled = false
	common.MemoryCacheEnabled = false
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.Token{},
		&model.Channel{},
		&model.UserSubscription{},
		&model.SystemTask{},
		&model.Midjourney{},
		&model.Log{},
		&model.BillingReservation{},
		&model.TaskBillingFinalization{},
		&model.QuotaData{},
	))

	t.Cleanup(func() {
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.RedisEnabled = oldRedisEnabled
		common.RDB = oldRedisClient
		common.MemoryCacheEnabled = oldMemoryCacheEnabled
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func TestRefundFailedMidjourneyTaskUsesPersistedWalletAndTokenContext(t *testing.T) {
	db := setupMidjourneyBillingTestDB(t)
	user := model.User{Username: "midjourney-refund", Password: "password", Quota: 900, AffCode: "midjourney-refund-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "midjourney-refund-token", RemainQuota: 400, UsedQuota: 100}
	require.NoError(t, db.Create(&token).Error)
	relayInfo := &relaycommon.RelayInfo{
		RequestId: "midjourney-refund-request",
		UserId:    user.Id,
		TokenId:   token.Id,
	}
	const purpose = "midjourney-submit:IMAGINE"
	_, applied, err := service.ApplyDurableQuotaAdjustment(relayInfo, purpose, 30, 0, false)
	require.NoError(t, err)
	require.True(t, applied)

	task := &model.Midjourney{
		Id:               42,
		UserId:           user.Id,
		Quota:            30,
		BillingRequestId: relayInfo.RequestId,
		BillingPurpose:   purpose,
		BillingSource:    service.BillingSourceWallet,
		BillingTokenId:   token.Id,
	}
	result, applied, err := refundFailedMidjourneyTask(task)
	require.NoError(t, err)
	assert.True(t, applied)
	assert.Equal(t, model.BillingAdjustmentResult{WalletDelta: -30, TokenDelta: -30}, result)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 900, user.Quota)
	assert.Equal(t, 400, token.RemainQuota)
	assert.Equal(t, 100, token.UsedQuota)
}

func TestRefundFailedLegacyMidjourneyTaskDoesNotCreateUnverifiableCredit(t *testing.T) {
	db := setupMidjourneyBillingTestDB(t)
	user := model.User{Username: "midjourney-legacy-refund", Password: "password", Quota: 870, AffCode: "midjourney-legacy-refund-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "midjourney-legacy-refund-token", RemainQuota: 370, UsedQuota: 130}
	require.NoError(t, db.Create(&token).Error)

	task := &model.Midjourney{Id: 43, UserId: user.Id, Code: 1, Quota: 30}
	result, applied, err := refundFailedMidjourneyTask(task)
	require.ErrorContains(t, err, "no verifiable billing charge")
	assert.False(t, applied)
	assert.Equal(t, model.BillingAdjustmentResult{}, result)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 870, user.Quota)
	assert.Equal(t, 370, token.RemainQuota)
	assert.Equal(t, 130, token.UsedQuota)
	var adjustmentCount int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&adjustmentCount).Error)
	assert.Zero(t, adjustmentCount)
}

func TestRefundFailedUnbilledMidjourneyTaskDoesNotCreateCredit(t *testing.T) {
	db := setupMidjourneyBillingTestDB(t)
	user := model.User{Username: "midjourney-unbilled", Password: "password", Quota: 900, AffCode: "midjourney-unbilled-aff"}
	require.NoError(t, db.Create(&user).Error)

	task := &model.Midjourney{
		Id:               44,
		UserId:           user.Id,
		Quota:            30,
		BillingRequestId: "midjourney-unbilled-request",
		BillingSource:    service.BillingSourceWallet,
	}
	_, applied, err := refundFailedMidjourneyTask(task)
	require.NoError(t, err)
	assert.False(t, applied)

	require.NoError(t, db.First(&user, user.Id).Error)
	assert.Equal(t, 900, user.Quota)
	var adjustmentCount int64
	require.NoError(t, db.Model(&model.SystemTask{}).Count(&adjustmentCount).Error)
	assert.Zero(t, adjustmentCount)
}

func TestRefundFailedMidjourneyTaskFinalizesZeroChargeReversal(t *testing.T) {
	db := setupMidjourneyBillingTestDB(t)
	user := model.User{Username: "midjourney-zero-refund", Password: "password", Quota: 900, AffCode: "midjourney-zero-refund-aff"}
	require.NoError(t, db.Create(&user).Error)
	channel := model.Channel{Name: "midjourney-zero-refund", Key: "channel-key"}
	require.NoError(t, db.Create(&channel).Error)
	info := &relaycommon.RelayInfo{RequestId: "midjourney-zero-refund-request", UserId: user.Id}
	const purpose = "midjourney-submit:IMAGINE"
	billingTaskID, err := service.DurableQuotaAdjustmentTaskID(info, purpose)
	require.NoError(t, err)
	task := &model.Midjourney{
		UserId:           user.Id,
		Action:           constant.MjActionImagine,
		MjId:             "midjourney-zero-refund-task",
		Status:           "FAILURE",
		Progress:         "100%",
		ChannelId:        channel.Id,
		BillingRequestId: info.RequestId,
		BillingPurpose:   purpose,
		BillingSource:    service.BillingSourceWallet,
		BillingTaskId:    billingTaskID,
	}
	require.NoError(t, db.Create(task).Error)
	require.NoError(t, service.ReconcileMidjourneyBilling(task, false))
	require.NoError(t, refundAndRecordFailedMidjourneyTask(context.Background(), task, "zero-price task failed"))

	require.NoError(t, db.First(task, task.Id).Error)
	assert.True(t, task.BillingFinalized)
	assert.True(t, task.BillingRefunded)
	var adjustments []model.SystemTask
	require.NoError(t, db.Where("type = ?", model.SystemTaskTypeBillingAdjustment).Order("id").Find(&adjustments).Error)
	require.Len(t, adjustments, 2)
	assert.Equal(t, model.SystemTaskStatusSucceeded, adjustments[0].Status)
	assert.Equal(t, model.SystemTaskStatusSucceeded, adjustments[1].Status)
	var refundLogs []model.Log
	require.NoError(t, db.Where("request_id = ? AND type = ?", model.BillingAdjustmentReversalTaskID(billingTaskID), model.LogTypeRefund).Find(&refundLogs).Error)
	require.Len(t, refundLogs, 1)
	assert.Zero(t, refundLogs[0].Quota)
}

func TestMidjourneyPollRefundsEveryBilledRowForDuplicateUpstreamID(t *testing.T) {
	db := setupMidjourneyBillingTestDB(t)
	user := model.User{Username: "midjourney-duplicate", Password: "password", Quota: 1_000, AffCode: "midjourney-duplicate-aff"}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "midjourney-duplicate-token", RemainQuota: 500}
	require.NoError(t, db.Create(&token).Error)

	const (
		upstreamTaskID = "shared-midjourney-task"
		channelID      = 91_501
		quota          = 30
		purpose        = "midjourney-submit:IMAGINE"
	)
	requestIDs := []string{"midjourney-duplicate-first", "midjourney-duplicate-second"}
	for _, requestID := range requestIDs {
		info := &relaycommon.RelayInfo{RequestId: requestID, UserId: user.Id, TokenId: token.Id}
		_, applied, err := service.ApplyDurableQuotaAdjustment(info, purpose, quota, 0, false)
		require.NoError(t, err)
		require.True(t, applied)
		billingTaskID, err := service.DurableQuotaAdjustmentTaskID(info, purpose)
		require.NoError(t, err)

		task := model.Midjourney{
			Code:             1,
			UserId:           user.Id,
			Action:           constant.MjActionImagine,
			MjId:             upstreamTaskID,
			Status:           "SUBMITTED",
			Progress:         "0%",
			ChannelId:        channelID,
			Quota:            quota,
			BillingRequestId: requestID,
			BillingPurpose:   purpose,
			BillingSource:    service.BillingSourceWallet,
			BillingTaskId:    billingTaskID,
			BillingTokenId:   token.Id,
		}
		require.NoError(t, db.Create(&task).Error)
	}

	summary, err := runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, err)

	assert.Equal(t, 2, summary.UnfinishedTasks)
	assert.Equal(t, 1, summary.ChannelsScanned)
	var tasks []model.Midjourney
	require.NoError(t, db.Order("id").Find(&tasks).Error)
	require.Len(t, tasks, 2)
	for _, task := range tasks {
		assert.Equal(t, "FAILURE", task.Status)
		assert.Equal(t, "100%", task.Progress)
	}
	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	assert.Equal(t, 1_000, user.Quota)
	assert.Equal(t, 500, token.RemainQuota)
	assert.Zero(t, token.UsedQuota)

	var refundLogs []model.Log
	require.NoError(t, db.Where("type = ?", model.LogTypeRefund).Order("id").Find(&refundLogs).Error)
	require.Len(t, refundLogs, 2)
	assert.Equal(t, quota, refundLogs[0].Quota)
	assert.Equal(t, quota, refundLogs[1].Quota)
}

func TestMidjourneyPollRejectsReplacementChannelBeforeOutboundRequest(t *testing.T) {
	db := setupMidjourneyBillingTestDB(t)

	// An invalid URL makes any accidental outbound path fail before it can
	// mutate the task. The replacement guard must instead terminally fail the
	// row with its specific reason.
	baseURL := "://replacement-channel-must-not-be-contacted"
	channel := model.Channel{
		Name:        "midjourney-replacement-channel",
		Key:         "replacement-secret",
		BaseURL:     &baseURL,
		CreatedTime: 1_700_000_020,
		Status:      common.ChannelStatusEnabled,
	}
	require.NoError(t, db.Create(&channel).Error)
	task := model.Midjourney{
		Code:               0,
		UserId:             19,
		Action:             constant.MjActionInPaint,
		MjId:               "midjourney-old-provider-task",
		Status:             "SUBMITTED",
		Progress:           "10%",
		ChannelId:          channel.Id,
		ChannelCreatedTime: channel.CreatedTime - 1,
	}
	// Seed a historical row directly: current insertion paths reject this
	// mismatch, but deployments can contain tasks accepted before the snapshot
	// invariant was introduced.
	require.NoError(t, db.Create(&task).Error)

	summary, err := runMidjourneyTaskUpdateOnce(context.Background(), nil)
	require.NoError(t, err)

	assert.Equal(t, 1, summary.UnfinishedTasks)
	assert.Equal(t, 1, summary.ChannelsScanned)
	require.NoError(t, db.First(&task, task.Id).Error)
	assert.Equal(t, "FAILURE", task.Status)
	assert.Equal(t, "100%", task.Progress)
	assert.Contains(t, task.FailReason, "原渠道已被替换")
}

func TestMidjourneyPollPropagatesTaskQueryFailure(t *testing.T) {
	db := setupMidjourneyBillingTestDB(t)
	injectedErr := errors.New("injected unfinished Midjourney query failure")
	const callbackName = "test:fail_unfinished_midjourney_query"
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "midjourneys" {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, db.Callback().Query().Remove(callbackName))
	})

	summary, err := runMidjourneyTaskUpdateOnce(context.Background(), nil)

	require.ErrorIs(t, err, injectedErr)
	assert.Zero(t, summary.UnfinishedTasks)
}

func TestMidjourneyTaskMayHaveChargeRejectsLegacyUpstreamFailures(t *testing.T) {
	assert.False(t, midjourneyTaskMayHaveCharge(&model.Midjourney{Code: 4, Quota: 30}))
	assert.True(t, midjourneyTaskMayHaveCharge(&model.Midjourney{Code: 1, Quota: 30}))
	assert.True(t, midjourneyTaskMayHaveCharge(&model.Midjourney{Code: 4, Quota: 30, BillingRequestId: "request-id"}))
	assert.False(t, midjourneyTaskMayHaveCharge(&model.Midjourney{Code: 1, Action: constant.MjActionInPaint, Quota: 30}))
	assert.False(t, midjourneyTaskMayHaveCharge(&model.Midjourney{Code: 1, Action: constant.MjActionCustomZoom, Quota: 30}))
}
