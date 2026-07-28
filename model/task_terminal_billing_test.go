package model

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func terminalRefundPayload(requestID string, userID int, quota int) TaskBillingFinalizationPayload {
	return TaskBillingFinalizationPayload{
		Adjustment: BillingAdjustment{
			RequestID:     requestID,
			Kind:          BillingAdjustmentRefund,
			FundingSource: BillingAdjustmentWallet,
			UserID:        userID,
			FundingDelta:  -quota,
		},
		Log: TaskBillingFinalizationLog{
			UserID:  userID,
			LogType: LogTypeRefund,
			Quota:   quota,
		},
	}
}

func TestTerminalTaskTransitionRollsBackWhenFinalizationConflicts(t *testing.T) {
	truncateTables(t)

	user := User{Id: 301, Username: "terminal-rollback-user", Password: "password", AffCode: "terminal-rollback-aff"}
	require.NoError(t, DB.Create(&user).Error)
	task := &Task{
		TaskID:   "task-terminal-rollback",
		UserId:   301,
		Quota:    100,
		Status:   TaskStatusInProgress,
		Progress: "50%",
		Data:     json.RawMessage(`{}`),
	}
	insertTask(t, task)

	canonical := terminalRefundPayload("terminal-rollback", task.UserId, 100)
	_, err := EnqueueTaskBillingFinalization(canonical)
	require.NoError(t, err)

	conflicting := terminalRefundPayload("terminal-rollback", task.UserId, 99)
	task.Status = TaskStatusFailure
	task.Progress = "100%"
	won, finalizationID, err := task.UpdateWithStatusAndBillingFinalization(TaskStatusInProgress, &conflicting)
	require.ErrorContains(t, err, "different accounting payload")
	assert.False(t, won)
	assert.Empty(t, finalizationID)

	var persisted Task
	require.NoError(t, DB.First(&persisted, task.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusInProgress), persisted.Status)
	assert.Equal(t, "50%", persisted.Progress)
	var count int64
	require.NoError(t, DB.Model(&TaskBillingFinalization{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTaskBillingFinalizationReplayKeepsCanonicalElapsedTime(t *testing.T) {
	truncateTables(t)

	user := User{Id: 303, Username: "terminal-replay-user", Password: "password", AffCode: "terminal-replay-aff"}
	require.NoError(t, DB.Create(&user).Error)
	canonical := terminalRefundPayload("terminal-elapsed-replay", 303, 100)
	canonical.Log.UseTime = 1
	finalizationID, err := EnqueueTaskBillingFinalization(canonical)
	require.NoError(t, err)

	retry := canonical
	retry.Log.UseTime = 30
	retryID, err := EnqueueTaskBillingFinalization(retry)
	require.NoError(t, err)
	assert.Equal(t, finalizationID, retryID)

	_, persisted, err := loadTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	assert.Equal(t, 1, persisted.Log.UseTime, "the first durable payload remains the canonical log snapshot")
}

func TestCleanupTaskBillingFinalizationsPreservesAggregateIdempotency(t *testing.T) {
	truncateTables(t)

	user := User{
		Username: "finalization-cleanup-user",
		Password: "password",
		Quota:    1_000,
		AffCode:  "finalization-cleanup-aff",
	}
	require.NoError(t, DB.Create(&user).Error)
	payload := TaskBillingFinalizationPayload{
		Adjustment: BillingAdjustment{
			RequestID:     "finalization-cleanup-request",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
		},
		UserUsedQuotaDelta:        10,
		IncrementUserRequestCount: true,
		ChannelUsedQuotaDelta:     10,
		Log: TaskBillingFinalizationLog{
			UserID:    user.Id,
			LogType:   LogTypeConsume,
			ModelName: "finalization-cleanup-model",
			Quota:     10,
		},
	}
	finalizationID, err := EnqueueTaskBillingFinalization(payload)
	require.NoError(t, err)
	_, err = ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	require.NoError(t, DB.Model(&TaskBillingFinalization{}).
		Where("finalization_id = ?", finalizationID).
		Update("updated_at", GetDBTimestamp()-1_000).Error)

	deleted, err := CleanupTaskBillingFinalizations(100)
	require.NoError(t, err)
	assert.Zero(t, deleted)

	replayedID, err := EnqueueTaskBillingFinalization(payload)
	require.NoError(t, err)
	assert.Equal(t, finalizationID, replayedID)
	_, err = ProcessTaskBillingFinalization(replayedID)
	require.NoError(t, err)

	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, 10, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	var logCount int64
	require.NoError(t, DB.Model(&Log{}).Where("billing_event_id = ?", finalizationID).Count(&logCount).Error)
	assert.Equal(t, int64(1), logCount)
}

func TestTaskBillingFinalizationRepairsCorruptedAggregateCounters(t *testing.T) {
	tests := []struct {
		name         string
		usedQuota    int
		requestCount int
	}{
		{name: "negative user used quota", usedQuota: -1},
		{name: "negative user request count", requestCount: -1},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncateTables(t)
			user := User{
				Username:     test.name,
				Password:     "password",
				Quota:        100,
				UsedQuota:    test.usedQuota,
				RequestCount: test.requestCount,
				AffCode:      fmt.Sprintf("corrupt-user-%d", index),
			}
			require.NoError(t, DB.Create(&user).Error)
			payload := TaskBillingFinalizationPayload{
				Adjustment: BillingAdjustment{
					RequestID:     fmt.Sprintf("corrupt-user-counter-%d", index),
					Kind:          BillingAdjustmentSettle,
					FundingSource: BillingAdjustmentWallet,
					UserID:        user.Id,
				},
				IncrementUserRequestCount: true,
				Log: TaskBillingFinalizationLog{
					UserID:  user.Id,
					LogType: LogTypeConsume,
				},
			}
			finalizationID, err := EnqueueTaskBillingFinalization(payload)
			require.NoError(t, err)

			_, err = ProcessTaskBillingFinalization(finalizationID)
			require.NoError(t, err)
			require.NoError(t, DB.First(&user, user.Id).Error)
			assert.Zero(t, user.UsedQuota)
			assert.Equal(t, 1, user.RequestCount)
			var logCount int64
			require.NoError(t, DB.Model(&Log{}).Count(&logCount).Error)
			assert.EqualValues(t, 1, logCount)
		})
	}
}

func TestTaskBillingFinalizationRepairsNegativeChannelUsedQuota(t *testing.T) {
	truncateTables(t)

	user := User{Username: "corrupt-channel", Password: "password", Quota: 100, AffCode: "corrupt-channel"}
	require.NoError(t, DB.Create(&user).Error)
	channel := Channel{Name: "corrupt-channel", Key: "key", UsedQuota: -1}
	require.NoError(t, DB.Create(&channel).Error)
	payload := TaskBillingFinalizationPayload{
		Adjustment: BillingAdjustment{
			RequestID:     "corrupt-channel-counter",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
		},
		UserUsedQuotaDelta:        1,
		IncrementUserRequestCount: true,
		ChannelUsedQuotaDelta:     1,
		Log: TaskBillingFinalizationLog{
			UserID:    user.Id,
			LogType:   LogTypeConsume,
			ChannelID: channel.Id,
			Quota:     1,
		},
	}
	finalizationID, err := EnqueueTaskBillingFinalization(payload)
	require.NoError(t, err)

	_, err = ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)
	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, 1, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	require.NoError(t, DB.First(&channel, channel.Id).Error)
	assert.EqualValues(t, 1, channel.UsedQuota)
	var logCount int64
	require.NoError(t, DB.Model(&Log{}).Count(&logCount).Error)
	assert.EqualValues(t, 1, logCount)
}

func TestTaskBillingFinalizationDoesNotUpdateReplacementChannel(t *testing.T) {
	truncateTables(t)

	user := User{Username: "replacement-channel-user", Password: "password", Quota: 100, AffCode: "replacement-channel-aff"}
	require.NoError(t, DB.Create(&user).Error)
	originalChannel := Channel{
		Name:        "original-channel",
		Key:         "original-channel-key",
		CreatedTime: 100,
	}
	require.NoError(t, DB.Create(&originalChannel).Error)
	payload := TaskBillingFinalizationPayload{
		Adjustment: BillingAdjustment{
			RequestID:     "replacement-channel-finalization",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
		},
		UserUsedQuotaDelta:        10,
		IncrementUserRequestCount: true,
		ChannelUsedQuotaDelta:     10,
		ChannelCreatedTime:        originalChannel.CreatedTime,
		Log: TaskBillingFinalizationLog{
			UserID:    user.Id,
			LogType:   LogTypeConsume,
			ChannelID: originalChannel.Id,
			Quota:     10,
		},
	}
	finalizationID, err := EnqueueTaskBillingFinalization(payload)
	require.NoError(t, err)

	require.NoError(t, DB.Delete(&originalChannel).Error)
	replacementChannel := Channel{
		Id:          originalChannel.Id,
		Name:        "replacement-channel",
		Key:         "replacement-channel-key",
		CreatedTime: 200,
		UsedQuota:   7,
	}
	require.NoError(t, DB.Create(&replacementChannel).Error)

	_, err = ProcessTaskBillingFinalization(finalizationID)
	require.NoError(t, err)

	require.NoError(t, DB.First(&user, user.Id).Error)
	assert.Equal(t, 10, user.UsedQuota)
	assert.Equal(t, 1, user.RequestCount)
	require.NoError(t, DB.First(&replacementChannel, replacementChannel.Id).Error)
	assert.EqualValues(t, 7, replacementChannel.UsedQuota)
}

func TestTerminalTaskTransitionCASLossDoesNotCreateFinalization(t *testing.T) {
	truncateTables(t)

	task := &Task{
		TaskID:   "task-terminal-cas-loss",
		UserId:   302,
		Quota:    100,
		Status:   TaskStatusFailure,
		Progress: "100%",
		Data:     json.RawMessage(`{}`),
	}
	insertTask(t, task)

	payload := terminalRefundPayload("terminal-cas-loss", task.UserId, task.Quota)
	task.Status = TaskStatusSuccess
	won, finalizationID, err := task.UpdateWithStatusAndBillingFinalization(TaskStatusInProgress, &payload)
	require.NoError(t, err)
	assert.False(t, won)
	assert.Empty(t, finalizationID)

	var count int64
	require.NoError(t, DB.Model(&TaskBillingFinalization{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestAcceptedTaskSubmissionPersistenceIsIdempotent(t *testing.T) {
	truncateTables(t)

	user := User{Username: "accepted-task-idempotent", Password: "password", AffCode: "accepted-task-idempotent-aff"}
	require.NoError(t, DB.Create(&user).Error)
	channel := Channel{Name: "accepted-task-idempotent", Key: "key", CreatedTime: 1234}
	require.NoError(t, DB.Create(&channel).Error)
	task := &Task{
		TaskID:    "task-accepted-idempotent",
		UserId:    user.Id,
		Group:     "default",
		ChannelId: channel.Id,
		Quota:     20,
		Action:    "generate",
		Status:    TaskStatusNotStart,
		Progress:  "0%",
		Data:      json.RawMessage(`{"accepted":true}`),
		Properties: Properties{
			OriginModelName:   "billing-model",
			UpstreamModelName: "upstream-model",
		},
		PrivateData: TaskPrivateData{
			UpstreamTaskID:     "upstream-accepted-idempotent",
			BillingSource:      BillingAdjustmentWallet,
			BillingRequestId:   "accepted-task-idempotent",
			ChannelCreatedTime: channel.CreatedTime,
		},
	}
	payload := TaskBillingFinalizationPayload{
		Adjustment: BillingAdjustment{
			RequestID:     "accepted-task-idempotent",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
		},
		UserUsedQuotaDelta:        20,
		IncrementUserRequestCount: true,
		ChannelUsedQuotaDelta:     20,
		ChannelCreatedTime:        channel.CreatedTime,
		Log: TaskBillingFinalizationLog{
			UserID:    user.Id,
			LogType:   LogTypeConsume,
			ChannelID: channel.Id,
			ModelName: "billing-model",
			Quota:     20,
		},
	}

	finalizationID, err := InsertTaskWithBillingFinalization(task, payload)
	require.NoError(t, err)
	require.NotEmpty(t, finalizationID)

	retry := *task
	retry.ID = 0
	replayedID, err := InsertTaskWithBillingFinalization(&retry, payload)
	require.NoError(t, err)
	assert.Equal(t, finalizationID, replayedID)
	assert.Equal(t, task.ID, retry.ID)

	var taskCount int64
	require.NoError(t, DB.Model(&Task{}).Where("task_id = ?", task.TaskID).Count(&taskCount).Error)
	assert.Equal(t, int64(1), taskCount)
	var finalizationCount int64
	require.NoError(t, DB.Model(&TaskBillingFinalization{}).
		Where("finalization_id = ?", finalizationID).
		Count(&finalizationCount).Error)
	assert.Equal(t, int64(1), finalizationCount)

	conflicting := retry
	conflicting.ID = 0
	conflicting.PrivateData.UpstreamTaskID = "another-upstream-task"
	_, err = InsertTaskWithBillingFinalization(&conflicting, payload)
	require.ErrorContains(t, err, "different accepted task context")
	require.NoError(t, DB.Model(&Task{}).Where("task_id = ?", task.TaskID).Count(&taskCount).Error)
	assert.Equal(t, int64(1), taskCount)
}

func TestTerminalTaskTransitionRejectsAnotherTaskFinalization(t *testing.T) {
	truncateTables(t)

	user := User{Username: "terminal-task-owner", Password: "password", AffCode: "terminal-task-owner-aff"}
	require.NoError(t, DB.Create(&user).Error)
	first := &Task{TaskID: "terminal-task-first", UserId: user.Id, ChannelId: 1, Status: TaskStatusInProgress, Data: json.RawMessage(`{}`)}
	second := &Task{TaskID: "terminal-task-second", UserId: user.Id, ChannelId: 1, Status: TaskStatusInProgress, Data: json.RawMessage(`{}`)}
	insertTask(t, first)
	insertTask(t, second)

	payload := terminalRefundPayload("terminal-cross-task", user.Id, 10)
	payload.Adjustment.Kind = BillingAdjustmentSettle
	payload.Adjustment.FundingDelta = 10
	payload.TaskDatabaseID = second.ID
	payload.UpdateTaskQuota = true
	payload.TargetTaskQuota = 20
	payload.Log.LogType = LogTypeConsume
	payload.Log.ChannelID = first.ChannelId
	first.Status = TaskStatusSuccess

	won, finalizationID, err := first.UpdateWithStatusAndBillingFinalization(TaskStatusInProgress, &payload)

	require.ErrorContains(t, err, "targets another task")
	assert.False(t, won)
	assert.Empty(t, finalizationID)
	var persisted Task
	require.NoError(t, DB.First(&persisted, first.ID).Error)
	assert.Equal(t, TaskStatus(TaskStatusInProgress), persisted.Status)
}
