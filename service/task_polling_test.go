package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	taskdto "github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/bytedance/gopkg/util/gopool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type taskPollingFetchAdaptor struct {
	mu           sync.Mutex
	taskIDs      []string
	keys         []string
	fetched      chan string
	blockTaskID  string
	blockStarted chan struct{}
	releaseBlock chan struct{}
	blockOnce    sync.Once
}

type sunoFailurePollingAdaptor struct {
	failReason string
}

func (a *sunoFailurePollingAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *sunoFailurePollingAdaptor) FetchTask(_ context.Context, _ string, _ string, body map[string]any, _ string) (*http.Response, error) {
	taskIDs, _ := body["ids"].([]string)
	items := make([]taskdto.SunoDataResponse, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		items = append(items, taskdto.SunoDataResponse{
			TaskID:     taskID,
			Status:     string(model.TaskStatusFailure),
			FailReason: a.failReason,
			FinishTime: time.Now().Unix(),
		})
	}

	responseBody, err := common.Marshal(taskdto.TaskResponse[[]taskdto.SunoDataResponse]{
		Code: taskdto.TaskSuccessCode,
		Data: items,
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *sunoFailurePollingAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, nil
}

func (a *sunoFailurePollingAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *taskPollingFetchAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *taskPollingFetchAdaptor) FetchTask(_ context.Context, _ string, key string, body map[string]any, _ string) (*http.Response, error) {
	taskID, _ := body["task_id"].(string)
	if taskID == a.blockTaskID && a.releaseBlock != nil {
		a.blockOnce.Do(func() {
			if a.blockStarted != nil {
				close(a.blockStarted)
			}
		})
		<-a.releaseBlock
	}

	a.mu.Lock()
	a.taskIDs = append(a.taskIDs, taskID)
	a.keys = append(a.keys, key)
	a.mu.Unlock()
	if a.fetched != nil {
		select {
		case a.fetched <- taskID:
		default:
		}
	}

	response := taskdto.TaskResponse[model.Task]{
		Code: taskdto.TaskSuccessCode,
		Data: model.Task{
			TaskID:   taskID,
			Status:   model.TaskStatusInProgress,
			Progress: "30%",
		},
	}
	responseBody, err := common.Marshal(response)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *taskPollingFetchAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return &relaycommon.TaskInfo{Status: model.TaskStatusInProgress}, nil
}

func (a *taskPollingFetchAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func (a *taskPollingFetchAdaptor) fetchCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.taskIDs)
}

func (a *taskPollingFetchAdaptor) fetchedTaskIDs() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.taskIDs...)
}

func (a *taskPollingFetchAdaptor) fetchedKeys() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.keys...)
}

type taskPollingResponseAdaptor struct {
	fetchErr     error
	statusCode   int
	responseBody []byte
	fetchedURL   string
	fetchedKey   string
	fetchedProxy string
	parseResult  *relaycommon.TaskInfo
	parseErr     error
	parseCalls   int
}

func (a *taskPollingResponseAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *taskPollingResponseAdaptor) FetchTask(_ context.Context, baseURL string, key string, _ map[string]any, proxy string) (*http.Response, error) {
	a.fetchedURL = baseURL
	a.fetchedKey = key
	a.fetchedProxy = proxy
	if a.fetchErr != nil {
		return nil, a.fetchErr
	}
	statusCode := a.statusCode
	if statusCode == 0 {
		statusCode = http.StatusOK
	}
	return &http.Response{
		StatusCode: statusCode,
		Body:       io.NopCloser(bytes.NewReader(a.responseBody)),
	}, nil
}

func (a *taskPollingResponseAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	a.parseCalls++
	return a.parseResult, a.parseErr
}

func (a *taskPollingResponseAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

type taskPollingRouteRequest struct {
	baseURL string
	key     string
	proxy   string
	taskIDs []string
}

type taskPollingRouteAdaptor struct {
	requests []taskPollingRouteRequest
}

func (a *taskPollingRouteAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *taskPollingRouteAdaptor) FetchTask(_ context.Context, baseURL string, key string, body map[string]any, proxy string) (*http.Response, error) {
	taskIDs, _ := body["ids"].([]string)
	a.requests = append(a.requests, taskPollingRouteRequest{
		baseURL: baseURL,
		key:     key,
		proxy:   proxy,
		taskIDs: append([]string(nil), taskIDs...),
	})
	items := make([]taskdto.SunoDataResponse, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		items = append(items, taskdto.SunoDataResponse{
			TaskID: taskID,
			Status: string(model.TaskStatusInProgress),
		})
	}
	responseBody, err := common.Marshal(taskdto.TaskResponse[[]taskdto.SunoDataResponse]{
		Code: taskdto.TaskSuccessCode,
		Data: items,
	})
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(responseBody)),
	}, nil
}

func (a *taskPollingRouteAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, errors.New("not used by Suno batch polling")
}

