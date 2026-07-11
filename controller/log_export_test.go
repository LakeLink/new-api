package controller

import (
	"context"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupLogExportControllerTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	gin.SetMode(gin.TestMode)
	oldDB := model.DB
	oldLogDB := model.LOG_DB
	oldMainDatabaseType := common.MainDatabaseType()
	oldLogDatabaseType := common.LogDatabaseType()
	oldRedisEnabled := common.RedisEnabled
	oldLogExportPermission := common.LogExportPermission

	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.RedisEnabled = false
	common.LogExportPermission = common.RoleAdminUser

	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)

	model.DB = db
	model.LOG_DB = db
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))

	t.Cleanup(func() {
		_ = sqlDB.Close()
		model.DB = oldDB
		model.LOG_DB = oldLogDB
		common.SetDatabaseTypes(oldMainDatabaseType, oldLogDatabaseType)
		common.RedisEnabled = oldRedisEnabled
		common.LogExportPermission = oldLogExportPermission
	})

	return db
}

func newLogExportContext(target string) (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, target, nil)
	ctx.Set("role", common.RoleAdminUser)
	return ctx, recorder
}

func seedControllerLogExportData(t *testing.T, db *gorm.DB) {
	t.Helper()

	require.NoError(t, db.Create(&model.Channel{Id: 21, Name: "primary-openai", Key: "sk-openai"}).Error)
	require.NoError(t, db.Create(&model.Channel{Id: 22, Name: "backup-claude", Key: "sk-claude"}).Error)
	require.NoError(t, db.Create(&model.Log{
		UserId:    42,
		CreatedAt: 1001,
		Type:      model.LogTypeConsume,
		Username:  "alice",
		ModelName: "gpt-4o",
		ChannelId: 21,
		RequestId: "req_1",
		Other:     `{"admin_info":{"node":"hidden"},"stream_status":"debug","safe":"old"}`,
	}).Error)
	require.NoError(t, db.Create(&model.Log{
		UserId:            7,
		CreatedAt:         1002,
		Type:              model.LogTypeConsume,
		Username:          "bob",
		ModelName:         "claude",
		ChannelId:         22,
		RequestId:         "req_2",
		UpstreamRequestId: "up_req_2",
	}).Error)
	require.NoError(t, db.Create(&model.Log{
		UserId:    42,
		CreatedAt: 1003,
		Type:      model.LogTypeError,
		Username:  "alice",
		ModelName: "gemini",
		ChannelId: 0,
		RequestId: "req_3",
		Other:     `{"safe":"new"}`,
	}).Error)
}

func TestGetLogQueryOptionsParsesExportLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		target      string
		wantNum     int
		wantNoLimit bool
	}{
		{
			name:        "all",
			target:      "/api/log/export?limit=%20ALL%20",
			wantNum:     0,
			wantNoLimit: true,
		},
		{
			name:    "explicit limit",
			target:  "/api/log/export?limit=100",
			wantNum: 100,
		},
		{
			name:    "clamps oversized limit",
			target:  "/api/log/export?limit=999999",
			wantNum: model.LogExportLimit,
		},
		{
			name:    "defaults missing limit",
			target:  "/api/log/export",
			wantNum: model.LogExportLimit,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, _ := newLogExportContext(tc.target)

			opts := getLogQueryOptions(ctx)

			assert.Equal(t, tc.wantNum, opts.Num)
			assert.Equal(t, tc.wantNoLimit, opts.NoLimit)
		})
	}
}

func TestGetLogQueryOptionsCarriesRequestContext(t *testing.T) {
	ctx, _ := newLogExportContext("/api/log/export")

	opts := getLogQueryOptions(ctx)

	assert.Equal(t, ctx.Request.Context(), opts.Context)
}

func TestGetLogExprSchemaScopesAdminFieldsByRole(t *testing.T) {
	adminContext, adminRecorder := newLogExportContext("/api/log/expr/schema")
	GetLogExprSchema(adminContext)

	require.Equal(t, http.StatusOK, adminRecorder.Code)
	var adminResponse struct {
		Success bool                `json:"success"`
		Data    model.LogExprSchema `json:"data"`
	}
	require.NoError(t, common.Unmarshal(adminRecorder.Body.Bytes(), &adminResponse))
	require.True(t, adminResponse.Success)
	assert.True(t, logExprSchemaResponseHasField(adminResponse.Data, "other"))

	userContext, userRecorder := newLogExportContext("/api/log/expr/schema")
	userContext.Set("role", common.RoleCommonUser)
	GetLogExprSchema(userContext)

	require.Equal(t, http.StatusOK, userRecorder.Code)
	var userResponse struct {
		Success bool                `json:"success"`
		Data    model.LogExprSchema `json:"data"`
	}
	require.NoError(t, common.Unmarshal(userRecorder.Body.Bytes(), &userResponse))
	require.True(t, userResponse.Success)
	assert.False(t, logExprSchemaResponseHasField(userResponse.Data, "other"))
}

func logExprSchemaResponseHasField(schema model.LogExprSchema, name string) bool {
	for _, field := range schema.Fields {
		for _, alias := range field.Names {
			if alias == name {
				return true
			}
		}
	}
	return false
}

