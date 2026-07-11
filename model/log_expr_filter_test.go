package model

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func useTestLogGroupCol(t *testing.T) {
	t.Helper()
	old := logGroupCol
	logGroupCol = "`group`"
	t.Cleanup(func() {
		logGroupCol = old
	})
}

func useTestLocalTimeZone(t *testing.T) *time.Location {
	t.Helper()
	oldLocal := time.Local
	loc := time.FixedZone("TEST", 8*60*60)
	time.Local = loc
	t.Cleanup(func() {
		time.Local = oldLocal
	})
	return loc
}

func TestBuildLogExprSQLCoversCurrentSearchFields(t *testing.T) {
	useTestLogGroupCol(t)

	where, args, needsJoin, err := buildLogExprSQL(
		`model_name contains "gpt" && token_name == "main" && group == "default" && request_id == "req_1" && type == 2 && created_at >= 1700000000`,
		false,
	)

	require.NoError(t, err)
	assert.False(t, needsJoin)
	assert.Equal(t, []any{"%gpt%", "main", "default", "req_1", 2, 1700000000}, args)

	for _, fragment := range []string{
		"logs.model_name LIKE ? ESCAPE '!'",
		"logs.token_name = ?",
		"logs.`group` = ?",
		"logs.request_id = ?",
		"logs.type = ?",
		"logs.created_at >= ?",
	} {
		assert.Contains(t, where, fragment)
	}
}

func TestBuildLogExprSQLAdminFieldsAndJoin(t *testing.T) {
	where, args, needsJoin, err := buildLogExprSQL(
		`username == "alice" && channel == 12 && channel_name startsWith "primary"`,
		true,
	)

	require.NoError(t, err)
	assert.True(t, needsJoin)
	assert.Equal(t, []any{"alice", 12, "primary%"}, args)
	assert.Contains(t, where, "logs.username = ?")
	assert.Contains(t, where, "logs.channel_id = ?")
	assert.Contains(t, where, "channels.name LIKE ? ESCAPE '!'")

	_, _, _, err = buildLogExprSQL(`username == "alice"`, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `不支持的日志字段 "username"`)
}

func TestApplyLogExprFilterRejectsChannelNameWithSeparateLogDB(t *testing.T) {
	oldDB := DB
	oldLogDB := LOG_DB
	DB = nil
	LOG_DB = &gorm.DB{}
	t.Cleanup(func() {
		DB = oldDB
		LOG_DB = oldLogDB
	})

	_, err := applyLogExprFilter(nil, `channel_name contains "primary"`, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "channel_name")
	assert.Contains(t, err.Error(), "独立日志数据库")
}

func TestBuildLogExprSQLOperators(t *testing.T) {
	where, args, needsJoin, err := buildLogExprSQL(
		`model in ["gpt-4o", "claude"] && !is_stream && channel_id not in [1, 2]`,
		true,
	)

	require.NoError(t, err)
	assert.False(t, needsJoin)
	assert.Contains(t, where, "logs.model_name IN ?")
	assert.Contains(t, where, "NOT (logs.is_stream = ?)")
	assert.Contains(t, where, "NOT (logs.channel_id IN ?)")
	assert.Equal(t, []any{[]any{"gpt-4o", "claude"}, true, []any{1, 2}}, args)
}

func TestBuildLogExprSQLDateLiteralsForTimestampFields(t *testing.T) {
	where, args, needsJoin, err := buildLogExprSQL(
		`created_at >= date("2025-01-01", "2006-01-02", "UTC") && timestamp < date("2025-01-02T00:00:00Z")`,
		false,
	)

	require.NoError(t, err)
	assert.False(t, needsJoin)
	assert.Contains(t, where, "logs.created_at >= ?")
	assert.Contains(t, where, "logs.created_at < ?")
	assert.Equal(t, []any{int64(1735689600), int64(1735776000)}, args)
}

func TestBuildLogExprSQLDateLiteralSupportsTimezone(t *testing.T) {
	where, args, _, err := buildLogExprSQL(
		`date("2025-01-01", "2006-01-02", "Asia/Shanghai") <= created_at`,
		false,
	)

	require.NoError(t, err)
	assert.Equal(t, "logs.created_at >= ?", where)
	assert.Equal(t, []any{int64(1735660800)}, args)
}

func TestBuildLogExprSQLDateLiteralSupportsTimezoneSecondArgument(t *testing.T) {
	where, args, _, err := buildLogExprSQL(
		`date("2025-01-01", "Asia/Shanghai") <= created_at`,
		false,
	)

	require.NoError(t, err)
	assert.Equal(t, "logs.created_at >= ?", where)
	assert.Equal(t, []any{int64(1735660800)}, args)
}