func (a *taskPollingRouteAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

type taskPollingCancellationAdaptor struct {
	started chan struct{}
}

func (a *taskPollingCancellationAdaptor) Init(_ *relaycommon.RelayInfo) {}

func (a *taskPollingCancellationAdaptor) FetchTask(ctx context.Context, _ string, _ string, _ map[string]any, _ string) (*http.Response, error) {
	close(a.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

func (a *taskPollingCancellationAdaptor) ParseTaskResult([]byte) (*relaycommon.TaskInfo, error) {
	return nil, errors.New("not reached")
}

func (a *taskPollingCancellationAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
	return 0
}

func seedTaskPollingChannel(t *testing.T, id int, disableSleep bool) {
	t.Helper()
	ch := &model.Channel{
		Id:          id,
		Type:        constant.ChannelTypeKling,
		Name:        "polling_channel",
		Key:         "sk-test",
		Status:      common.ChannelStatusEnabled,
		CreatedTime: int64(id) * 100,
	}
	if disableSleep {
		ch.SetOtherSettings(dto.ChannelOtherSettings{DisableTaskPollingSleep: true})
	}
	require.NoError(t, model.DB.Create(ch).Error)
}

func seedPollingTask(t *testing.T, channelID int, publicID string, upstreamID string) *model.Task {
	t.Helper()
	task := &model.Task{
		TaskID:    publicID,
		Platform:  constant.TaskPlatform("kling"),
		UserId:    1,
		ChannelId: channelID,
		Action:    constant.TaskActionGenerate,
		Status:    model.TaskStatusInProgress,
		Progress:  "30%",
		CreatedAt: time.Now().Unix(),
		UpdatedAt: time.Now().Unix(),
		PrivateData: model.TaskPrivateData{
			UpstreamTaskID: upstreamID,
		},
	}
	require.NoError(t, model.DB.Create(task).Error)
	return task
}

func seedBillablePollingTask(t *testing.T) (*model.Task, *model.Channel) {
	t.Helper()

	const userID, tokenID, channelID, quota = 601, 602, 603, 300
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "polling-retryable-token", 2_000)
	seedTaskPollingChannel(t, channelID, true)

	task := makeTask(userID, channelID, quota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "polling-retryable-task"
	task.Platform = constant.TaskPlatform("kling")
	task.Action = constant.TaskActionGenerate
	task.Progress = "40%"
	task.PrivateData.UpstreamTaskID = "polling-retryable-upstream"
	require.NoError(t, model.DB.Create(task).Error)

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	return task, &channel
}

func TestSweepTimedOutTasksDoesNotRefundPreBillingMigrationTask(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID, quota = 610, 611, 612, 300
	seedUser(t, userID, 10_000)
	seedToken(t, tokenID, userID, "legacy-timeout-token", 2_000)
	seedTaskPollingChannel(t, channelID, true)

	task := makeTask(userID, channelID, quota, tokenID, BillingSourceWallet, 0)
	task.TaskID = "legacy-timeout-before-durable-billing"
	task.SubmitTime = 1748736000 // 2025-06-01 00:00:00 UTC
	require.Less(t, task.SubmitTime, legacyTaskBillingCutoff)
	require.NoError(t, model.DB.Create(task).Error)

	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })

	sweepTimedOutTasks(context.Background())

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), persisted.Status)
	assert.Contains(t, persisted.FailReason, "旧系统遗留任务")
	assert.Equal(t, 10_000, getUserQuota(t, userID))
	assert.Equal(t, 2_000, getTokenRemainQuota(t, tokenID))
	assert.Zero(t, countLogs(t))

	var finalizationCount int64
	require.NoError(t, model.DB.Model(&model.TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Zero(t, finalizationCount)
}

func TestUpdateVideoSingleTaskRetryableErrorsDoNotFailOrRefund(t *testing.T) {
	tests := []struct {
		name           string
		adaptor        *taskPollingResponseAdaptor
		wantParseCalls int
	}{
		{
			name:           "transport error",
			adaptor:        &taskPollingResponseAdaptor{fetchErr: errors.New("temporary network failure")},
			wantParseCalls: 0,
		},
		{
			name: "non-2xx response",
			adaptor: &taskPollingResponseAdaptor{
				statusCode:   http.StatusServiceUnavailable,
				responseBody: []byte(`{"error":{"message":"temporarily unavailable"}}`),
				parseResult:  relaycommon.FailTaskInfo("must not be parsed as terminal"),
			},
			wantParseCalls: 0,
		},
		{
			name: "parse error",
			adaptor: &taskPollingResponseAdaptor{
				responseBody: []byte(`not-json`),
				parseErr:     errors.New("temporary malformed response"),
			},
			wantParseCalls: 1,
		},
		{
			name: "empty status",
			adaptor: &taskPollingResponseAdaptor{
				responseBody: []byte(`{}`),
				parseResult:  &relaycommon.TaskInfo{},
			},
			wantParseCalls: 1,
		},
		{
			name: "unknown status",
			adaptor: &taskPollingResponseAdaptor{
				responseBody: []byte(`{}`),
				parseResult:  &relaycommon.TaskInfo{Status: model.TaskStatusUnknown},
			},
			wantParseCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncate(t)
			task, channel := seedBillablePollingTask(t)

			err := updateVideoSingleTask(context.Background(), test.adaptor, channel, task.GetUpstreamTaskID(), map[string]*model.Task{
				task.GetUpstreamTaskID(): task,
			})
			require.Error(t, err)
			assert.Equal(t, test.wantParseCalls, test.adaptor.parseCalls)

			var persisted model.Task
			require.NoError(t, model.DB.First(&persisted, task.ID).Error)
			assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), persisted.Status)
			assert.Equal(t, "40%", persisted.Progress)
			assert.Empty(t, persisted.FailReason)
			assert.Equal(t, model.TaskStatus(model.TaskStatusInProgress), task.Status)
			assert.Equal(t, 10_000, getUserQuota(t, task.UserId))
			assert.Equal(t, 2_000, getTokenRemainQuota(t, task.PrivateData.TokenId))
			assert.Equal(t, int64(0), countLogs(t))

			var finalizationCount int64
			require.NoError(t, model.DB.Model(&model.TaskBillingFinalization{}).Count(&finalizationCount).Error)
			assert.Zero(t, finalizationCount)
		})
	}
}

