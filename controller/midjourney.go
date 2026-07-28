package controller

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"github.com/gin-gonic/gin"
)

// midjourneyPollSummary is the result recorded on a midjourney_poll system task
// row, summarizing one polling pass.
type midjourneyPollSummary struct {
	UnfinishedTasks int `json:"unfinished_tasks"`
	ChannelsScanned int `json:"channels_scanned"`
	NullTasksFailed int `json:"null_tasks_failed"`
}

// runMidjourneyTaskUpdateOnce performs one Midjourney polling pass synchronously.
// It honors ctx cancellation (the system-task runner cancels it when the lease
// is lost) and, when report is non-nil, reports progress as (processedChannels,
// totalChannels) so the system task surfaces a percentage.
func runMidjourneyTaskUpdateOnce(ctx context.Context, report func(processed, total int)) (midjourneyPollSummary, error) {
	summary := midjourneyPollSummary{}
	if ctx == nil {
		ctx = context.Background()
	}

	tasks, err := model.GetAllUnFinishTasks()
	if err != nil {
		return summary, fmt.Errorf("query unfinished Midjourney tasks: %w", err)
	}
	if len(tasks) == 0 {
		return summary, nil
	}
	summary.UnfinishedTasks = len(tasks)

	logger.LogInfo(ctx, fmt.Sprintf("检测到未完成的任务数有: %v", len(tasks)))
	taskChannelM := make(map[int][]string)
	// One upstream task can have multiple local billing rows. Midjourney proxy
	// code 21 explicitly reports an existing task ID, and each accepted local
	// submission is persisted and billed independently. Keep every local row
	// while querying each upstream ID only once per channel.
	taskM := make(map[int]map[string][]*model.Midjourney)
	nullTasks := make([]*model.Midjourney, 0)
	for _, task := range tasks {
		if task.BillingPurpose != "" && !task.BillingFinalized {
			if err := service.ReconcileMidjourneyBilling(task, true); err != nil {
				logger.LogError(ctx, fmt.Sprintf("Reconcile Midjourney task %d billing error: %v", task.Id, err))
				continue
			}
		}
		if task.Progress == "100%" {
			if task.Status == "FAILURE" && task.BillingPurpose != "" && !task.BillingRefunded {
				_ = refundAndRecordFailedMidjourneyTask(ctx, task, task.FailReason)
			}
			continue
		}
		if task.MjId == "" {
			// 统计失败的未完成任务
			nullTasks = append(nullTasks, task)
			continue
		}
		if taskM[task.ChannelId] == nil {
			taskM[task.ChannelId] = make(map[string][]*model.Midjourney)
		}
		if len(taskM[task.ChannelId][task.MjId]) == 0 {
			taskChannelM[task.ChannelId] = append(taskChannelM[task.ChannelId], task.MjId)
		}
		taskM[task.ChannelId][task.MjId] = append(taskM[task.ChannelId][task.MjId], task)
	}
	if len(nullTasks) > 0 {
		summary.NullTasksFailed = len(nullTasks)
		for _, task := range nullTasks {
			preStatus := task.Status
			task.Status = "FAILURE"
			task.Progress = "100%"
			task.FailReason = "Midjourney task ID is empty"
			won, err := task.UpdateWithStatus(preStatus)
			if err != nil {
				logger.LogError(ctx, fmt.Sprintf("Fix null mj_id task %d error: %v", task.Id, err))
				continue
			}
			if won && midjourneyTaskMayHaveCharge(task) {
				_ = refundAndRecordFailedMidjourneyTask(ctx, task, "任务 ID 为空")
			}
		}
	}
	if len(taskChannelM) == 0 {
		return summary, nil
	}

	totalChannels := len(taskChannelM)
	processedChannels := 0
	for channelId, taskIds := range taskChannelM {
		if ctx != nil && ctx.Err() != nil {
			break
		}
		if report != nil {
			report(processedChannels, totalChannels)
		}
		processedChannels++
		summary.ChannelsScanned++
		logger.LogInfo(ctx, fmt.Sprintf("渠道 #%d 未完成的任务有: %d", channelId, len(taskIds)))
		if len(taskIds) == 0 {
			continue
		}
		midjourneyChannel, err := model.CacheGetChannel(channelId)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("CacheGetChannel: %v", err))
			failReason := fmt.Sprintf("获取渠道信息失败，请联系管理员，渠道ID：%d", channelId)
			for _, taskId := range taskIds {
				for _, task := range taskM[channelId][taskId] {
					preStatus := task.Status
					task.FailReason = failReason
					task.Status = "FAILURE"
					task.Progress = "100%"
					won, updateErr := task.UpdateWithStatus(preStatus)
					if updateErr != nil {
						logger.LogError(ctx, fmt.Sprintf("UpdateMidjourneyTask error: %v", updateErr))
						continue
					}
					if won && midjourneyTaskMayHaveCharge(task) {
						_ = refundAndRecordFailedMidjourneyTask(ctx, task, "渠道信息不可用")
					}
				}
			}
			continue
		}

		// A numeric channel ID can be reused after deletion. Reject every task
		// whose creation-time snapshot belongs to an older row before sending
		// any upstream IDs or credentials to the replacement channel.
		pollableTaskIDs := make([]string, 0, len(taskIds))
		for _, taskID := range taskIds {
			localTasks := taskM[channelId][taskID]
			pollableLocalTasks := make([]*model.Midjourney, 0, len(localTasks))
			for _, task := range localTasks {
				if task.ChannelCreatedTime == 0 ||
					task.ChannelCreatedTime == midjourneyChannel.CreatedTime {
					pollableLocalTasks = append(pollableLocalTasks, task)
					continue
				}

				preStatus := task.Status
				task.Status = "FAILURE"
				task.Progress = "100%"
				task.FailReason = fmt.Sprintf(
					"任务原渠道已被替换（channel=%d, expected_created=%d, actual_created=%d）",
					channelId,
					task.ChannelCreatedTime,
					midjourneyChannel.CreatedTime,
				)
				won, updateErr := task.UpdateWithStatus(preStatus)
				if updateErr != nil {
					logger.LogError(ctx, fmt.Sprintf(
						"Reject replacement channel for Midjourney task %d: %v",
						task.Id,
						updateErr,
					))
					continue
				}
				if won && midjourneyTaskMayHaveCharge(task) {
					_ = refundAndRecordFailedMidjourneyTask(ctx, task, task.FailReason)
				}
			}
			if len(pollableLocalTasks) == 0 {
				delete(taskM[channelId], taskID)
				continue
			}
			taskM[channelId][taskID] = pollableLocalTasks
			pollableTaskIDs = append(pollableTaskIDs, taskID)
		}
		taskIds = pollableTaskIDs
		if len(taskIds) == 0 {
			continue
		}

		requestUrl := fmt.Sprintf("%s/mj/task/list-by-condition", midjourneyChannel.GetBaseURL())

		body, err := common.Marshal(map[string]any{
			"ids": taskIds,
		})
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("Get Task marshal body error: %v", err))
			continue
		}
		timeout := time.Second * 15
		requestCtx, cancel := context.WithTimeout(ctx, timeout)
		req, err := http.NewRequestWithContext(requestCtx, "POST", requestUrl, bytes.NewBuffer(body))
		if err != nil {
			cancel()
			logger.LogError(ctx, fmt.Sprintf("Get Task error: %v", service.SanitizeNetworkError(err)))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("mj-api-secret", midjourneyChannel.Key)
		resp, err := service.DoUpstreamRequest(service.GetHttpClient(), req)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("Get Task Do req error: %v", err))
			cancel()
			continue
		}
		if resp.StatusCode != http.StatusOK {
			logger.LogError(ctx, fmt.Sprintf("Get Task status code: %d", resp.StatusCode))
			resp.Body.Close()
			cancel()
			continue
		}
		responseBody, err := service.ReadUpstreamResponseBody(resp.Body)
		if err != nil {
			logger.LogError(ctx, fmt.Sprintf("Get Mjp Task parse body error: %v", err))
			resp.Body.Close()
			cancel()
			continue
		}
		var responseItems []dto.MidjourneyDto
		err = common.Unmarshal(responseBody, &responseItems)
		if err != nil {
			logger.LogError(
				ctx,
				fmt.Sprintf(
					"Get Mjp Task parse body error: %v, status=%d response_bytes=%d",
					err,
					resp.StatusCode,
					len(responseBody),
				),
			)
			resp.Body.Close()
			cancel()
			continue
		}
		resp.Body.Close()
		req.Body.Close()
		cancel()

		for _, responseItem := range responseItems {
			localTasks := taskM[channelId][responseItem.MjId]
			if len(localTasks) == 0 {
				logger.LogWarn(ctx, fmt.Sprintf("Midjourney task response ignored: unknown mj_id=%s", responseItem.MjId))
				continue
			}

			for _, task := range localTasks {
				localResponse := responseItem
				useTime := (time.Now().UnixNano() / int64(time.Millisecond)) - task.SubmitTime
				// 如果时间超过一小时，且进度不是100%，则认为任务失败
				if useTime > 3600000 && task.Progress != "100%" {
					localResponse.FailReason = "上游任务超时（超过1小时）"
					localResponse.Status = "FAILURE"
				}
				if !checkMjTaskNeedUpdate(task, localResponse) {
					continue
				}
				preStatus := task.Status
				task.Code = 1
				task.Progress = localResponse.Progress
				task.PromptEn = localResponse.PromptEn
				task.State = localResponse.State
				task.SubmitTime = localResponse.SubmitTime
				task.StartTime = localResponse.StartTime
				task.FinishTime = localResponse.FinishTime
				task.ImageUrl = localResponse.ImageUrl
				task.Status = localResponse.Status
				task.FailReason = localResponse.FailReason
				if localResponse.Properties != nil {
					propertiesStr, _ := common.Marshal(localResponse.Properties)
					task.Properties = string(propertiesStr)
				}
				if localResponse.Buttons != nil {
					buttonStr, _ := common.Marshal(localResponse.Buttons)
					task.Buttons = string(buttonStr)
				}
				// 映射 VideoUrl
				task.VideoUrl = localResponse.VideoUrl

				// 映射 VideoUrls - 将数组序列化为 JSON 字符串
				if localResponse.VideoUrls != nil && len(localResponse.VideoUrls) > 0 {
					videoUrlsStr, err := common.Marshal(localResponse.VideoUrls)
					if err != nil {
						logger.LogError(ctx, fmt.Sprintf("序列化 VideoUrls 失败: %v", err))
						task.VideoUrls = "[]" // 失败时设置为空数组
					} else {
						task.VideoUrls = string(videoUrlsStr)
					}
				} else {
					task.VideoUrls = "" // 空值时清空字段
				}

				shouldReturnQuota := false
				if (task.Progress != "100%" && localResponse.FailReason != "") || (task.Progress == "100%" && task.Status == "FAILURE") {
					logger.LogInfo(ctx, task.MjId+" 构建失败，"+task.FailReason)
					task.Progress = "100%"
					if midjourneyTaskMayHaveCharge(task) {
						shouldReturnQuota = true
					}
				}
				won, err := task.UpdateWithStatus(preStatus)
				if err != nil {
					logger.LogError(ctx, "UpdateMidjourneyTask task error: "+err.Error())
				} else if won && shouldReturnQuota {
					_ = refundAndRecordFailedMidjourneyTask(ctx, task, "构图失败")
				}
			}
		}
	}
	if report != nil && (ctx == nil || ctx.Err() == nil) {
		report(totalChannels, totalChannels)
	}
	if err := ctx.Err(); err != nil {
		return summary, err
	}
	return summary, nil
}

