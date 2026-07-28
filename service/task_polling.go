package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel/task/taskcommon"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/samber/lo"
)

// TaskPollingAdaptor 定义轮询所需的最小适配器接口，避免 service -> relay 的循环依赖
type TaskPollingAdaptor interface {
	Init(info *relaycommon.RelayInfo)
	FetchTask(ctx context.Context, baseURL string, key string, body map[string]any, proxy string) (*http.Response, error)
	ParseTaskResult(body []byte) (*relaycommon.TaskInfo, error)
	// AdjustBillingOnComplete 在任务到达终态（成功/失败）时由轮询循环调用。
	// 返回正数触发差额结算（补扣/退还），返回 0 保持预扣费金额不变。
	AdjustBillingOnComplete(task *model.Task, taskResult *relaycommon.TaskInfo) int
}

// GetTaskAdaptorFunc 由 main 包注入，用于获取指定平台的任务适配器。
// 打破 service -> relay -> relay/channel -> service 的循环依赖。
var GetTaskAdaptorFunc func(platform constant.TaskPlatform) TaskPollingAdaptor

// Tasks submitted before the durable task-billing rollout cannot be refunded
// safely because their persisted rows may not identify the original funding
// source. This timestamp is 2026-02-22 00:00:00 UTC, the deployment boundary
// used by the timeout sweeper.
const legacyTaskBillingCutoff int64 = 1771718400

func readTaskPollingResponseBody(body io.Reader) ([]byte, error) {
	maxMB := constant.MaxUpstreamResponseBodyMB
	if maxMB <= 0 {
		maxMB = 128
	}
	maxBytes := common.BytesFromMegabytes(maxMB)
	responseBody, err := io.ReadAll(io.LimitReader(body, common.ReadLimitWithOverrunByte(maxBytes)))
	if err != nil {
		return nil, err
	}
	if int64(len(responseBody)) > maxBytes {
		return nil, fmt.Errorf("task polling response exceeds %d MB", maxMB)
	}
	return responseBody, nil
}

func persistTaskTerminalTransition(ctx context.Context, task *model.Task, fromStatus model.TaskStatus, payload *model.TaskBillingFinalizationPayload) (bool, error) {
	won, finalizationID, err := task.UpdateWithStatusAndBillingFinalization(fromStatus, payload)
	if err != nil || !won {
		return won, err
	}
	processPersistedTaskBillingFinalization(ctx, task, finalizationID)
	return true, nil
}

func reloadTaskAfterPollingCASLoss(task *model.Task, original model.Task) error {
	persisted, exists, err := model.GetByTaskId(original.UserId, original.TaskID)
	if err != nil {
		*task = original
		return err
	}
	if !exists || persisted.ID != original.ID {
		*task = original
		return errors.New("task changed concurrently and its canonical row could not be reloaded")
	}
	*task = *persisted
	return nil
}

func failTaskWithRefund(ctx context.Context, task *model.Task, reason string) (bool, error) {
	fromStatus := task.Status
	task.Status = model.TaskStatusFailure
	task.Progress = taskcommon.ProgressComplete
	if task.FinishTime == 0 {
		task.FinishTime = time.Now().Unix()
	}
	task.FailReason = reason
	return persistTaskTerminalTransition(ctx, task, fromStatus, taskRefundFinalization(task, reason))
}

func failTasksWithRefund(ctx context.Context, tasks []*model.Task, reason string) (int, error) {
	failed := 0
	var transitionErrors []error
	for _, task := range tasks {
		fromStatus := task.Status
		task.Status = model.TaskStatusFailure
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = time.Now().Unix()
		}
		task.FailReason = reason

		var payload *model.TaskBillingFinalizationPayload
		if task.SubmitTime > 0 && task.SubmitTime < legacyTaskBillingCutoff {
			task.FailReason = reason + "（旧系统遗留任务，不进行退款，请联系管理员）"
		} else {
			payload = taskRefundFinalization(task, reason)
		}
		won, err := persistTaskTerminalTransition(ctx, task, fromStatus, payload)
		if err != nil {
			transitionErrors = append(transitionErrors, fmt.Errorf("task %s: %w", task.TaskID, err))
			continue
		}
		if won {
			failed++
		}
	}
	return failed, errors.Join(transitionErrors...)
}

