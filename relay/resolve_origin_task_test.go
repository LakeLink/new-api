package relay

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupResolveOriginTaskTest(t *testing.T) *gorm.DB {
	t.Helper()

	previousDB := model.DB
	previousMainDatabaseType := common.MainDatabaseType()
	previousLogDatabaseType := common.LogDatabaseType()
	previousMemoryCacheEnabled := common.MemoryCacheEnabled

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	require.NoError(t, db.AutoMigrate(&model.Task{}, &model.Channel{}))
	model.DB = db
	common.SetDatabaseTypes(common.DatabaseTypeSQLite, common.DatabaseTypeSQLite)
	common.MemoryCacheEnabled = false
	t.Cleanup(func() {
		model.DB = previousDB
		common.SetDatabaseTypes(previousMainDatabaseType, previousLogDatabaseType)
		common.MemoryCacheEnabled = previousMemoryCacheEnabled
		require.NoError(t, sqlDB.Close())
	})
	return db
}

func seedRemixOriginTask(t *testing.T, db *gorm.DB) (*model.Task, *model.Channel) {
	t.Helper()

	baseURL := "https://accepted-sora.example"
	channel := &model.Channel{
		Id:          702,
		Type:        constant.ChannelTypeSora,
		Name:        "origin-sora",
		Key:         "account-a\naccount-b",
		BaseURL:     &baseURL,
		Status:      common.ChannelStatusEnabled,
		CreatedTime: 12345,
		ChannelInfo: model.ChannelInfo{
			IsMultiKey:   true,
			MultiKeySize: 2,
		},
	}
	require.NoError(t, db.Create(channel).Error)

	task := &model.Task{
		TaskID:    "task_origin_for_remix",
		UserId:    701,
		ChannelId: channel.Id,
		Status:    model.TaskStatusSuccess,
		Properties: model.Properties{
			OriginModelName:   "sora-2-pro",
			UpstreamModelName: "sora-2-pro-upstream",
		},
		PrivateData: model.TaskPrivateData{
			Key:                    "account-b",
			ChannelType:            channel.Type,
			ChannelBaseURL:         channel.GetBaseURL(),
			RoutingSnapshotVersion: 1,
			UpstreamTaskID:         "provider_origin_for_remix",
			ChannelCreatedTime:     channel.CreatedTime,
		},
	}
	require.NoError(t, db.Create(task).Error)
	return task, channel
}

func remixOriginContext(taskID string) *gin.Context {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos/"+taskID+"/remix", nil)
	ctx.Params = gin.Params{{Key: "video_id", Value: taskID}}
	return ctx
}

func TestResolveOriginTaskPinsAcceptedProviderAccount(t *testing.T) {
	db := setupResolveOriginTaskTest(t)
	task, channel := seedRemixOriginTask(t, db)
	ctx := remixOriginContext(task.TaskID)
	common.SetContextKey(ctx, constant.ContextKeyChannelId, 999)
	common.SetContextKey(ctx, constant.ContextKeyChannelKey, "unrelated-distributor-key")
	info := &relaycommon.RelayInfo{
		UserId:          task.UserId,
		OriginModelName: task.Properties.OriginModelName,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}

	taskErr := ResolveOriginTask(ctx, info)

	require.Nil(t, taskErr)
	assert.Equal(t, constant.TaskActionRemix, info.Action)
	assert.Equal(t, task.GetUpstreamTaskID(), info.OriginTaskID)
	assert.Equal(t, channel, info.LockedChannel)
	assert.True(t, info.LockedChannelKeyPinned)
	assert.Equal(t, "account-b", info.LockedChannelKey)
	assert.Equal(t, 1, info.LockedChannelKeyIndex)
	assert.Equal(t, task.Properties.UpstreamModelName, info.OriginTaskUpstreamModelName)
	assert.Equal(t, 999, common.GetContextKeyInt(ctx, constant.ContextKeyChannelId),
		"channel middleware must initialize the complete locked-channel context")
	assert.Equal(t, "unrelated-distributor-key", common.GetContextKeyString(ctx, constant.ContextKeyChannelKey))
}

func TestResolveOriginTaskRejectsDifferentClientModel(t *testing.T) {
	db := setupResolveOriginTaskTest(t)
	task, _ := seedRemixOriginTask(t, db)
	ctx := remixOriginContext(task.TaskID)
	info := &relaycommon.RelayInfo{
		UserId:          task.UserId,
		OriginModelName: "sora-2",
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}

	taskErr := ResolveOriginTask(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, "origin_task_model_mismatch", taskErr.Code)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Nil(t, info.LockedChannel)
}

func TestResolveOriginTaskRejectsReplacedChannel(t *testing.T) {
	db := setupResolveOriginTaskTest(t)
	task, channel := seedRemixOriginTask(t, db)
	require.NoError(t, db.Model(channel).Update("created_time", channel.CreatedTime+1).Error)
	ctx := remixOriginContext(task.TaskID)
	info := &relaycommon.RelayInfo{
		UserId:          task.UserId,
		OriginModelName: task.Properties.OriginModelName,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}

	taskErr := ResolveOriginTask(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, "origin_task_channel_changed", taskErr.Code)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Nil(t, info.LockedChannel)
}

func TestResolveOriginTaskRejectsIncompleteOrigin(t *testing.T) {
	db := setupResolveOriginTaskTest(t)
	task, _ := seedRemixOriginTask(t, db)
	require.NoError(t, db.Model(task).Update("status", model.TaskStatusInProgress).Error)
	ctx := remixOriginContext(task.TaskID)
	info := &relaycommon.RelayInfo{
		UserId:          task.UserId,
		OriginModelName: task.Properties.OriginModelName,
		TaskRelayInfo:   &relaycommon.TaskRelayInfo{},
	}

	taskErr := ResolveOriginTask(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, "origin_task_not_completed", taskErr.Code)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Nil(t, info.LockedChannel)
}