func midjourneyTaskMayHaveCharge(task *model.Midjourney) bool {
	if task == nil {
		return false
	}
	// These actions have always bypassed quota consumption. Older rows do not
	// carry billing context, so the action is the only reliable way to avoid
	// trying to reconcile a charge that never existed.
	if task.Action == constant.MjActionInPaint || task.Action == constant.MjActionCustomZoom {
		return false
	}
	if task.BillingRequestId != "" || task.BillingPurpose != "" {
		return true
	}
	// Accepted legacy submissions may have been charged, so surface them for
	// manual reconciliation. refundFailedMidjourneyTask deliberately refuses to
	// create a credit without persisted proof of the original charge.
	return task.Code == 1 || task.Code == 21 || task.Code == 22
}

func refundAndRecordFailedMidjourneyTask(ctx context.Context, task *model.Midjourney, reason string) error {
	_, _, err := refundFailedMidjourneyTask(task)
	if err != nil {
		logger.LogError(ctx, "Midjourney task refund failed or queued: "+err.Error())
		return err
	}
	if _, err := model.FinalizeMidjourneyBillingRefund(task.Id, task.BillingTaskId, reason); err != nil {
		logger.LogError(ctx, "Midjourney task refund finalization failed: "+err.Error())
		return err
	}
	task.BillingRefunded = true
	return nil
}