// sweepTimedOutTasks 在主轮询之前独立清理超时任务。
// 每次最多处理 100 条，剩余的下个周期继续处理。
// 使用 per-task CAS (UpdateWithStatus) 防止覆盖被正常轮询已推进的任务。
func sweepTimedOutTasks(ctx context.Context) error {
	if constant.TaskTimeoutMinutes <= 0 {
		return nil
	}
	timeout := common.SafeIntervalDuration(
		constant.TaskTimeoutMinutes,
		time.Minute,
		24*time.Hour,
		"task timeout",
	)
	cutoff := time.Now().Add(-timeout).Unix()
	tasks, err := model.GetTimedOutUnfinishedTasks(cutoff, 100)
	if err != nil {
		return fmt.Errorf("query timed-out tasks: %w", err)
	}
	if len(tasks) == 0 {
		return nil
	}

	reason := fmt.Sprintf("任务超时（%d分钟）", int64(timeout/time.Minute))
	legacyReason := "任务超时（旧系统遗留任务，不进行退款，请联系管理员）"
	now := time.Now().Unix()
	timedOutCount := 0
	var transitionErrors []error

	for _, task := range tasks {
		isLegacy := task.SubmitTime > 0 && task.SubmitTime < legacyTaskBillingCutoff

		oldStatus := task.Status
		task.Status = model.TaskStatusFailure
		task.Progress = "100%"
		task.FinishTime = now
		if isLegacy {
			task.FailReason = legacyReason
		} else {
			task.FailReason = reason
		}

		var payload *model.TaskBillingFinalizationPayload
		if !isLegacy {
			payload = taskRefundFinalization(task, reason)
		}
		won, err := persistTaskTerminalTransition(ctx, task, oldStatus, payload)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("sweepTimedOutTasks CAS update error for task %s: %v", task.TaskID, err))
			transitionErrors = append(transitionErrors, fmt.Errorf("task %s: %w", task.TaskID, err))
			continue
		}
		if !won {
			logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: task %s already transitioned, skip", task.TaskID))
			continue
		}
		timedOutCount++
	}

	if timedOutCount > 0 {
		logger.LogInfo(ctx, fmt.Sprintf("sweepTimedOutTasks: timed out %d tasks", timedOutCount))
	}
	return errors.Join(transitionErrors...)
}

// TaskPollSummary is the result recorded on an async_task_poll system task row,
// summarizing one polling pass.
type TaskPollSummary struct {
	UnfinishedTasks  int `json:"unfinished_tasks"`
	PlatformsScanned int `json:"platforms_scanned"`
	NullTasksFailed  int `json:"null_tasks_failed"`
}

// RunTaskPollingOnce performs one async-task (Suno/video) polling pass
// synchronously. It honors ctx cancellation (the system-task runner cancels it
// when the lease is lost) and, when report is non-nil, reports progress as
// (processedPlatforms, totalPlatforms). It returns immediately if the task
// adaptor factory has not been wired yet, to avoid a nil call during startup.
func RunTaskPollingOnce(ctx context.Context, report func(processed, total int)) (TaskPollSummary, error) {
	summary := TaskPollSummary{}
	if GetTaskAdaptorFunc == nil {
		return summary, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	common.SysLog("任务进度轮询开始")
	if err := sweepTimedOutTasks(ctx); err != nil {
		return summary, err
	}
	allTasks, err := model.GetAllUnFinishSyncTasks(constant.TaskQueryLimit)
	if err != nil {
		return summary, fmt.Errorf("query unfinished async tasks: %w", err)
	}
	summary.UnfinishedTasks = len(allTasks)
	platformTask := make(map[constant.TaskPlatform][]*model.Task)
	for _, t := range allTasks {
		platformTask[t.Platform] = append(platformTask[t.Platform], t)
	}

	totalPlatforms := len(platformTask)
	processedPlatforms := 0
	for platform, tasks := range platformTask {
		if ctx.Err() != nil {
			break
		}
		if report != nil {
			report(processedPlatforms, totalPlatforms)
		}
		processedPlatforms++
		if len(tasks) == 0 {
			continue
		}
		summary.PlatformsScanned++
		taskChannelM := make(map[int][]string)
		taskM := make(map[string]*model.Task)
		nullTasks := make([]*model.Task, 0)
		for _, task := range tasks {
			upstreamID := task.GetUpstreamTaskID()
			if upstreamID == "" {
				nullTasks = append(nullTasks, task)
				continue
			}
			// Use the unique database row as the local polling handle. Provider
			// IDs are account-scoped and public IDs can collide in imported
			// legacy data; either one could otherwise overwrite a task before
			// its route-specific polling request.
			pollingHandle := "db:" + strconv.FormatInt(task.ID, 10)
			taskM[taskPollingMapKey(task.ChannelId, pollingHandle)] = task
			taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], pollingHandle)
		}
		if len(nullTasks) > 0 {
			failed, err := failTasksWithRefund(ctx, nullTasks, "任务缺少上游任务 ID")
			summary.NullTasksFailed += failed
			if err != nil {
				logger.LogError(ctx, fmt.Sprintf("Fix null task_id task error: %v", err))
			} else {
				logger.LogInfo(ctx, fmt.Sprintf("Fix null task_id task success: %d", failed))
			}
		}
		if len(taskChannelM) == 0 {
			continue
		}

		DispatchPlatformUpdate(ctx, platform, taskChannelM, taskM)
	}
	if report != nil && ctx.Err() == nil {
		report(totalPlatforms, totalPlatforms)
	}
	common.SysLog("任务进度轮询完成")
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	return summary, nil
}

