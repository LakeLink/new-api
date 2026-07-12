package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetLogExportTables(t *testing.T) {
	t.Helper()
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)
}

func seedLogExportLog(t *testing.T, log Log) Log {
	t.Helper()
	require.NoError(t, LOG_DB.Create(&log).Error)
	return log
}

func TestStreamExportAllLogsWithOptionsHonorsLimitAndFillsChannelNames(t *testing.T) {
	resetLogExportTables(t)

	require.NoError(t, DB.Create(&Channel{Id: 11, Name: "primary-openai", Key: "sk-openai"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 12, Name: "backup-claude", Key: "sk-claude"}).Error)
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1001, Type: LogTypeConsume, ModelName: "gpt-4o", ChannelId: 11, RequestId: "req_1"})
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1002, Type: LogTypeConsume, ModelName: "claude", ChannelId: 12, RequestId: "req_2"})
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1003, Type: LogTypeConsume, ModelName: "gemini", ChannelId: 0, RequestId: "req_3"})

	readyCalls := 0
	var requestIds []string
	var channelNames []string
	err := StreamExportAllLogsWithOptions(
		LogQueryOptions{Num: 2, IncludeAdminFields: true},
		func() error {
			readyCalls++
			return nil
		},
		func(log *Log) error {
			requestIds = append(requestIds, log.RequestId)
			channelNames = append(channelNames, log.ChannelName)
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, 1, readyCalls)
	assert.Equal(t, []string{"req_3", "req_2"}, requestIds)
	assert.Equal(t, []string{"", "backup-claude"}, channelNames)
}

func TestStreamExportAllLogsWithOptionsNoLimitStreamsEveryMatch(t *testing.T) {
	resetLogExportTables(t)
	originalBatchSize := logExportBatchSize
	logExportBatchSize = 1
	t.Cleanup(func() { logExportBatchSize = originalBatchSize })

	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1001, Type: LogTypeConsume, RequestId: "req_1"})
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1002, Type: LogTypeConsume, RequestId: "req_2"})
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1003, Type: LogTypeError, RequestId: "req_3"})

	var requestIds []string
	err := StreamExportAllLogsWithOptions(
		LogQueryOptions{Num: 1, NoLimit: true, LogType: LogTypeConsume, IncludeAdminFields: true},
		nil,
		func(log *Log) error {
			requestIds = append(requestIds, log.RequestId)
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"req_2", "req_1"}, requestIds)
}

func TestStreamExportAllLogsWithOptionsKeysetPaginationExcludesNewerRows(t *testing.T) {
	resetLogExportTables(t)
	originalBatchSize := logExportBatchSize
	logExportBatchSize = 2
	t.Cleanup(func() { logExportBatchSize = originalBatchSize })

	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1001, Type: LogTypeConsume, RequestId: "req_1"})
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1002, Type: LogTypeConsume, RequestId: "req_2"})
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1003, Type: LogTypeConsume, RequestId: "req_3"})
	seedLogExportLog(t, Log{UserId: 1, CreatedAt: 1004, Type: LogTypeConsume, RequestId: "req_4"})

	var requestIds []string
	err := StreamExportAllLogsWithOptions(
		LogQueryOptions{NoLimit: true, IncludeAdminFields: true},
		nil,
		func(log *Log) error {
			requestIds = append(requestIds, log.RequestId)
			if len(requestIds) == 2 {
				seedLogExportLog(t, Log{UserId: 1, CreatedAt: 2000, Type: LogTypeConsume, RequestId: "req_new"})
			}
			return nil
		},
	)

	require.NoError(t, err)
	assert.Equal(t, []string{"req_4", "req_3", "req_2", "req_1"}, requestIds)
}

func TestStreamExportUserLogsWithOptionsScrubsAdminFieldsAndUsesDisplayIds(t *testing.T) {
	resetLogExportTables(t)

	seedLogExportLog(t, Log{
		UserId:    42,
		CreatedAt: 1001,
		Type:      LogTypeConsume,
		ChannelId: 11,
		RequestId: "req_old",
		Other:     `{"admin_info":{"node":"secret"},"stream_status":"debug","safe":"kept"}`,
	})
	seedLogExportLog(t, Log{
		UserId:    7,
		CreatedAt: 1002,
		Type:      LogTypeConsume,
		RequestId: "req_other_user",
	})
	seedLogExportLog(t, Log{
		UserId:    42,
		CreatedAt: 1003,
		Type:      LogTypeConsume,
		ChannelId: 12,
		RequestId: "req_new",
		Other:     `{"safe":"new"}`,
	})

	var logs []*Log
	err := StreamExportUserLogsWithOptions(
		LogQueryOptions{UserId: 42, Num: 1, NoLimit: true},
		nil,
		func(log *Log) error {
			logs = append(logs, log)
			return nil
		},
	)

	require.NoError(t, err)
	require.Len(t, logs, 2)
	assert.Equal(t, []string{"req_new", "req_old"}, []string{logs[0].RequestId, logs[1].RequestId})
	assert.Equal(t, []int{1, 2}, []int{logs[0].Id, logs[1].Id})
	assert.Equal(t, "", logs[0].ChannelName)
	assert.Equal(t, "", logs[1].ChannelName)

	other, err := common.StrToMap(logs[1].Other)
	require.NoError(t, err)
	assert.Equal(t, "kept", other["safe"])
	assert.NotContains(t, other, "admin_info")
	assert.NotContains(t, other, "stream_status")
}