func TestGetAllLogsRejectsNonPositivePageSize(t *testing.T) {
	for _, pageSize := range []string{"0", "-1"} {
		t.Run(pageSize, func(t *testing.T) {
			ctx, recorder := newLogExportContext("/api/log?page_size=" + pageSize)

			GetAllLogs(ctx)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Contains(t, response.Message, "page and page_size must be positive integers")
		})
	}
}

func TestGetUserLogsRejectsNonPositivePage(t *testing.T) {
	for _, page := range []string{"0", "-1"} {
		t.Run(page, func(t *testing.T) {
			ctx, recorder := newLogExportContext("/api/log/self?p=" + page)
			ctx.Set("id", 42)

			GetUserLogs(ctx)

			require.Equal(t, http.StatusOK, recorder.Code)
			var response struct {
				Success bool   `json:"success"`
				Message string `json:"message"`
			}
			require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
			assert.False(t, response.Success)
			assert.Contains(t, response.Message, "page and page_size must be positive integers")
		})
	}
}

func TestGetAllLogsRejectsPageOffsetOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	ctx, recorder := newLogExportContext(fmt.Sprintf("/api/log?p=%d&page_size=100", maxInt))

	GetAllLogs(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "page offset is too large")
}

func TestExportAllLogsStreamsJSONLWithNoLimit(t *testing.T) {
	db := setupLogExportControllerTestDB(t)
	seedControllerLogExportData(t, db)
	ctx, recorder := newLogExportContext("/api/log/export?format=jsonl&limit=all")

	ExportAllLogs(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Content-Type"), "application/x-ndjson")
	assert.Contains(t, recorder.Header().Get("Content-Disposition"), "call-logs-")

	lines := strings.Split(strings.TrimSpace(recorder.Body.String()), "\n")
	require.Len(t, lines, 3)

	var logs []model.Log
	for _, line := range lines {
		var log model.Log
		require.NoError(t, common.Unmarshal([]byte(line), &log))
		logs = append(logs, log)
	}
	assert.Equal(t, []string{"req_3", "req_2", "req_1"}, []string{logs[0].RequestId, logs[1].RequestId, logs[2].RequestId})
	assert.Equal(t, []string{"", "backup-claude", "primary-openai"}, []string{logs[0].ChannelName, logs[1].ChannelName, logs[2].ChannelName})
}

func TestExportAllLogsStreamsCSVWithSelectedLimit(t *testing.T) {
	db := setupLogExportControllerTestDB(t)
	seedControllerLogExportData(t, db)
	ctx, recorder := newLogExportContext("/api/log/export?format=csv&limit=2")

	ExportAllLogs(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Content-Type"), "text/csv")

	rows, err := csv.NewReader(strings.NewReader(recorder.Body.String())).ReadAll()
	require.NoError(t, err)
	require.Len(t, rows, 3)
	rows[0][0] = strings.TrimPrefix(rows[0][0], "\ufeff")
	assert.Equal(t, "id", rows[0][0])
	assert.Equal(t, "request_id", rows[0][17])
	assert.Equal(t, "upstream_request_id", rows[0][len(rows[0])-1])
	assert.Equal(t, "req_3", rows[1][17])
	assert.Equal(t, "req_2", rows[2][17])
	assert.Equal(t, "backup-claude", rows[2][13])
	assert.Equal(t, "up_req_2", rows[2][len(rows[2])-1])
}

func TestExportUserLogsStreamsJSONAndScrubsAdminFields(t *testing.T) {
	db := setupLogExportControllerTestDB(t)
	seedControllerLogExportData(t, db)
	ctx, recorder := newLogExportContext("/api/log/self/export?format=json&limit=all")
	ctx.Set("id", 42)

	ExportUserLogs(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Content-Type"), "application/json")

	var logs []model.Log
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &logs))
	require.Len(t, logs, 2)
	assert.Equal(t, []string{"req_3", "req_1"}, []string{logs[0].RequestId, logs[1].RequestId})
	assert.Equal(t, []int{1, 2}, []int{logs[0].Id, logs[1].Id})
	assert.Equal(t, "", logs[0].ChannelName)
	assert.Equal(t, "", logs[1].ChannelName)

	other, err := common.StrToMap(logs[1].Other)
	require.NoError(t, err)
	assert.Equal(t, "old", other["safe"])
	assert.NotContains(t, other, "admin_info")
	assert.NotContains(t, other, "stream_status")
}

func TestExportAllLogsRejectsUnsupportedFormatBeforeStreaming(t *testing.T) {
	setupLogExportControllerTestDB(t)
	ctx, recorder := newLogExportContext("/api/log/export?format=xml")

	ExportAllLogs(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	require.NoError(t, common.Unmarshal(recorder.Body.Bytes(), &response))
	assert.False(t, response.Success)
	assert.Contains(t, response.Message, "unsupported export format")
}

func TestExportAllLogsTreatsRequestCancellationAsNormalTermination(t *testing.T) {
	setupLogExportControllerTestDB(t)
	ctx, recorder := newLogExportContext("/api/log/export?format=jsonl&limit=all")
	requestContext, cancel := context.WithCancel(ctx.Request.Context())
	cancel()
	ctx.Request = ctx.Request.WithContext(requestContext)

	ExportAllLogs(ctx)

	assert.Empty(t, recorder.Body.String())
}