func taskPollingMapKey(channelID int, pollingID string) string {
	return strconv.Itoa(channelID) + "\x00" + pollingID
}

func taskForPolling(taskM map[string]*model.Task, channelID int, pollingID string) *model.Task {
	if task := taskM[taskPollingMapKey(channelID, pollingID)]; task != nil {
		return task
	}
	// Keep compatibility with direct/internal callers that predate channel-
	// scoped local handles.
	return taskM[pollingID]
}

type taskPollingRoute struct {
	baseURL string
	key     string
	proxy   string
}

func taskPollingRouteFor(task *model.Task, channel *model.Channel) taskPollingRoute {
	if channel == nil {
		return taskPollingRoute{}
	}
	baseURL := constant.ChannelBaseURLs[channel.Type]
	if channel.GetBaseURL() != "" {
		baseURL = channel.GetBaseURL()
	}
	route := taskPollingRoute{
		baseURL: baseURL,
		key:     channel.Key,
		proxy:   channel.GetSetting().Proxy,
	}
	if task == nil {
		return route
	}
	if task.PrivateData.RoutingSnapshotVersion > 0 {
		route.baseURL = task.PrivateData.ChannelBaseURL
		route.key = task.PrivateData.Key
		route.proxy = task.PrivateData.ChannelProxy
		return route
	}
	// Gemini/Vertex tasks written before versioned routing snapshots already
	// persisted their selected key. Preserve that legacy compatibility.
	if task.PrivateData.Key != "" {
		route.key = task.PrivateData.Key
	}
	return route
}

func failTaskIfChannelIdentityChanged(ctx context.Context, task *model.Task, channel *model.Channel) (bool, error) {
	if task == nil || channel == nil || task.PrivateData.ChannelCreatedTime == 0 ||
		task.PrivateData.ChannelCreatedTime == channel.CreatedTime {
		return false, nil
	}
	reason := fmt.Sprintf(
		"任务原渠道已被替换（channel=%d, expected_created=%d, actual_created=%d）",
		channel.Id,
		task.PrivateData.ChannelCreatedTime,
		channel.CreatedTime,
	)
	won, err := failTaskWithRefund(ctx, task, reason)
	if err != nil {
		return true, err
	}
	if !won {
		logger.LogWarn(ctx, fmt.Sprintf("Task %s already transitioned while rejecting replacement channel", task.TaskID))
	}
	return true, nil
}

// DispatchPlatformUpdate 按平台分发轮询更新
func DispatchPlatformUpdate(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) {
	if ctx == nil {
		ctx = context.Background()
	}
	switch platform {
	case constant.TaskPlatformMidjourney:
		// MJ 轮询由其自身处理，这里预留入口
	case constant.TaskPlatformSuno:
		_ = UpdateSunoTasks(ctx, taskChannelM, taskM)
	default:
		if err := UpdateVideoTasks(ctx, platform, taskChannelM, taskM); err != nil {
			common.SysLog(fmt.Sprintf("UpdateVideoTasks fail: %s", err))
		}
	}
}