func TestUpdateVideoSingleTaskUsesAcceptedRoutingSnapshot(t *testing.T) {
	truncate(t)

	const channelID = 606
	seedTaskPollingChannel(t, channelID, true)
	task := seedPollingTask(t, channelID, "video-route-snapshot", "video-route-upstream")
	task.PrivateData.RoutingSnapshotVersion = 1
	task.PrivateData.Key = "accepted-video-key"
	task.PrivateData.ChannelBaseURL = "https://accepted-video.example"
	task.PrivateData.ChannelProxy = "http://accepted-video-proxy.example"
	require.NoError(t, model.DB.Save(task).Error)

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	adaptor := &taskPollingResponseAdaptor{
		responseBody: []byte(`{}`),
		parseResult: &relaycommon.TaskInfo{
			Status:   model.TaskStatusInProgress,
			Progress: "40%",
		},
	}
	err := updateVideoSingleTask(
		context.Background(),
		adaptor,
		&channel,
		task.GetUpstreamTaskID(),
		map[string]*model.Task{task.GetUpstreamTaskID(): task},
	)
	require.NoError(t, err)
	assert.Equal(t, "https://accepted-video.example", adaptor.fetchedURL)
	assert.Equal(t, "accepted-video-key", adaptor.fetchedKey)
	assert.Equal(t, "http://accepted-video-proxy.example", adaptor.fetchedProxy)
}

func TestUpdateVideoSingleTaskCancelsInFlightProviderFetch(t *testing.T) {
	truncate(t)
	task, channel := seedBillablePollingTask(t)
	adaptor := &taskPollingCancellationAdaptor{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)

	go func() {
		result <- updateVideoSingleTask(ctx, adaptor, channel, task.GetUpstreamTaskID(), map[string]*model.Task{
			task.GetUpstreamTaskID(): task,
		})
	}()

	<-adaptor.started
	cancel()

	err := <-result
	require.ErrorIs(t, err, context.Canceled)
}

func TestApplyTaskPollingResultReloadsWinnerAfterCASLoss(t *testing.T) {
	truncate(t)

	task := seedPollingTask(t, 901, "polling-cas-reload", "polling-cas-upstream")
	stale := *task
	require.NoError(t, model.DB.Model(&model.Task{}).
		Where("id = ?", task.ID).
		Updates(map[string]any{
			"status":      model.TaskStatusFailure,
			"progress":    "100%",
			"fail_reason": "canonical provider failure",
			"data":        []byte(`{"winner":"failure"}`),
		}).Error)

	err := ApplyTaskPollingResult(
		context.Background(),
		&taskPollingResponseAdaptor{},
		&stale,
		&relaycommon.TaskInfo{
			Status:   model.TaskStatusSuccess,
			Progress: "100%",
			Url:      "https://example.invalid/losing-result.mp4",
		},
		[]byte(`{"loser":"success"}`),
	)
	require.NoError(t, err)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), stale.Status)
	assert.Equal(t, "100%", stale.Progress)
	assert.Equal(t, "canonical provider failure", stale.FailReason)
	assert.JSONEq(t, `{"winner":"failure"}`, string(stale.Data))
	assert.Empty(t, stale.PrivateData.ResultURL)

	var finalizationCount int64
	require.NoError(t, model.DB.Model(&model.TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Zero(t, finalizationCount)
}

func TestApplyTaskPollingResultRestoresObjectAfterPersistenceError(t *testing.T) {
	tests := []struct {
		name   string
		result *relaycommon.TaskInfo
	}{
		{
			name: "terminal transition",
			result: &relaycommon.TaskInfo{
				Status:   model.TaskStatusSuccess,
				Progress: taskcommon.ProgressComplete,
				Url:      "https://example.invalid/uncommitted-result.mp4",
			},
		},
		{
			name: "nonterminal update",
			result: &relaycommon.TaskInfo{
				Status:   model.TaskStatusQueued,
				Progress: taskcommon.ProgressQueued,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			truncate(t)
			task := seedPollingTask(t, 902, "polling-write-error", "polling-write-error-upstream")
			task.Data = []byte(`{"persisted":"original"}`)
			task.PrivateData.ResultURL = "https://example.invalid/original.mp4"
			require.NoError(t, model.DB.Save(task).Error)
			original := *task

			callbackName := "test:task-polling-write-error"
			require.NoError(t, model.DB.Callback().Update().Before("gorm:update").Register(
				callbackName,
				func(tx *gorm.DB) {
					tx.AddError(errors.New("injected task update failure"))
				},
			))
			t.Cleanup(func() {
				require.NoError(t, model.DB.Callback().Update().Remove(callbackName))
			})

			err := ApplyTaskPollingResult(
				context.Background(),
				&taskPollingResponseAdaptor{},
				task,
				test.result,
				[]byte(`{"upstream":"not-committed"}`),
			)
			require.ErrorContains(t, err, "injected task update failure")
			assert.Equal(t, original.Status, task.Status)
			assert.Equal(t, original.Progress, task.Progress)
			assert.Equal(t, original.StartTime, task.StartTime)
			assert.Equal(t, original.FinishTime, task.FinishTime)
			assert.Equal(t, original.FailReason, task.FailReason)
			assert.Equal(t, original.PrivateData.ResultURL, task.PrivateData.ResultURL)
			assert.Equal(t, string(original.Data), string(task.Data))
		})
	}
}

func TestApplyTaskPollingResultRejectsUnboundedProviderUsageBeforeTerminalCommit(t *testing.T) {
	truncate(t)
	task, _ := seedBillablePollingTask(t)
	original := *task

	err := ApplyTaskPollingResult(
		context.Background(),
		&taskPollingResponseAdaptor{},
		task,
		&relaycommon.TaskInfo{
			Status:      model.TaskStatusSuccess,
			Progress:    taskcommon.ProgressComplete,
			Url:         "https://example.invalid/result.mp4",
			TotalTokens: common.MaxTokensLimit + 1,
		},
		[]byte(`{"status":"succeeded"}`),
	)

	require.ErrorContains(t, err, "provider token total exceeds")
	assert.Equal(t, original.Status, task.Status)
	assert.Equal(t, original.Progress, task.Progress)
	assert.Equal(t, original.PrivateData.ResultURL, task.PrivateData.ResultURL)
	assert.Equal(t, string(original.Data), string(task.Data))

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, original.Status, persisted.Status)

	var finalizationCount int64
	require.NoError(t, model.DB.Model(&model.TaskBillingFinalization{}).Count(&finalizationCount).Error)
	assert.Zero(t, finalizationCount)
}