func refundFailedMidjourneyTask(task *model.Midjourney) (model.BillingAdjustmentResult, bool, error) {
	if task == nil {
		return model.BillingAdjustmentResult{}, false, fmt.Errorf("Midjourney task is nil")
	}
	if task.Quota < 0 {
		return model.BillingAdjustmentResult{}, false, fmt.Errorf("Midjourney task %d has negative quota", task.Id)
	}
	if task.Quota == 0 && task.BillingPurpose == "" {
		return model.BillingAdjustmentResult{}, false, nil
	}
	if task.BillingRequestId == "" {
		if task.BillingPurpose != "" {
			return model.BillingAdjustmentResult{}, false, fmt.Errorf("Midjourney task %d is missing its billing request ID", task.Id)
		}
		return model.BillingAdjustmentResult{}, false, fmt.Errorf("Midjourney task %d has no verifiable billing charge; manual reconciliation is required", task.Id)
	}

	billingSource := task.BillingSource
	if billingSource == "" {
		billingSource = service.BillingSourceWallet
	}
	if billingSource != service.BillingSourceWallet && billingSource != service.BillingSourceSubscription {
		return model.BillingAdjustmentResult{}, false, fmt.Errorf("Midjourney task %d has invalid billing source %q", task.Id, task.BillingSource)
	}
	task.BillingSource = billingSource
	relayInfo := &relaycommon.RelayInfo{
		RequestId:      task.BillingRequestId,
		UserId:         task.UserId,
		TokenId:        task.BillingTokenId,
		BillingSource:  billingSource,
		SubscriptionId: task.BillingSubscriptionId,
		IsPlayground:   task.BillingIsPlayground,
	}

	// New tasks persist a request ID even when the upstream response was not
	// billable. An empty purpose therefore means there is no charge to undo.
	if task.BillingPurpose == "" {
		return model.BillingAdjustmentResult{}, false, nil
	}
	return service.ReverseDurableQuotaAdjustment(
		relayInfo,
		task.BillingPurpose,
		fmt.Sprintf("midjourney-failure-refund:%d", task.Id),
	)
}