// UpdateSunoTasks 按渠道更新所有 Suno 任务
func UpdateSunoTasks(ctx context.Context, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	var updateErrors []error
	for channelId, taskIds := range taskChannelM {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := updateSunoTasks(ctx, channelId, taskIds, taskM)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("渠道 #%d 更新异步任务失败: %s", channelId, err.Error()))
			updateErrors = append(updateErrors, fmt.Errorf("channel %d: %w", channelId, err))
		}
	}
	return errors.Join(updateErrors...)
}

func updateSunoTasks(ctx context.Context, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	ch, err := model.CacheGetChannel(channelId)
	if err != nil {
		common.SysLog(fmt.Sprintf("CacheGetChannel: %v", err))
		return fmt.Errorf("CacheGetChannel failed for channel %d: %w", channelId, err)
	}
	var responseErrors []error
	type sunoPollingGroup struct {
		route taskPollingRoute
		tasks []*model.Task
	}
	groups := make([]sunoPollingGroup, 0, 1)
	groupIndexes := make(map[taskPollingRoute]int)
	for _, taskID := range taskIds {
		task := taskForPolling(taskM, channelId, taskID)
		if task == nil {
			responseErrors = append(responseErrors, fmt.Errorf("task %s is missing from the polling set", taskID))
			continue
		}
		handled, identityErr := failTaskIfChannelIdentityChanged(ctx, task, ch)
		if identityErr != nil {
			responseErrors = append(responseErrors, fmt.Errorf("task %s channel identity: %w", taskID, identityErr))
		}
		if handled {
			continue
		}
		route := taskPollingRouteFor(task, ch)
		index, exists := groupIndexes[route]
		if !exists {
			index = len(groups)
			groupIndexes[route] = index
			groups = append(groups, sunoPollingGroup{route: route})
		}
		groups[index].tasks = append(groups[index].tasks, task)
	}
	if len(groups) == 0 {
		return errors.Join(responseErrors...)
	}
	adaptor := GetTaskAdaptorFunc(constant.TaskPlatformSuno)
	if adaptor == nil {
		return errors.New("adaptor not found")
	}

	for _, group := range groups {
		if err := pollSunoTaskGroup(ctx, adaptor, channelId, group.route, group.tasks); err != nil {
			responseErrors = append(responseErrors, err)
		}
	}
	return errors.Join(responseErrors...)
}