func TestUpdateVideoSingleTaskExplicitProviderFailureRefunds(t *testing.T) {
	truncate(t)
	task, channel := seedBillablePollingTask(t)
	adaptor := &taskPollingResponseAdaptor{
		responseBody: []byte(`{}`),
		parseResult: &relaycommon.TaskInfo{
			Status: model.TaskStatusFailure,
			Reason: "provider reported generation failure",
		},
	}

	err := updateVideoSingleTask(context.Background(), adaptor, channel, task.GetUpstreamTaskID(), map[string]*model.Task{
		task.GetUpstreamTaskID(): task,
	})
	require.NoError(t, err)

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), persisted.Status)
	assert.Equal(t, taskcommon.ProgressComplete, persisted.Progress)
	assert.Equal(t, "provider reported generation failure", persisted.FailReason)
	assert.Equal(t, 10_000+task.Quota, getUserQuota(t, task.UserId))
	assert.Equal(t, 2_000+task.Quota, getTokenRemainQuota(t, task.PrivateData.TokenId))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestUpdateVideoSingleTaskRejectsReplacementChannelBeforeFetch(t *testing.T) {
	truncate(t)
	task, replacementChannel := seedBillablePollingTask(t)
	task.PrivateData.ChannelCreatedTime = replacementChannel.CreatedTime - 1
	require.NoError(t, model.DB.Model(task).Update("private_data", task.PrivateData).Error)
	adaptor := &taskPollingFetchAdaptor{}

	err := updateVideoSingleTask(context.Background(), adaptor, replacementChannel, task.GetUpstreamTaskID(), map[string]*model.Task{
		task.GetUpstreamTaskID(): task,
	})
	require.NoError(t, err)
	assert.Zero(t, adaptor.fetchCount(), "replacement channel credentials must never be sent upstream")

	var persisted model.Task
	require.NoError(t, model.DB.First(&persisted, task.ID).Error)
	assert.Equal(t, model.TaskStatus(model.TaskStatusFailure), persisted.Status)
	assert.Equal(t, taskcommon.ProgressComplete, persisted.Progress)
	assert.Contains(t, persisted.FailReason, "原渠道已被替换")
	assert.Equal(t, 10_000+task.Quota, getUserQuota(t, task.UserId))
	assert.Equal(t, 2_000+task.Quota, getTokenRemainQuota(t, task.PrivateData.TokenId))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestUpdateSunoTasksUsesDefaultBaseURLWhenChannelBaseURLIsNil(t *testing.T) {
	truncate(t)

	const channelID = 604
	const upstreamTaskID = "suno-default-base-url"
	seedTaskPollingChannel(t, channelID, true)
	task := seedPollingTask(t, channelID, "suno-public-task", upstreamTaskID)
	task.Platform = constant.TaskPlatformSuno
	require.NoError(t, model.DB.Model(task).Update("platform", task.Platform).Error)

	responseBody, err := common.Marshal(taskdto.TaskResponse[[]taskdto.SunoDataResponse]{
		Code: taskdto.TaskSuccessCode,
		Data: []taskdto.SunoDataResponse{{
			TaskID: upstreamTaskID,
			Status: string(task.Status),
			Data:   task.Data,
		}},
	})
	require.NoError(t, err)
	adaptor := &taskPollingResponseAdaptor{responseBody: responseBody}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	err = updateSunoTasks(context.Background(), channelID, []string{upstreamTaskID}, map[string]*model.Task{
		taskPollingMapKey(channelID, upstreamTaskID): task,
	})
	require.NoError(t, err)
	assert.Equal(t, constant.ChannelBaseURLs[constant.ChannelTypeKling], adaptor.fetchedURL)
}

func TestUpdateSunoTasksGroupsAcceptedRoutingSnapshots(t *testing.T) {
	truncate(t)

	const channelID = 605
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "suno-route-first", "suno-upstream-first")
	second := seedPollingTask(t, channelID, "suno-route-second", "suno-upstream-second")
	third := seedPollingTask(t, channelID, "suno-route-third", "suno-upstream-first")

	first.PrivateData.RoutingSnapshotVersion = 1
	first.PrivateData.Key = "accepted-key-a"
	first.PrivateData.ChannelBaseURL = "https://accepted-a.example"
	first.PrivateData.ChannelProxy = "http://accepted-proxy-a.example"
	second.PrivateData = first.PrivateData
	second.PrivateData.UpstreamTaskID = "suno-upstream-second"
	third.PrivateData.RoutingSnapshotVersion = 1
	third.PrivateData.Key = "accepted-key-b"
	third.PrivateData.ChannelBaseURL = "https://accepted-b.example"
	third.PrivateData.ChannelProxy = "http://accepted-proxy-b.example"
	require.NoError(t, model.DB.Save(first).Error)
	require.NoError(t, model.DB.Save(second).Error)
	require.NoError(t, model.DB.Save(third).Error)

	adaptor := &taskPollingRouteAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	taskM := map[string]*model.Task{
		taskPollingMapKey(channelID, first.TaskID):  first,
		taskPollingMapKey(channelID, second.TaskID): second,
		taskPollingMapKey(channelID, third.TaskID):  third,
	}
	err := updateSunoTasks(
		context.Background(),
		channelID,
		[]string{first.TaskID, second.TaskID, third.TaskID},
		taskM,
	)
	require.NoError(t, err)
	require.Len(t, adaptor.requests, 2)
	assert.Equal(t, taskPollingRouteRequest{
		baseURL: "https://accepted-a.example",
		key:     "accepted-key-a",
		proxy:   "http://accepted-proxy-a.example",
		taskIDs: []string{"suno-upstream-first", "suno-upstream-second"},
	}, adaptor.requests[0])
	assert.Equal(t, taskPollingRouteRequest{
		baseURL: "https://accepted-b.example",
		key:     "accepted-key-b",
		proxy:   "http://accepted-proxy-b.example",
		taskIDs: []string{"suno-upstream-first"},
	}, adaptor.requests[1])
}

