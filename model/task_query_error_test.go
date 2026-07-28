package model

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestTaskListQueriesPropagateDatabaseErrors(t *testing.T) {
	injectedErr := errors.New("injected task query failure")
	const callbackName = "test:fail_task_list_queries"
	require.NoError(t, DB.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema == nil {
				return
			}
			switch tx.Statement.Schema.Table {
			case "tasks", "midjourneys":
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, DB.Callback().Query().Remove(callbackName))
	})

	tasks, err := TaskGetAllTasks(0, 10, SyncTaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, tasks)

	userTasks, err := TaskGetAllUserTask(1, 0, 10, SyncTaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, userTasks)

	_, err = TaskCountAllTasks(SyncTaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	_, err = TaskCountAllUserTask(1, SyncTaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	timedOutTasks, err := GetTimedOutUnfinishedTasks(1, 10)
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, timedOutTasks)
	unfinishedTasks, err := GetAllUnFinishSyncTasks(10)
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, unfinishedTasks)
	_, err = HasUnfinishedSyncTasks()
	require.ErrorIs(t, err, injectedErr)

	midjourneyTasks, err := GetAllTasks(0, 10, TaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, midjourneyTasks)

	userMidjourneyTasks, err := GetAllUserTask(1, 0, 10, TaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, userMidjourneyTasks)

	_, err = CountAllTasks(TaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	_, err = CountAllUserTask(1, TaskQueryParams{})
	require.ErrorIs(t, err, injectedErr)
	unfinishedMidjourneyTasks, err := GetAllUnFinishTasks()
	require.ErrorIs(t, err, injectedErr)
	assert.Nil(t, unfinishedMidjourneyTasks)
	_, err = HasUnfinishedMidjourneyTasks()
	require.ErrorIs(t, err, injectedErr)
}