// pollSunoTaskGroup batches only tasks accepted with the same immutable
// provider route. Mixing selected multi-keys or endpoints in one request can
// query the wrong account and strand an otherwise billable task.
func pollSunoTaskGroup(
	ctx context.Context,
	adaptor TaskPollingAdaptor,
	channelID int,
	route taskPollingRoute,
	tasks []*model.Task,
) error {
	taskIDs := make([]string, 0, len(tasks))
	tasksByUpstreamID := make(map[string][]*model.Task, len(tasks))
	for _, task := range tasks {
		upstreamID := task.GetUpstreamTaskID()
		taskIDs = append(taskIDs, upstreamID)
		tasksByUpstreamID[upstreamID] = append(tasksByUpstreamID[upstreamID], task)
	}
	resp, err := adaptor.FetchTask(ctx, route.baseURL, route.key, map[string]any{
		"ids": taskIDs,
	}, route.proxy)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Task Do req error: %v", err))
		return err
	}
	if resp == nil {
		return errors.New("Get Suno Task returned a nil response")
	}
	if resp.Body == nil {
		return errors.New("Get Suno Task returned a nil response body")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		logger.LogError(ctx, fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
		return fmt.Errorf("Get Task status code: %d", resp.StatusCode)
	}
	responseBody, err := readTaskPollingResponseBody(resp.Body)
	if err != nil {
		common.SysLog(fmt.Sprintf("Get Suno Task parse body error: %v", err))
		return err
	}
	var responseItems dto.TaskResponse[[]dto.SunoDataResponse]
	err = common.Unmarshal(responseBody, &responseItems)
	if err != nil {
		logger.LogError(
			ctx,
			fmt.Sprintf(
				"Get Suno Task parse body error: %v, response_bytes=%d",
				err,
				len(responseBody),
			),
		)
		return err
	}
	if !responseItems.IsSuccess() {
		common.SysLog(fmt.Sprintf(
			"channel #%d Suno poll failed for %d tasks: code=%s response_bytes=%d",
			channelID,
			len(taskIDs),
			responseItems.Code,
			len(responseBody),
		))
		return fmt.Errorf(
			"Get Suno Task returned unsuccessful response: code=%s message_bytes=%d",
			responseItems.Code,
			len(responseItems.Message),
		)
	}

	var responseErrors []error
	for _, responseItem := range responseItems.Data {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		matchedTasks := tasksByUpstreamID[responseItem.TaskID]
		if len(matchedTasks) == 0 {
			logger.LogWarn(
				ctx,
				fmt.Sprintf(
					"Suno task response ignored: unknown task_id_bytes=%d",
					len(responseItem.TaskID),
				),
			)
			continue
		}
		for _, task := range matchedTasks {
			responseStatus := model.TaskStatus(responseItem.Status)
			switch responseStatus {
			case model.TaskStatusNotStart, model.TaskStatusSubmitted, model.TaskStatusQueued,
				model.TaskStatusInProgress, model.TaskStatusSuccess, model.TaskStatusFailure:
			default:
				responseErrors = append(responseErrors, fmt.Errorf("Suno task %s returned unknown status %q", task.TaskID, responseItem.Status))
				continue
			}
			if !taskNeedsUpdate(task, responseItem) {
				continue
			}

			fromStatus := task.Status
			task.Status = responseStatus
			task.FailReason = lo.If(responseItem.FailReason != "", responseItem.FailReason).Else(task.FailReason)
			task.SubmitTime = lo.If(responseItem.SubmitTime != 0, responseItem.SubmitTime).Else(task.SubmitTime)
			task.StartTime = lo.If(responseItem.StartTime != 0, responseItem.StartTime).Else(task.StartTime)
			task.FinishTime = lo.If(responseItem.FinishTime != 0, responseItem.FinishTime).Else(task.FinishTime)
			if responseItem.FailReason != "" || task.Status == model.TaskStatusFailure {
				logger.LogInfo(
					ctx,
					fmt.Sprintf(
						"Suno task %s failed; reason_bytes=%d",
						task.TaskID,
						len(task.FailReason),
					),
				)
				task.Progress = "100%"
			}
			if responseItem.Status == model.TaskStatusSuccess {
				task.Progress = "100%"
			}
			task.Data = responseItem.Data

			if task.Status == model.TaskStatusFailure || task.Status == model.TaskStatusSuccess {
				var payload *model.TaskBillingFinalizationPayload
				if task.Status == model.TaskStatusFailure {
					payload = taskRefundFinalization(task, task.FailReason)
				}
				won, transitionErr := persistTaskTerminalTransition(ctx, task, fromStatus, payload)
				if transitionErr != nil {
					common.SysLog("UpdateSunoTask terminal transition error: " + transitionErr.Error())
					responseErrors = append(responseErrors, fmt.Errorf("persist Suno task %s terminal transition: %w", task.TaskID, transitionErr))
				} else if !won {
					logger.LogWarn(ctx, fmt.Sprintf("Suno task %s already transitioned, skip billing", task.TaskID))
				}
				continue
			}

			if _, err = task.UpdateWithStatus(fromStatus); err != nil {
				common.SysLog("UpdateSunoTask task error: " + err.Error())
				responseErrors = append(responseErrors, fmt.Errorf("persist Suno task %s polling state: %w", task.TaskID, err))
			}
		}
	}
	return errors.Join(responseErrors...)
}

// taskNeedsUpdate 检查 Suno 任务是否需要更新
func taskNeedsUpdate(oldTask *model.Task, newTask dto.SunoDataResponse) bool {
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if string(oldTask.Status) != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}

	if (oldTask.Status == model.TaskStatusFailure || oldTask.Status == model.TaskStatusSuccess) && oldTask.Progress != "100%" {
		return true
	}

	var oldData any
	var newData any
	oldErr := common.Unmarshal(oldTask.Data, &oldData)
	newErr := common.Unmarshal(newTask.Data, &newData)
	if oldErr == nil && newErr == nil {
		return !reflect.DeepEqual(oldData, newData)
	}
	return !bytes.Equal(oldTask.Data, newTask.Data)
}