func TestReadTaskPollingResponseBodyRejectsOversizedBody(t *testing.T) {
	previousLimit := constant.MaxUpstreamResponseBodyMB
	constant.MaxUpstreamResponseBodyMB = 1
	t.Cleanup(func() { constant.MaxUpstreamResponseBodyMB = previousLimit })

	body := bytes.Repeat([]byte("x"), 1<<20+1)
	responseBody, err := readTaskPollingResponseBody(bytes.NewReader(body))
	require.ErrorContains(t, err, "exceeds 1 MB")
	assert.Nil(t, responseBody)
}

func TestTaskNeedsUpdateComparesJSONSemantically(t *testing.T) {
	task := &model.Task{
		Status: model.TaskStatusInProgress,
		Data:   []byte(`{"a":1,"b":2}`),
	}

	assert.True(t, taskNeedsUpdate(task, taskdto.SunoDataResponse{
		Status: string(task.Status),
		Data:   []byte(`{"a":2,"b":1}`),
	}), "JSON payloads with the same byte multiset can still carry different values")
	assert.False(t, taskNeedsUpdate(task, taskdto.SunoDataResponse{
		Status: string(task.Status),
		Data:   []byte(`{"b":2,"a":1}`),
	}), "object key order must not create a false update")
}

func TestUpdateVideoTasksDefaultSleepWaitsBetweenTasks(t *testing.T) {
	truncate(t)

	const channelID = 101
	seedTaskPollingChannel(t, channelID, false)
	first := seedPollingTask(t, channelID, "task_public_1", "upstream_1")
	second := seedPollingTask(t, channelID, "task_public_2", "upstream_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.Equal(t, 1, adaptor.fetchCount())
}

func TestUpdateVideoTasksCanSkipPollingSleepPerChannel(t *testing.T) {
	truncate(t)

	const channelID = 102
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "task_public_3", "upstream_3")
	second := seedPollingTask(t, channelID, "task_public_4", "upstream_4")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		channelID: {
			first.GetUpstreamTaskID(),
			second.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		first.GetUpstreamTaskID():  first,
		second.GetUpstreamTaskID(): second,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, adaptor.fetchCount())
}

