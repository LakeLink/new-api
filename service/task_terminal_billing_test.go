package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTerminalTaskRefundCanReplayAfterStatusCommit(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID, quota = 401, 402, 403, 250
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "terminal-replay-token", 2_000)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, quota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task-terminal-replay"
	require.NoError(t, model.DB.Create(task).Error)

	fromStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.FailReason = "provider failed"
	payload := taskRefundFinalization(task, task.FailReason)
	require.NotNil(t, payload)
	won, finalizationID, err := task.UpdateWithStatusAndBillingFinalization(fromStatus, payload)
	require.NoError(t, err)
	require.True(t, won)
	require.NotEmpty(t, finalizationID)

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), persisted.Status)
	assert.Equal(t, 10_000, getUserQuota(t, userID), "the outbox commit itself must not mutate balances")
	assert.Equal(t, int64(0), countLogs(t))

	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	assert.Equal(t, 10_000+quota, getUserQuota(t, userID))
	assert.Equal(t, 2_000+quota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestTerminalSubscriptionRefundCanPrecedeInitialSubmissionFinalization(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID, subscriptionID = 441, 442, 443, 444
	const submittedQuota, initialTokenQuota = 120, 5_000
	const subscriptionTotal int64 = 1_000_000
	const requestID = "terminal-before-initial-subscription-finalization"
	seedUser(t, userID, 0)
	seedToken(t, tokenID, userID, "terminal-before-initial-token", initialTokenQuota-submittedQuota)
	seedChannel(t, channelID)
	seedSubscription(t, subscriptionID, userID, subscriptionTotal, submittedQuota)
	require.NoError(t, model.DB.Model(&model.Token{}).Where("id = ?", tokenID).Update("used_quota", submittedQuota).Error)
	require.NoError(t, model.DB.Create(&model.SubscriptionPreConsumeRecord{
		RequestId:          requestID,
		UserId:             userID,
		UserSubscriptionId: subscriptionID,
		PreConsumed:        submittedQuota,
		Status:             "consumed",
	}).Error)
	require.NoError(t, model.DB.Create(&model.BillingReservation{
		RequestID:      requestID,
		UserID:         userID,
		TokenID:        tokenID,
		TokenKeyHash:   model.BillingTokenKeyHash("terminal-before-initial-token"),
		FundingSource:  model.BillingAdjustmentSubscription,
		ModelName:      "test-model",
		RequestedQuota: submittedQuota,
		InitialQuota:   submittedQuota,
		ReservedQuota:  submittedQuota,
		TokenReserved:  submittedQuota,
		SubscriptionID: subscriptionID,
		Status:         "pending",
	}).Error)

	info := &relaycommon.RelayInfo{
		RequestId:                             requestID,
		UserId:                                userID,
		TokenId:                               tokenID,
		TokenKey:                              "terminal-before-initial-token",
		OriginModelName:                       "test-model",
		UsingGroup:                            "default",
		BillingSource:                         BillingSourceSubscription,
		SubscriptionId:                        subscriptionID,
		SubscriptionPreConsumed:               submittedQuota,
		SubscriptionAmountTotal:               subscriptionTotal,
		SubscriptionAmountUsedAfterPreConsume: submittedQuota,
		StartTime:                             time.Now(),
		ChannelMeta:                           &relaycommon.ChannelMeta{ChannelId: channelID},
		TaskRelayInfo:                         &relaycommon.TaskRelayInfo{Action: "generate"},
	}
	info.PriceData.Quota = submittedQuota
	info.Billing = &BillingSession{
		relayInfo: info,
		funding: &SubscriptionFunding{
			requestId:       requestID,
			userId:          userID,
			modelName:       info.OriginModelName,
			amount:          submittedQuota,
			subscriptionId:  subscriptionID,
			preConsumed:     submittedQuota,
			AmountTotal:     subscriptionTotal,
			AmountUsedAfter: submittedQuota,
		},
		preConsumedQuota: submittedQuota,
		tokenConsumed:    submittedQuota,
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/video/generations", nil)
	c.Set(common.RequestIdKey, requestID)

	task := makeTask(userID, channelID, submittedQuota, tokenID, BillingSourceSubscription, subscriptionID)
	task.TaskID = "terminal-before-initial-task"
	task.PrivateData.BillingRequestId = requestID
	initialPayload, err := BuildTaskSubmissionBillingFinalization(c, info, task, submittedQuota)
	require.NoError(t, err)
	initialFinalizationID, err := model.InsertTaskWithBillingFinalization(task, initialPayload)
	require.NoError(t, err)

	fromStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.FailReason = "provider failed before initial billing replay"
	terminalPayload := taskRefundFinalization(task, task.FailReason)
	require.NotNil(t, terminalPayload)
	won, terminalFinalizationID, err := task.UpdateWithStatusAndBillingFinalization(fromStatus, terminalPayload)
	require.NoError(t, err)
	require.True(t, won)
	_, err = model.ProcessTaskBillingFinalization(terminalFinalizationID)
	require.NoError(t, err)

	var reservation model.SubscriptionPreConsumeRecord
	require.NoError(t, model.DB.Where("request_id = ?", requestID).First(&reservation).Error)
	assert.Equal(t, "refunded", reservation.Status)
	assert.Zero(t, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))

	require.NoError(t, ProcessPersistedBillingFinalization(c, info, submittedQuota, initialFinalizationID))
	require.NoError(t, model.DB.Where("request_id = ?", requestID).First(&reservation).Error)
	assert.Equal(t, "refunded", reservation.Status, "late initial replay must not restore a refunded reservation")
	var billingReservation model.BillingReservation
	require.NoError(t, model.DB.Where("request_id = ?", requestID).First(&billingReservation).Error)
	assert.Equal(t, "refunded", billingReservation.Status, "the original reservation tombstone must close with the refund")
	assert.Zero(t, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
	var token model.Token
	require.NoError(t, model.DB.First(&token, tokenID).Error)
	assert.Zero(t, token.UsedQuota)
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, submittedQuota, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	assert.Equal(t, int64(submittedQuota), channel.UsedQuota)
	assert.Equal(t, int64(2), countLogs(t))

	_, err = model.ProcessTaskBillingFinalization(terminalFinalizationID)
	require.NoError(t, err)
	require.NoError(t, model.ProcessPendingTaskBillingFinalizations(100))
	assert.Zero(t, getSubscriptionUsed(t, subscriptionID))
	assert.Equal(t, initialTokenQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(2), countLogs(t))
}

func TestTaskPollingMissingUpstreamIDFailsAndRefundsAtomically(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID, quota = 411, 412, 413, 300
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "missing-id-token", 2_000)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, quota, tokenID, BillingSourceWallet, 0)
	task.TaskID = ""
	task.Platform = constant.TaskPlatform("video-test")
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	require.NoError(t, model.DB.Create(task).Error)

	oldFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return nil }
	t.Cleanup(func() { GetTaskAdaptorFunc = oldFactory })
	oldTaskQueryLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() { constant.TaskQueryLimit = oldTaskQueryLimit })

	summary, err := RunTaskPollingOnce(context.Background(), nil)
	require.NoError(t, err)
	assert.Equal(t, 1, summary.NullTasksFailed)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), persisted.Status)
	assert.Equal(t, "100%", persisted.Progress)
	assert.NotEmpty(t, persisted.FailReason)
	assert.Equal(t, 10_000+quota, getUserQuota(t, userID))
	assert.Equal(t, 2_000+quota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestTaskPollingMissingChannelLeavesTaskPendingWithoutRefund(t *testing.T) {
	truncate(t)

	const userID, tokenID, missingChannelID, quota = 421, 422, 423, 350
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "missing-channel-token", 2_000)
	task := makeTask(userID, missingChannelID, quota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task-missing-channel"
	task.PrivateData.UpstreamTaskID = "upstream-missing-channel"
	task.Platform = constant.TaskPlatform("video-test")
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	require.NoError(t, model.DB.Create(task).Error)

	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemoryCacheEnabled })

	err := updateVideoTasks(context.Background(), task.Platform, missingChannelID,
		[]string{task.PrivateData.UpstreamTaskID}, map[string]*model.Task{task.PrivateData.UpstreamTaskID: task})
	require.Error(t, err)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
	assert.Equal(t, "50%", persisted.Progress)
	assert.Empty(t, persisted.FailReason)
	assert.Equal(t, 10_000, getUserQuota(t, userID))
	assert.Equal(t, 2_000, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSunoPollingMissingChannelLeavesTaskPendingWithoutRefund(t *testing.T) {
	truncate(t)

	const userID, tokenID, missingChannelID, quota = 424, 425, 426, 350
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "missing-suno-channel-token", 2_000)
	task := makeTask(userID, missingChannelID, quota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task-missing-suno-channel"
	task.PrivateData.UpstreamTaskID = "upstream-missing-suno-channel"
	task.Platform = constant.TaskPlatformSuno
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	require.NoError(t, model.DB.Create(task).Error)

	oldMemoryCacheEnabled := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = false
	t.Cleanup(func() { common.MemoryCacheEnabled = oldMemoryCacheEnabled })

	err := updateSunoTasks(context.Background(), missingChannelID,
		[]string{task.PrivateData.UpstreamTaskID}, map[string]*model.Task{task.PrivateData.UpstreamTaskID: task})
	require.Error(t, err)
	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
	assert.Equal(t, "50%", persisted.Progress)
	assert.Empty(t, persisted.FailReason)
	assert.Equal(t, 10_000, getUserQuota(t, userID))
	assert.Equal(t, 2_000, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestTaskBillingFinalizationCompletesAfterChannelDeletion(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 431, 432, 433
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "deleted-channel-token", 2_000)
	seedChannel(t, channelID)
	task := makeTask(userID, channelID, 100, tokenID, BillingSourceWallet, 0)
	task.TaskID = "task-deleted-channel-finalization"
	require.NoError(t, model.DB.Create(task).Error)
	payload := taskRecalculationFinalization(task, 150, "provider usage")
	require.NotNil(t, payload)

	finalizationID, err := model.EnqueueTaskBillingFinalization(*payload)
	require.NoError(t, err)
	adjustmentID, err := model.EnqueueBillingAdjustment(payload.Adjustment)
	require.NoError(t, err)
	require.Equal(t, finalizationID, adjustmentID)
	_, err = model.ProcessBillingAdjustmentWithResult(adjustmentID)
	require.NoError(t, err)
	require.NoError(t, model.DB.Delete(&model.Channel{}, channelID).Error)

	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	_, err = model.ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	assert.Equal(t, 9_950, getUserQuota(t, userID))
	assert.Equal(t, 1_950, getTokenRemainQuota(t, tokenID))
	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	assert.Equal(t, 50, user.UsedQuota)
	assert.Zero(t, user.RequestCount, "task recalculation must not count a second request")
	var persistedTask model.Task
	require.NoError(t, model.DB.First(&persistedTask, task.ID).Error)
	assert.Equal(t, 150, persistedTask.Quota)
	assert.Equal(t, int64(1), countLogs(t))
}