func TestBuildLogExprSQLDateShortcuts(t *testing.T) {
	loc := useTestLocalTimeZone(t)
	fixedNow := time.Date(2025, 1, 2, 12, 30, 0, 0, loc)
	expectedTodayStart, expectedTodayEnd := logExprDayBounds(fixedNow, 0)
	expectedYesterdayStart, expectedYesterdayEnd := logExprDayBounds(fixedNow, -1)

	where, args, needsJoin, err := buildLogExprSQLAt(
		`model_name contains "gpt-5.5" and today and yesterday`,
		false,
		fixedNow,
	)

	require.NoError(t, err)
	assert.False(t, needsJoin)
	assert.Contains(t, where, "logs.model_name LIKE ? ESCAPE '!'")
	assert.Contains(t, where, "logs.created_at >= ? AND logs.created_at < ?")
	assert.Equal(t, "%gpt-5.5%", args[0])
	assert.Equal(t, expectedTodayStart, args[1])
	assert.Equal(t, expectedTodayEnd, args[2])
	assert.Equal(t, expectedYesterdayStart, args[3])
	assert.Equal(t, expectedYesterdayEnd, args[4])
}

func TestBuildLogExprSQLDateVariablesArePrecomputedForWholeExpression(t *testing.T) {
	loc := useTestLocalTimeZone(t)
	fixedNow := time.Date(2025, 1, 2, 12, 30, 0, 0, loc)
	expectedTodayStart, expectedTodayEnd := logExprDayBounds(fixedNow, 0)

	_, args, _, err := buildLogExprSQLAt(`today || today`, false, fixedNow)

	require.NoError(t, err)
	assert.Equal(t, []any{expectedTodayStart, expectedTodayEnd, expectedTodayStart, expectedTodayEnd}, args)
}

func TestLogExprDateShortcutYesterdayUsesLocalDay(t *testing.T) {
	loc := useTestLocalTimeZone(t)
	fixedNow := time.Date(2025, 1, 2, 12, 30, 0, 0, loc)
	shortcut, ok := compileLogExprDateShortcut("yesterday", fixedNow)

	require.True(t, ok)
	assert.Equal(t, "(logs.created_at >= ? AND logs.created_at < ?)", shortcut.sql)
	assert.Equal(t, []any{int64(1735660800), int64(1735747200)}, shortcut.args)
}

func TestBuildLogExprSQLEscapesLikePattern(t *testing.T) {
	_, args, _, err := buildLogExprSQL(`model_name contains "gpt_%!"`, false)

	require.NoError(t, err)
	assert.Equal(t, []any{"%gpt!_!%!!%"}, args)
}

func TestBuildLogExprStringMatchConditionAcrossLogDatabases(t *testing.T) {
	originalDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(originalDatabaseType)
	})

	tests := []struct {
		name          string
		databaseType  common.DatabaseType
		op            string
		negate        bool
		wantCondition string
		wantPattern   string
	}{
		{name: "sqlite contains", databaseType: common.DatabaseTypeSQLite, op: "contains", wantCondition: "logs.model_name LIKE ? ESCAPE '!'", wantPattern: `%gpt!_!%!!\mini%`},
		{name: "mysql starts with", databaseType: common.DatabaseTypeMySQL, op: "startsWith", wantCondition: "logs.model_name LIKE ? ESCAPE '!'", wantPattern: `gpt!_!%!!\mini%`},
		{name: "postgres ends with", databaseType: common.DatabaseTypePostgreSQL, op: "endsWith", wantCondition: "logs.model_name LIKE ? ESCAPE '!'", wantPattern: `%gpt!_!%!!\mini`},
		{name: "postgres negated", databaseType: common.DatabaseTypePostgreSQL, op: "contains", negate: true, wantCondition: "logs.model_name NOT LIKE ? ESCAPE '!'", wantPattern: `%gpt!_!%!!\mini%`},
		{name: "clickhouse contains", databaseType: common.DatabaseTypeClickHouse, op: "contains", wantCondition: "logs.model_name LIKE ?", wantPattern: `%gpt\_\%!\\mini%`},
		{name: "clickhouse starts with", databaseType: common.DatabaseTypeClickHouse, op: "startsWith", wantCondition: "logs.model_name LIKE ?", wantPattern: `gpt\_\%!\\mini%`},
		{name: "clickhouse ends with", databaseType: common.DatabaseTypeClickHouse, op: "endsWith", wantCondition: "logs.model_name LIKE ?", wantPattern: `%gpt\_\%!\\mini`},
		{name: "clickhouse negated", databaseType: common.DatabaseTypeClickHouse, op: "contains", negate: true, wantCondition: "logs.model_name NOT LIKE ?", wantPattern: `%gpt\_\%!\\mini%`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			common.SetLogDatabaseType(test.databaseType)

			condition, pattern := buildLogExprStringMatchCondition("logs.model_name", test.op, `gpt_%!\mini`, test.negate)

			assert.Equal(t, test.wantCondition, condition)
			assert.Equal(t, test.wantPattern, pattern)
		})
	}
}