func TestUpdateVideoTasksScopesDuplicateUpstreamIDsByChannel(t *testing.T) {
	truncate(t)

	const firstChannelID = 151
	const secondChannelID = 152
	const sharedUpstreamID = "provider-task-shared-across-channels"
	seedTaskPollingChannel(t, firstChannelID, true)
	seedTaskPollingChannel(t, secondChannelID, true)
	first := seedPollingTask(t, firstChannelID, "task_public_channel_1", sharedUpstreamID)
	second := seedPollingTask(t, secondChannelID, "task_public_channel_2", sharedUpstreamID)

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	err := UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"), map[int][]string{
		firstChannelID:  {sharedUpstreamID},
		secondChannelID: {sharedUpstreamID},
	}, map[string]*model.Task{
		taskPollingMapKey(firstChannelID, sharedUpstreamID):  first,
		taskPollingMapKey(secondChannelID, sharedUpstreamID): second,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, adaptor.fetchCount(), "each channel must poll its own task even when provider IDs collide")
	assert.ElementsMatch(t, []string{sharedUpstreamID, sharedUpstreamID}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksScopesDuplicateUpstreamIDsByAcceptedRoute(t *testing.T) {
	truncate(t)

	const channelID = 153
	const sharedUpstreamID = "provider-task-shared-across-selected-keys"
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "task_public_key_1", sharedUpstreamID)
	second := seedPollingTask(t, channelID, "task_public_key_2", sharedUpstreamID)
	first.PrivateData.RoutingSnapshotVersion = 1
	first.PrivateData.Key = "accepted-key-1"
	second.PrivateData.RoutingSnapshotVersion = 1
	second.PrivateData.Key = "accepted-key-2"

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	err := UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"), map[int][]string{
		channelID: {first.TaskID, second.TaskID},
	}, map[string]*model.Task{
		taskPollingMapKey(channelID, first.TaskID):  first,
		taskPollingMapKey(channelID, second.TaskID): second,
	})

	require.NoError(t, err)
	assert.Equal(t, 2, adaptor.fetchCount())
	assert.ElementsMatch(t, []string{sharedUpstreamID, sharedUpstreamID}, adaptor.fetchedTaskIDs())
	assert.ElementsMatch(t, []string{"accepted-key-1", "accepted-key-2"}, adaptor.fetchedKeys())
}

func TestRunTaskPollingOncePreservesSameChannelUpstreamIDCollisions(t *testing.T) {
	truncate(t)

	const channelID = 154
	const sharedUpstreamID = "provider-task-shared-in-run-loop"
	seedTaskPollingChannel(t, channelID, true)
	first := seedPollingTask(t, channelID, "task_public_run_key_1", sharedUpstreamID)
	second := seedPollingTask(t, channelID, "task_public_run_key_2", sharedUpstreamID)
	first.PrivateData.RoutingSnapshotVersion = 1
	first.PrivateData.Key = "run-key-1"
	second.PrivateData.RoutingSnapshotVersion = 1
	second.PrivateData.Key = "run-key-2"
	require.NoError(t, model.DB.Save(first).Error)
	require.NoError(t, model.DB.Save(second).Error)

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 0
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })
	previousLimit := constant.TaskQueryLimit
	constant.TaskQueryLimit = 100
	t.Cleanup(func() { constant.TaskQueryLimit = previousLimit })

	summary, err := RunTaskPollingOnce(context.Background(), nil)
	require.NoError(t, err)

	assert.Equal(t, 2, summary.UnfinishedTasks)
	assert.Equal(t, 2, adaptor.fetchCount())
	assert.ElementsMatch(t, []string{sharedUpstreamID, sharedUpstreamID}, adaptor.fetchedTaskIDs())
	assert.ElementsMatch(t, []string{"run-key-1", "run-key-2"}, adaptor.fetchedKeys())
}

func TestRunTaskPollingOncePropagatesTaskQueryFailure(t *testing.T) {
	truncate(t)

	injectedErr := errors.New("injected unfinished task query failure")
	const callbackName = "test:fail_unfinished_task_query"
	require.NoError(t, model.DB.Callback().Query().Before("gorm:query").Register(
		callbackName,
		func(tx *gorm.DB) {
			if tx.Statement.Schema != nil && tx.Statement.Schema.Table == "tasks" {
				_ = tx.AddError(injectedErr)
			}
		},
	))
	t.Cleanup(func() {
		require.NoError(t, model.DB.Callback().Query().Remove(callbackName))
	})

	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return &taskPollingFetchAdaptor{}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })
	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 0
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })

	summary, err := RunTaskPollingOnce(context.Background(), nil)

	require.ErrorIs(t, err, injectedErr)
	assert.Zero(t, summary.UnfinishedTasks)
}

func TestUpdateVideoTasksDefaultSleepDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const firstChannelID = 201
	const secondChannelID = 202
	seedTaskPollingChannel(t, firstChannelID, false)
	seedTaskPollingChannel(t, secondChannelID, false)
	firstChannelFirst := seedPollingTask(t, firstChannelID, "task_public_5", "upstream_a_1")
	firstChannelSecond := seedPollingTask(t, firstChannelID, "task_public_6", "upstream_a_2")
	secondChannelFirst := seedPollingTask(t, secondChannelID, "task_public_7", "upstream_b_1")
	secondChannelSecond := seedPollingTask(t, secondChannelID, "task_public_8", "upstream_b_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		firstChannelID: {
			firstChannelFirst.GetUpstreamTaskID(),
			firstChannelSecond.GetUpstreamTaskID(),
		},
		secondChannelID: {
			secondChannelFirst.GetUpstreamTaskID(),
			secondChannelSecond.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		firstChannelFirst.GetUpstreamTaskID():   firstChannelFirst,
		firstChannelSecond.GetUpstreamTaskID():  firstChannelSecond,
		secondChannelFirst.GetUpstreamTaskID():  secondChannelFirst,
		secondChannelSecond.GetUpstreamTaskID(): secondChannelSecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_a_1", "upstream_b_1"}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksSlowChannelDoesNotBlockOtherChannels(t *testing.T) {
	truncate(t)

	const slowChannelID = 251
	const fastChannelID = 252
	seedTaskPollingChannel(t, slowChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	slowTask := seedPollingTask(t, slowChannelID, "task_public_slow", "upstream_slow_1")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_fast_1", "upstream_fast_parallel_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_fast_2", "upstream_fast_parallel_2")
	slowTaskID := slowTask.GetUpstreamTaskID()
	fastFirstID := fastFirst.GetUpstreamTaskID()
	fastSecondID := fastSecond.GetUpstreamTaskID()

	adaptor := &taskPollingFetchAdaptor{
		fetched:      make(chan string, 4),
		blockTaskID:  slowTaskID,
		blockStarted: make(chan struct{}),
		releaseBlock: make(chan struct{}),
	}
	var releaseOnce sync.Once
	releaseBlockedTask := func() {
		releaseOnce.Do(func() {
			close(adaptor.releaseBlock)
		})
	}
	t.Cleanup(releaseBlockedTask)
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	errCh := make(chan error, 1)
	gopool.Go(func() {
		errCh <- UpdateVideoTasks(context.Background(), constant.TaskPlatform("kling"), map[int][]string{
			slowChannelID: {
				slowTaskID,
			},
			fastChannelID: {
				fastFirstID,
				fastSecondID,
			},
		}, map[string]*model.Task{
			slowTaskID:   slowTask,
			fastFirstID:  fastFirst,
			fastSecondID: fastSecond,
		})
	})

	select {
	case <-adaptor.blockStarted:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("slow channel did not start blocking")
	}

	require.Eventually(t, func() bool {
		fetchedTaskIDs := adaptor.fetchedTaskIDs()
		return len(fetchedTaskIDs) == 2 &&
			fetchedTaskIDs[0] == fastFirstID &&
			fetchedTaskIDs[1] == fastSecondID
	}, 500*time.Millisecond, 10*time.Millisecond)

	releaseBlockedTask()
	require.NoError(t, <-errCh)
	assert.ElementsMatch(t, []string{
		slowTaskID,
		fastFirstID,
		fastSecondID,
	}, adaptor.fetchedTaskIDs())
}

func TestUpdateVideoTasksMixedChannelSleepSettings(t *testing.T) {
	truncate(t)

	const sleepyChannelID = 301
	const fastChannelID = 302
	seedTaskPollingChannel(t, sleepyChannelID, false)
	seedTaskPollingChannel(t, fastChannelID, true)
	sleepyFirst := seedPollingTask(t, sleepyChannelID, "task_public_9", "upstream_sleepy_1")
	sleepySecond := seedPollingTask(t, sleepyChannelID, "task_public_10", "upstream_sleepy_2")
	fastFirst := seedPollingTask(t, fastChannelID, "task_public_11", "upstream_fast_1")
	fastSecond := seedPollingTask(t, fastChannelID, "task_public_12", "upstream_fast_2")

	adaptor := &taskPollingFetchAdaptor{}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := UpdateVideoTasks(ctx, constant.TaskPlatform("kling"), map[int][]string{
		sleepyChannelID: {
			sleepyFirst.GetUpstreamTaskID(),
			sleepySecond.GetUpstreamTaskID(),
		},
		fastChannelID: {
			fastFirst.GetUpstreamTaskID(),
			fastSecond.GetUpstreamTaskID(),
		},
	}, map[string]*model.Task{
		sleepyFirst.GetUpstreamTaskID():  sleepyFirst,
		sleepySecond.GetUpstreamTaskID(): sleepySecond,
		fastFirst.GetUpstreamTaskID():    fastFirst,
		fastSecond.GetUpstreamTaskID():   fastSecond,
	})

	require.ErrorIs(t, err, context.DeadlineExceeded)
	assert.ElementsMatch(t, []string{"upstream_sleepy_1", "upstream_fast_1", "upstream_fast_2"}, adaptor.fetchedTaskIDs())
}

func TestUpdateSunoTasksStalePollsRefundExactlyOnce(t *testing.T) {
	truncate(t)

	const userID, tokenID, channelID = 401, 401, 401
	const initialUserQuota, initialTokenQuota, taskQuota = 10_000, 6_000, 2_500
	const publicTaskID, upstreamTaskID = "suno_public_refund_once", "suno_upstream_refund_once"

	seedUser(t, userID, initialUserQuota)
	seedToken(t, tokenID, userID, "sk-suno-refund-once", initialTokenQuota)
	baseURL := "https://suno.invalid"
	require.NoError(t, model.DB.Create(&model.Channel{
		Id:      channelID,
		Type:    constant.ChannelTypeSunoAPI,
		Name:    "suno_refund_once",
		Key:     "sk-suno-channel",
		Status:  common.ChannelStatusEnabled,
		BaseURL: &baseURL,
	}).Error)

	task := makeTask(userID, channelID, taskQuota, tokenID, BillingSourceWallet, 0)
	task.TaskID = publicTaskID
	task.Platform = constant.TaskPlatformSuno
	task.Status = model.TaskStatusInProgress
	task.Progress = "50%"
	task.SubmitTime = time.Now().Unix()
	task.PrivateData.UpstreamTaskID = upstreamTaskID
	require.NoError(t, model.DB.Create(task).Error)

	var firstPollTask model.Task
	var staleSecondPollTask model.Task
	require.NoError(t, model.DB.First(&firstPollTask, task.ID).Error)
	require.NoError(t, model.DB.First(&staleSecondPollTask, task.ID).Error)

	adaptor := &sunoFailurePollingAdaptor{failReason: "upstream failed"}
	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor { return adaptor }
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	require.NoError(t, updateSunoTasks(context.Background(), channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &firstPollTask,
	}))
	require.NoError(t, updateSunoTasks(context.Background(), channelID, []string{upstreamTaskID}, map[string]*model.Task{
		upstreamTaskID: &staleSecondPollTask,
	}))

	var reloaded model.Task
	require.NoError(t, model.DB.First(&reloaded, task.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloaded.Status)
	assert.Zero(t, reloaded.Quota)
	assert.Equal(t, initialUserQuota+taskQuota, getUserQuota(t, userID))
	assert.Equal(t, initialTokenQuota+taskQuota, getTokenRemainQuota(t, tokenID))
	assert.Equal(t, int64(1), countLogs(t))
}

func TestRunTaskPollingOnceDoesNotRefundHistoricalFailedTask(t *testing.T) {
	truncate(t)

	const userID, initialQuota, taskQuota = 402, 10_000, 1_200
	seedUser(t, userID, initialQuota)

	task := makeTask(userID, 0, taskQuota, 0, BillingSourceWallet, 0)
	task.TaskID = "historical_failed_already_refunded"
	task.Status = model.TaskStatusFailure
	task.Progress = "100%"
	task.SubmitTime = time.Now().Add(-90 * 24 * time.Hour).Unix()
	task.UpdatedAt = time.Now().Add(-time.Minute).Unix()
	require.NoError(t, model.DB.Create(task).Error)

	previousFactory := GetTaskAdaptorFunc
	GetTaskAdaptorFunc = func(constant.TaskPlatform) TaskPollingAdaptor {
		return &taskPollingFetchAdaptor{}
	}
	t.Cleanup(func() { GetTaskAdaptorFunc = previousFactory })

	summary, err := RunTaskPollingOnce(context.Background(), nil)
	require.NoError(t, err)

	assert.Zero(t, summary.UnfinishedTasks)
	assert.Equal(t, initialQuota, getUserQuota(t, userID))
	assert.Equal(t, taskQuota, getTaskQuota(t, task.ID))
	assert.Equal(t, int64(0), countLogs(t))
}

func TestSweepTimedOutTasksHonorsRefundRolloutBoundary(t *testing.T) {
	truncate(t)

	const (
		userID          = 403
		initialQuota    = 10_000
		legacyTaskQuota = 1_800
		modernTaskQuota = 1_200
	)
	seedUser(t, userID, initialQuota)

	legacyTask := makeTask(userID, 0, legacyTaskQuota, 0, BillingSourceWallet, 0)
	legacyTask.TaskID = "legacy_timeout_without_refund"
	legacyTask.Progress = "50%"
	legacyTask.SubmitTime = 1771718399 // 2026-02-21 23:59:59 UTC
	require.NoError(t, model.DB.Create(legacyTask).Error)

	modernTask := makeTask(userID, 0, modernTaskQuota, 0, BillingSourceWallet, 0)
	modernTask.TaskID = "modern_timeout_with_refund"
	modernTask.Progress = "50%"
	modernTask.SubmitTime = 1771718400 // 2026-02-22 00:00:00 UTC
	require.NoError(t, model.DB.Create(modernTask).Error)

	previousTimeout := constant.TaskTimeoutMinutes
	constant.TaskTimeoutMinutes = 1
	t.Cleanup(func() { constant.TaskTimeoutMinutes = previousTimeout })

	sweepTimedOutTasks(context.Background())

	var reloadedLegacy model.Task
	var reloadedModern model.Task
	require.NoError(t, model.DB.First(&reloadedLegacy, legacyTask.ID).Error)
	require.NoError(t, model.DB.First(&reloadedModern, modernTask.ID).Error)
	assert.EqualValues(t, model.TaskStatusFailure, reloadedLegacy.Status)
	assert.EqualValues(t, model.TaskStatusFailure, reloadedModern.Status)
	assert.Equal(t, legacyTaskQuota, reloadedLegacy.Quota)
	assert.Zero(t, reloadedModern.Quota)
	assert.Contains(t, reloadedLegacy.FailReason, "旧系统遗留任务")
	assert.Contains(t, reloadedModern.FailReason, "任务超时")
	assert.Equal(t, initialQuota+modernTaskQuota, getUserQuota(t, userID))
	assert.Equal(t, int64(1), countLogs(t))
}