// UpdateVideoTasks 按渠道更新所有视频任务
func UpdateVideoTasks(ctx context.Context, platform constant.TaskPlatform, taskChannelM map[int][]string, taskM map[string]*model.Task) error {
	channelIDs := make([]int, 0, len(taskChannelM))
	for channelID := range taskChannelM {
		channelIDs = append(channelIDs, channelID)
	}
	sort.Ints(channelIDs)

	var wg sync.WaitGroup
	for _, channelId := range channelIDs {
		taskIds := taskChannelM[channelId]
		if len(taskIds) == 0 {
			continue
		}
		taskIds = append([]string(nil), taskIds...)

		wg.Add(1)
		gopool.Go(func() {
			defer wg.Done()
			if err := updateVideoTasks(ctx, platform, channelId, taskIds, taskM); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Channel #%d failed to update video async tasks: %s", channelId, err.Error()))
			}
		})
	}
	wg.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return nil
}

func updateVideoTasks(ctx context.Context, platform constant.TaskPlatform, channelId int, taskIds []string, taskM map[string]*model.Task) error {
	logger.LogInfo(ctx, fmt.Sprintf("Channel #%d pending video tasks: %d", channelId, len(taskIds)))
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if len(taskIds) == 0 {
		return nil
	}
	cacheGetChannel, err := model.CacheGetChannel(channelId)
	if err != nil {
		return fmt.Errorf("CacheGetChannel failed for channel %d: %w", channelId, err)
	}
	adaptor := GetTaskAdaptorFunc(platform)
	if adaptor == nil {
		return fmt.Errorf("video adaptor not found")
	}
	info := &relaycommon.RelayInfo{}
	info.ChannelMeta = &relaycommon.ChannelMeta{
		ChannelBaseUrl: cacheGetChannel.GetBaseURL(),
	}
	info.ApiKey = cacheGetChannel.Key
	adaptor.Init(info)
	disablePollingSleep := cacheGetChannel.GetOtherSettings().DisableTaskPollingSleep
	for i, taskId := range taskIds {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := updateVideoSingleTask(ctx, adaptor, cacheGetChannel, taskId, taskM); err != nil {
			logger.LogError(ctx, fmt.Sprintf("Failed to update video task %s: %s", taskId, err.Error()))
		}
		if disablePollingSleep || i == len(taskIds)-1 {
			continue
		}

		// sleep 1 second between tasks for this channel only.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(1 * time.Second):
		}
	}
	return nil
}