func TestBuildLogExprSQLUsesClickHouseStringEscaping(t *testing.T) {
	originalDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(originalDatabaseType)
	})
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)

	where, args, _, err := buildLogExprSQL(`model_name contains "gpt_%"`, false)

	require.NoError(t, err)
	assert.Equal(t, "logs.model_name LIKE ?", where)
	assert.Equal(t, []any{`%gpt\_\%%`}, args)
}

func TestBuildLogExprSQLKeepsRawOtherAdminOnly(t *testing.T) {
	_, _, _, err := buildLogExprSQL(`other contains "admin_info"`, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `不支持的日志字段 "other"`)

	where, args, _, err := buildLogExprSQL(`other contains "admin_info"`, true)
	require.NoError(t, err)
	assert.Contains(t, where, "logs.other LIKE ?")
	assert.Equal(t, []any{"%admin!_info%"}, args)
}

func TestBuildLogExprSQLKeepsPersistentIDAdminOnly(t *testing.T) {
	originalDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(originalDatabaseType)
	})
	common.SetLogDatabaseType(common.DatabaseTypeSQLite)

	_, _, _, err := buildLogExprSQL(`id == 123`, false)
	require.Error(t, err)
	assert.Contains(t, err.Error(), `不支持的日志字段 "id"`)

	where, args, _, err := buildLogExprSQL(`id == 123`, true)
	require.NoError(t, err)
	assert.Equal(t, "logs.id = ?", where)
	assert.Equal(t, []any{123}, args)
}

func TestBuildLogExprSQLRejectsClickHouseDisplayOnlyID(t *testing.T) {
	originalDatabaseType := common.LogDatabaseType()
	t.Cleanup(func() {
		common.SetLogDatabaseType(originalDatabaseType)
	})
	common.SetLogDatabaseType(common.DatabaseTypeClickHouse)

	_, _, _, err := buildLogExprSQL(`id == 1`, true)

	require.Error(t, err)
	assert.Contains(t, err.Error(), `不支持的日志字段 "id"`)
}

func TestBuildLogExprSQLRejectsUnsupportedExpressions(t *testing.T) {
	longString := strings.Repeat("x", maxLogExprStringLength+1)
	largeInList := `model_name in [`
	for i := 0; i < maxLogExprInItems+1; i++ {
		if i > 0 {
			largeInList += `,`
		}
		largeInList += `"gpt"`
	}
	largeInList += `]`

	tests := []string{
		`len(model_name) > 0`,
		`model_name + "-x" == "gpt-x"`,
		`model_name matches "gpt.*"`,
		`model_name contains 2`,
		`unknown == "x"`,
		`model_name == "` + longString + `"`,
		`quota > date("2025-01-01", "2006-01-02", "UTC")`,
		`created_at > now()`,
		`created_at > date(20250101)`,
		largeInList,
	}

	for _, expr := range tests {
		t.Run(expr, func(t *testing.T) {
			_, _, _, err := buildLogExprSQL(expr, true)
			require.Error(t, err)
		})
	}
}

func TestBuildLogExprSQLTreatsInjectionLikeTextAsParameter(t *testing.T) {
	where, args, _, err := buildLogExprSQL(`model_name == "x' OR 1=1 --"`, false)

	require.NoError(t, err)
	assert.Equal(t, "logs.model_name = ?", where)
	assert.Equal(t, []any{"x' OR 1=1 --"}, args)
}

