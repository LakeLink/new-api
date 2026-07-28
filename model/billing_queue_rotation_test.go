package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPendingBillingAdjustmentsRotateFailedRowsBehindReadyWork(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &SystemTask{})
	user := User{Username: "billing-queue-ready", Password: "password", AffCode: "billing-queue-ready-aff"}
	require.NoError(t, db.Create(&user).Error)
	poison := SystemTask{
		TaskID:    "billing-poison-row",
		Type:      SystemTaskTypeBillingAdjustment,
		Status:    SystemTaskStatusPending,
		Payload:   "{",
		CreatedAt: 1,
		UpdatedAt: 1,
	}
	require.NoError(t, db.Create(&poison).Error)

	readyID, err := EnqueueBillingAdjustment(BillingAdjustment{
		RequestID:     "billing-ready-row",
		Kind:          BillingAdjustmentSettle,
		FundingSource: BillingAdjustmentWallet,
		UserID:        user.Id,
	})
	require.NoError(t, err)

	require.Error(t, ProcessPendingBillingAdjustments(1))
	require.NoError(t, db.First(&poison, poison.ID).Error)
	var ready SystemTask
	require.NoError(t, db.Where("task_id = ?", readyID).First(&ready).Error)
	assert.Equal(t, SystemTaskStatusPending, poison.Status)
	assert.Greater(t, poison.UpdatedAt, ready.UpdatedAt)

	require.NoError(t, ProcessPendingBillingAdjustments(1))
	require.NoError(t, db.Where("task_id = ?", readyID).First(&ready).Error)
	assert.Equal(t, SystemTaskStatusSucceeded, ready.Status)

	require.Error(t, ProcessPendingBillingAdjustments(1))
	require.NoError(t, db.First(&poison, poison.ID).Error)
	assert.Equal(t, SystemTaskStatusPending, poison.Status)
}

func TestPendingTaskBillingFinalizationsRotateFailedRowsBehindReadyWork(t *testing.T) {
	db := setupBillingAdjustmentTestDB(t, &User{}, &SystemTask{}, &Log{})
	user := User{Username: "finalization-queue-ready", Password: "password", AffCode: "finalization-queue-ready-aff"}
	require.NoError(t, db.Create(&user).Error)
	oldDataExportEnabled := common.DataExportEnabled
	common.DataExportEnabled = false
	t.Cleanup(func() {
		common.DataExportEnabled = oldDataExportEnabled
	})

	poison := TaskBillingFinalization{
		FinalizationID: "finalization-poison-row",
		Payload:        "{",
		MainStatus:     taskBillingFinalizationPending,
		LogStatus:      taskBillingFinalizationPending,
	}
	require.NoError(t, db.Create(&poison).Error)
	require.NoError(t, db.Model(&poison).Updates(map[string]interface{}{
		"created_at": 1,
		"updated_at": 1,
	}).Error)

	readyID, err := EnqueueTaskBillingFinalization(TaskBillingFinalizationPayload{
		Adjustment: BillingAdjustment{
			RequestID:     "finalization-ready-row",
			Kind:          BillingAdjustmentSettle,
			FundingSource: BillingAdjustmentWallet,
			UserID:        user.Id,
		},
		Log: TaskBillingFinalizationLog{
			UserID:    user.Id,
			LogType:   LogTypeConsume,
			ModelName: "queue-rotation-model",
			Group:     "default",
			CreatedAt: common.GetTimestamp(),
		},
	})
	require.NoError(t, err)

	require.Error(t, ProcessPendingTaskBillingFinalizations(1))
	require.NoError(t, db.First(&poison, poison.ID).Error)
	var ready TaskBillingFinalization
	require.NoError(t, db.Where("finalization_id = ?", readyID).First(&ready).Error)
	assert.Greater(t, poison.UpdatedAt, ready.UpdatedAt)

	require.NoError(t, ProcessPendingTaskBillingFinalizations(1))
	require.NoError(t, db.Where("finalization_id = ?", readyID).First(&ready).Error)
	assert.Equal(t, taskBillingFinalizationSucceeded, ready.MainStatus)
	assert.Equal(t, taskBillingFinalizationSucceeded, ready.LogStatus)

	require.Error(t, ProcessPendingTaskBillingFinalizations(1))
	require.NoError(t, db.First(&poison, poison.ID).Error)
	assert.Equal(t, taskBillingFinalizationPending, poison.MainStatus)
	assert.Equal(t, taskBillingFinalizationPending, poison.LogStatus)
}