func checkMjTaskNeedUpdate(oldTask *model.Midjourney, newTask dto.MidjourneyDto) bool {
	if oldTask.Code != 1 {
		return true
	}
	if oldTask.Progress != newTask.Progress {
		return true
	}
	if oldTask.PromptEn != newTask.PromptEn {
		return true
	}
	if oldTask.State != newTask.State {
		return true
	}
	if oldTask.SubmitTime != newTask.SubmitTime {
		return true
	}
	if oldTask.StartTime != newTask.StartTime {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if oldTask.ImageUrl != newTask.ImageUrl {
		return true
	}
	if oldTask.Status != newTask.Status {
		return true
	}
	if oldTask.FailReason != newTask.FailReason {
		return true
	}
	if oldTask.FinishTime != newTask.FinishTime {
		return true
	}
	if oldTask.Progress != "100%" && newTask.FailReason != "" {
		return true
	}
	// 检查 VideoUrl 是否需要更新
	if oldTask.VideoUrl != newTask.VideoUrl {
		return true
	}
	// 检查 VideoUrls 是否需要更新
	if newTask.VideoUrls != nil && len(newTask.VideoUrls) > 0 {
		newVideoUrlsStr, _ := common.Marshal(newTask.VideoUrls)
		if oldTask.VideoUrls != string(newVideoUrlsStr) {
			return true
		}
	} else if oldTask.VideoUrls != "" {
		// 如果新数据没有 VideoUrls 但旧数据有，需要更新（清空）
		return true
	}

	return false
}

func GetAllMidjourney(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	// 解析其他查询参数
	queryParams := model.TaskQueryParams{
		ChannelID:      c.Query("channel_id"),
		MjID:           c.Query("mj_id"),
		StartTimestamp: c.Query("start_timestamp"),
		EndTimestamp:   c.Query("end_timestamp"),
	}

	items, err := model.GetAllTasks(pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, err := model.CountAllTasks(queryParams)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	if setting.IsMjForwardURLEnabled() {
		for i, midjourney := range items {
			midjourney.ImageUrl = system_setting.GetServerAddress() + "/mj/image/" + midjourney.MjId
			items[i] = midjourney
		}
	}
	pageInfo.SetTotal(total)
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}

func GetUserMidjourney(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)

	userId := c.GetInt("id")

	queryParams := model.TaskQueryParams{
		MjID:           c.Query("mj_id"),
		StartTimestamp: c.Query("start_timestamp"),
		EndTimestamp:   c.Query("end_timestamp"),
	}

	items, err := model.GetAllUserTask(userId, pageInfo.GetStartIdx(), pageInfo.GetPageSize(), queryParams)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	total, err := model.CountAllUserTask(userId, queryParams)
	if err != nil {
		common.ApiError(c, err)
		return
	}

	if setting.IsMjForwardURLEnabled() {
		for i, midjourney := range items {
			midjourney.ImageUrl = system_setting.GetServerAddress() + "/mj/image/" + midjourney.MjId
			items[i] = midjourney
		}
	}
	pageInfo.SetTotal(total)
	pageInfo.SetItems(items)
	common.ApiSuccess(c, pageInfo)
}