func TestGetAllLogsWithExprFilterUsesGeneratedSQL(t *testing.T) {
	useTestLogGroupCol(t)
	truncateTables(t)
	require.NoError(t, DB.Exec("DELETE FROM logs").Error)
	require.NoError(t, DB.Exec("DELETE FROM channels").Error)

	require.NoError(t, DB.Create(&Channel{Id: 7, Name: "primary-openai", Key: "sk-test"}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:       1,
		CreatedAt:    1700000001,
		Type:         LogTypeConsume,
		TokenName:    "main",
		ModelName:    "gpt-4o",
		ChannelId:    7,
		Group:        "default",
		RequestId:    "req_match",
		IsStream:     true,
		PromptTokens: 10,
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:    1,
		CreatedAt: 1700000002,
		Type:      LogTypeConsume,
		TokenName: "main",
		ModelName: "claude-3",
		ChannelId: 7,
		Group:     "default",
		RequestId: "req_other",
	}).Error)

	logs, total, err := GetAllLogsWithOptions(LogQueryOptions{
		StartIdx:           0,
		Num:                10,
		Expr:               `model_name contains "gpt" && channel_name startsWith "primary" && is_stream`,
		IncludeAdminFields: true,
	})

	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Len(t, logs, 1)
	assert.Equal(t, "req_match", logs[0].RequestId)
	assert.True(t, strings.HasPrefix(logs[0].ChannelName, "primary"))
}

func TestLogQueriesHonorCanceledContext(t *testing.T) {
	truncateTables(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := GetAllLogsWithOptions(LogQueryOptions{Context: ctx, Num: 10, IncludeAdminFields: true})
	require.ErrorIs(t, err, context.Canceled)

	_, _, err = GetUserLogsWithOptions(LogQueryOptions{Context: ctx, UserId: 1, Num: 10})
	require.ErrorIs(t, err, context.Canceled)

	_, err = SumUsedQuotaWithOptions(LogQueryOptions{Context: ctx, IncludeAdminFields: true})
	require.ErrorIs(t, err, context.Canceled)

	readyCalled := false
	err = StreamExportAllLogsWithOptions(
		LogQueryOptions{Context: ctx, Num: 10, IncludeAdminFields: true},
		func() error {
			readyCalled = true
			return nil
		},
		func(log *Log) error { return nil },
	)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, readyCalled)

	readyCalled = false
	err = StreamExportUserLogsWithOptions(
		LogQueryOptions{Context: ctx, UserId: 1, Num: 10},
		func() error {
			readyCalled = true
			return nil
		},
		func(log *Log) error { return nil },
	)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, readyCalled)
}

func TestSumUsedQuotaWithOptionsAnchorsSelfStatsByUserID(t *testing.T) {
	useTestLogGroupCol(t)
	truncateTables(t)
	require.NoError(t, LOG_DB.Create(&Log{UserId: 42, Username: "shared", CreatedAt: time.Now().Unix(), Type: LogTypeConsume, Quota: 100}).Error)
	require.NoError(t, LOG_DB.Create(&Log{UserId: 7, Username: "shared", CreatedAt: time.Now().Unix(), Type: LogTypeConsume, Quota: 900}).Error)

	stat, err := SumUsedQuotaWithOptions(LogQueryOptions{UserId: 42})

	require.NoError(t, err)
	assert.Equal(t, 100, stat.Quota)
	assert.Equal(t, 1, stat.Rpm)
}

func TestSelfLogExpressionORCannotEscapeUserScope(t *testing.T) {
	useTestLogGroupCol(t)
	truncateTables(t)
	now := time.Now().Unix()
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:    42,
		CreatedAt: now,
		Type:      LogTypeConsume,
		ModelName: "own",
		Quota:     100,
		RequestId: "req_own",
		TokenName: "self-token",
	}).Error)
	require.NoError(t, LOG_DB.Create(&Log{
		UserId:    7,
		CreatedAt: now,
		Type:      LogTypeConsume,
		ModelName: "other",
		Quota:     900,
		RequestId: "req_other",
		TokenName: "other-token",
	}).Error)
	expr := `model_name == "own" || quota == 900`

	logs, total, err := GetUserLogsWithOptions(LogQueryOptions{
		UserId: 42,
		Num:    10,
		Expr:   expr,
	})
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	require.Len(t, logs, 1)
	assert.Equal(t, "req_own", logs[0].RequestId)

	stat, err := SumUsedQuotaWithOptions(LogQueryOptions{
		UserId: 42,
		Expr:   expr,
	})
	require.NoError(t, err)
	assert.Equal(t, 100, stat.Quota)
	assert.Equal(t, 1, stat.Rpm)
}

func TestLogPerformanceIndexesExist(t *testing.T) {
	require.True(t, LOG_DB.Migrator().HasIndex(&Log{}, "idx_user_created_at_id"))
	require.True(t, LOG_DB.Migrator().HasIndex(&Log{}, "idx_type_created_at_id"))
}