// ApplyTaskPollingResult persists one provider polling result. Terminal status
// and its refund/recalculation outbox are committed by the same CAS
// transaction, regardless of whether the result came from the background
// poller or an on-demand realtime fetch.
func ApplyTaskPollingResult(
	ctx context.Context,
	adaptor TaskPollingAdaptor,
	task *model.Task,
	taskResult *relaycommon.TaskInfo,
	responseBody []byte,
) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if adaptor == nil || task == nil || taskResult == nil {
		return errors.New("task polling result context is incomplete")
	}
	if taskResult.Status == "" {
		return fmt.Errorf("upstream returned empty task status for task %s", task.TaskID)
	}

	original := *task
	snap := task.Snapshot()
	resultStatus := model.TaskStatus(taskResult.Status)
	if (snap.Status == model.TaskStatusSuccess || snap.Status == model.TaskStatusFailure) &&
		resultStatus != snap.Status {
		return fmt.Errorf("terminal task %s cannot transition from %s to %s", task.TaskID, snap.Status, resultStatus)
	}

	now := time.Now().Unix()
	var billingFinalization *model.TaskBillingFinalizationPayload
	switch resultStatus {
	case model.TaskStatusNotStart:
	case model.TaskStatusSubmitted:
		task.Progress = taskcommon.ProgressSubmitted
	case model.TaskStatusQueued:
		task.Progress = taskcommon.ProgressQueued
	case model.TaskStatusInProgress:
		task.Progress = taskcommon.ProgressInProgress
		if task.StartTime == 0 {
			task.StartTime = now
		}
	case model.TaskStatusSuccess:
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		if strings.HasPrefix(taskResult.Url, "data:") {
			// data: URI (e.g. Vertex base64 encoded video) — keep in Data, not
			// in ResultURL.
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		} else if taskResult.Url != "" {
			task.PrivateData.ResultURL = taskResult.Url
		} else {
			task.PrivateData.ResultURL = taskcommon.BuildProxyURL(task.TaskID)
		}
		var billingErr error
		billingFinalization, billingErr = taskSettlementFinalizationOnComplete(ctx, adaptor, task, taskResult)
		if billingErr != nil {
			*task = original
			return fmt.Errorf("prepare terminal billing for task %s: %w", task.TaskID, billingErr)
		}
	case model.TaskStatusFailure:
		task.Progress = taskcommon.ProgressComplete
		if task.FinishTime == 0 {
			task.FinishTime = now
		}
		task.FailReason = taskResult.Reason
		logger.LogInfo(
			ctx,
			fmt.Sprintf(
				"Task %s failed; reason_bytes=%d",
				task.TaskID,
				len(task.FailReason),
			),
		)
		taskResult.Progress = taskcommon.ProgressComplete
		billingFinalization = taskRefundFinalization(task, task.FailReason)
	default:
		return fmt.Errorf("unknown task status %s for task %s", taskResult.Status, task.TaskID)
	}

	task.Status = resultStatus
	if responseBody != nil {
		task.Data = redactVideoResponseBody(responseBody)
	}
	if taskResult.Progress != "" {
		task.Progress = taskResult.Progress
	}

	isDone := task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure
	if isDone && snap.Status != task.Status {
		won, err := persistTaskTerminalTransition(ctx, task, snap.Status, billingFinalization)
		if err != nil {
			// Realtime fetch callers render this same object after a failed
			// persistence attempt. Do not leak an upstream-only terminal state
			// when the atomic status/billing transaction did not commit.
			*task = original
			return fmt.Errorf("persist terminal task %s: %w", task.TaskID, err)
		}
		if !won {
			logger.LogWarn(ctx, fmt.Sprintf("Task %s already transitioned by another process, skip billing", task.TaskID))
			if err := reloadTaskAfterPollingCASLoss(task, original); err != nil {
				return fmt.Errorf("reload task %s after terminal CAS loss: %w", original.TaskID, err)
			}
		}
		return nil
	}
	if snap.Equal(task.Snapshot()) {
		logger.LogDebug(ctx, "No update needed for task %s", task.TaskID)
		return nil
	}
	won, err := task.UpdateWithStatus(snap.Status)
	if err != nil {
		// Keep the returned object aligned with durable state on write errors.
		// Background polling discards the object, but realtime fetches do not.
		*task = original
		return fmt.Errorf("persist task %s polling state: %w", task.TaskID, err)
	}
	if !won {
		logger.LogWarn(ctx, fmt.Sprintf("Task %s polling state changed concurrently", task.TaskID))
		if err := reloadTaskAfterPollingCASLoss(task, original); err != nil {
			return fmt.Errorf("reload task %s after polling CAS loss: %w", original.TaskID, err)
		}
	}
	return nil
}

