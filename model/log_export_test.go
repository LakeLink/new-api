package model

import (
	"os"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
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

// Exercise the fork's query/export contract against independently configured
// main and log databases, including SQL dialect quoting and user isolation.
func TestLogExportDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var mainDriver, logDriver gorm.Dialector
			switch dialect {
			case "sqlite":
				mainDriver, logDriver = sqlite.Open(":memory:"), sqlite.Open(":memory:")
			case "mysql":
				if os.Getenv("TEST_MYSQL_DSN") == "" || os.Getenv("TEST_MYSQL_LOG_DSN") == "" {
					t.Skip("separate MySQL test databases not configured")
				}
				mainDriver, logDriver = mysql.Open(os.Getenv("TEST_MYSQL_DSN")), mysql.Open(os.Getenv("TEST_MYSQL_LOG_DSN"))
			case "postgres":
				if os.Getenv("TEST_POSTGRES_DSN") == "" || os.Getenv("TEST_POSTGRES_LOG_DSN") == "" {
					t.Skip("separate PostgreSQL test databases not configured")
				}
				mainDriver, logDriver = postgres.Open(os.Getenv("TEST_POSTGRES_DSN")), postgres.Open(os.Getenv("TEST_POSTGRES_LOG_DSN"))
			}
			mainDB, err := gorm.Open(mainDriver, &gorm.Config{})
			require.NoError(t, err)
			logDB, err := gorm.Open(logDriver, &gorm.Config{})
			require.NoError(t, err)
			mainSQL, err := mainDB.DB()
			require.NoError(t, err)
			logSQL, err := logDB.DB()
			require.NoError(t, err)
			mainSQL.SetMaxOpenConns(1)
			logSQL.SetMaxOpenConns(1)
			previousDB, previousLogDB := DB, LOG_DB
			previousMainType, previousLogType := common.MainDatabaseType(), common.LogDatabaseType()
			previousBatchSize := logExportBatchSize
			DB, LOG_DB = mainDB, logDB
			common.SetDatabaseTypes(common.DatabaseType(dialect), common.DatabaseType(dialect))
			initCol()
			logExportBatchSize = 1
			t.Cleanup(func() {
				logDB.Where("request_id IN ?", []string{"merge-matrix-old", "merge-matrix-new", "merge-matrix-other"}).Delete(&Log{})
				mainDB.Delete(&Channel{}, 900001)
				DB, LOG_DB = previousDB, previousLogDB
				common.SetDatabaseTypes(previousMainType, previousLogType)
				initCol()
				logExportBatchSize = previousBatchSize
				require.NoError(t, mainSQL.Close())
				require.NoError(t, logSQL.Close())
			})
			require.NoError(t, mainDB.AutoMigrate(&Channel{}))
			require.NoError(t, logDB.AutoMigrate(&Log{}))
			require.NoError(t, mainDB.Create(&Channel{Id: 900001, Name: "merge-primary", Key: "test-key"}).Error)
			rows := []Log{
				{UserId: 900001, CreatedAt: 1001, Type: LogTypeConsume, Group: "premium", ModelName: "gpt-test", ChannelId: 900001, RequestId: "merge-matrix-old", Other: `{"admin_info":{"node":"private"},"safe":"kept"}`},
				{UserId: 900001, CreatedAt: 1002, Type: LogTypeConsume, Group: "premium", ModelName: "gpt-test", ChannelId: 900001, RequestId: "merge-matrix-new"},
				{UserId: 900002, CreatedAt: 1003, Type: LogTypeConsume, Group: "premium", ModelName: "gpt-test", ChannelId: 900001, RequestId: "merge-matrix-other"},
			}
			require.NoError(t, logDB.Create(&rows).Error)
			var exported []*Log
			require.NoError(t, StreamExportAllLogsWithOptions(LogQueryOptions{Expr: `group == "premium" && model_name contains "gpt" && request_id startsWith "merge-matrix"`, NoLimit: true, IncludeAdminFields: true}, nil, func(log *Log) error { exported = append(exported, log); return nil }))
			require.Len(t, exported, 3)
			assert.Equal(t, "merge-matrix-other", exported[0].RequestId)
			assert.Equal(t, "merge-primary", exported[0].ChannelName)
			exported = nil
			require.NoError(t, StreamExportUserLogsWithOptions(LogQueryOptions{UserId: 900001, Expr: `request_id == "merge-matrix-other" || group == "premium"`, NoLimit: true}, nil, func(log *Log) error { exported = append(exported, log); return nil }))
			require.Len(t, exported, 2, "an OR expression must not bypass the user boundary")
			assert.Equal(t, []string{"merge-matrix-new", "merge-matrix-old"}, []string{exported[0].RequestId, exported[1].RequestId})
			assert.Equal(t, "", exported[1].ChannelName)
			assert.JSONEq(t, `{"safe":"kept"}`, exported[1].Other)
			_, _, err = GetAllLogsWithOptions(LogQueryOptions{Expr: `channel_name == "merge-primary"`, IncludeAdminFields: true, Num: 10})
			require.Error(t, err, "cross-database joins must fail explicitly")
			assert.Contains(t, err.Error(), "channel_name")
		})
	}
}