func updateVideoSingleTask(ctx context.Context, adaptor TaskPollingAdaptor, ch *model.Channel, taskId string, taskM map[string]*model.Task) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}

	task := taskForPolling(taskM, ch.Id, taskId)
	if task == nil {
		logger.LogError(ctx, fmt.Sprintf("Task %s not found in taskM", taskId))
		return fmt.Errorf("task %s not found", taskId)
	}
	handled, err := failTaskIfChannelIdentityChanged(ctx, task, ch)
	if err != nil {
		return fmt.Errorf("reject replacement channel for task %s: %w", taskId, err)
	}
	if handled {
		return nil
	}
	route := taskPollingRouteFor(task, ch)
	resp, err := adaptor.FetchTask(ctx, route.baseURL, route.key, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, route.proxy)
	if err != nil {
		return fmt.Errorf("fetchTask failed for task %s: %w", taskId, err)
	}
	if resp == nil {
		return fmt.Errorf("fetchTask returned a nil response for task %s", taskId)
	}
	if resp.Body == nil {
		return fmt.Errorf("fetchTask returned a nil response body for task %s", taskId)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("fetchTask returned status code %d for task %s", resp.StatusCode, taskId)
	}
	responseBody, err := readTaskPollingResponseBody(resp.Body)
	if err != nil {
		return fmt.Errorf("readAll failed for task %s: %w", taskId, err)
	}

	logger.LogDebug(
		ctx,
		"updateVideoSingleTask response received: status=%d bytes=%d",
		resp.StatusCode,
		len(responseBody),
	)

	taskResult := &relaycommon.TaskInfo{}
	// try parse as New API response format
	var responseItems dto.TaskResponse[model.Task]
	if err = common.Unmarshal(responseBody, &responseItems); err == nil && responseItems.IsSuccess() {
		logger.LogDebug(
			ctx,
			"updateVideoSingleTask parsed as new api response format: status=%s",
			responseItems.Data.Status,
		)
		t := responseItems.Data
		taskResult.TaskID = t.TaskID
		taskResult.Status = string(t.Status)
		taskResult.Url = t.GetResultURL()
		taskResult.Progress = t.Progress
		taskResult.Reason = t.FailReason
	} else if taskResult, err = adaptor.ParseTaskResult(responseBody); err != nil {
		return fmt.Errorf("parseTaskResult failed for task %s: %w", taskId, err)
	}

	if taskResult == nil {
		return fmt.Errorf("parseTaskResult returned no result for task %s", taskId)
	}
	logger.LogDebug(
		ctx,
		"updateVideoSingleTask parsed result: status=%s progress=%s",
		taskResult.Status,
		taskResult.Progress,
	)
	return ApplyTaskPollingResult(ctx, adaptor, task, taskResult, responseBody)
}

func redactVideoResponseBody(body []byte) []byte {
	var m map[string]any
	if err := common.Unmarshal(body, &m); err != nil {
		return body
	}
	resp, _ := m["response"].(map[string]any)
	if resp != nil {
		delete(resp, "bytesBase64Encoded")
		if v, ok := resp["video"].(string); ok {
			resp["video"] = truncateBase64(v)
		}
		if vs, ok := resp["videos"].([]any); ok {
			for i := range vs {
				if vm, ok := vs[i].(map[string]any); ok {
					delete(vm, "bytesBase64Encoded")
				}
			}
		}
	}
	b, err := common.Marshal(m)
	if err != nil {
		return body
	}
	return b
}

func truncateBase64(s string) string {
	const maxKeep = 256
	if len(s) <= maxKeep {
		return s
	}
	return s[:maxKeep] + "..."
}

// taskSettlementFinalizationOnComplete builds the adjustment that must commit
// atomically with a successful terminal task transition.
// 优先级：1. adaptor.AdjustBillingOnComplete 返回正数 → 使用 adaptor 计算的额度
//
//  2. taskResult.TotalTokens > 0 → 按 token 重算
//  3. 都不满足 → 保持预扣额度不变
func taskSettlementFinalizationOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) (*model.TaskBillingFinalizationPayload, error) {
	// 0. 按次计费的任务不做差额结算
	if bc := task.PrivateData.BillingContext; bc != nil && bc.PerCallBilling {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 按次计费，跳过差额结算", task.TaskID))
		return nil, nil
	}
	// 1. 优先让 adaptor 决定最终额度
	if actualQuota := adaptor.AdjustBillingOnComplete(task, taskResult); actualQuota > 0 {
		return taskRecalculationFinalization(task, actualQuota, "adaptor计费调整"), nil
	}
	// 2. 回退到 token 重算
	actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(task, taskResult.TotalTokens)
	if err != nil {
		return nil, err
	}
	if ok {
		return taskRecalculationFinalization(task, actualQuota, reason, clamp), nil
	}
	// 3. 无调整，保持预扣额度
	return nil, nil
}

// settleTaskBillingOnComplete is retained for non-polling callers that already
// own the task lifecycle. The polling path persists the payload together with
// its terminal status instead of invoking this function.
func settleTaskBillingOnComplete(ctx context.Context, adaptor TaskPollingAdaptor, task *model.Task, taskResult *relaycommon.TaskInfo) {
	payload, err := taskSettlementFinalizationOnComplete(ctx, adaptor, task, taskResult)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("计算任务差额结算失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	if payload == nil {
		return
	}
	if _, err := processTaskBillingFinalization(*payload); err != nil {
		logger.LogError(ctx, fmt.Sprintf("持久化任务差额结算失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	if payload.UpdateTaskQuota {
		task.Quota = payload.TargetTaskQuota
	}
}
