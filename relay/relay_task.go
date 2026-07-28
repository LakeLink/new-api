package relay

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/relay/channel"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type TaskSubmitResult struct {
	UpstreamTaskID string
	TaskData       []byte
	Platform       constant.TaskPlatform
	Quota          int
	//PerCallPrice   types.PriceData
}

// ResolveOriginTask 处理基于已有任务的提交（remix / continuation）：
// 查找原始任务、从中提取模型名称、将渠道锁定到原始任务的渠道
// （通过 info.LockedChannel，并固定到拥有原始任务的 provider key），
// 以及提取 OtherRatios（时长、分辨率）。
// 该函数在控制器的重试循环之前调用一次，其结果通过 info 字段和上下文持久化。
func ResolveOriginTask(c *gin.Context, info *relaycommon.RelayInfo) *dto.TaskError {
	// 检测 remix action
	path := c.Request.URL.Path
	if strings.Contains(path, "/v1/videos/") && strings.HasSuffix(path, "/remix") {
		info.Action = constant.TaskActionRemix
	}

	// 提取 remix 任务的 video_id
	if info.Action == constant.TaskActionRemix {
		videoID := c.Param("video_id")
		if strings.TrimSpace(videoID) == "" {
			return service.TaskErrorWrapperLocal(fmt.Errorf("video_id is required"), "invalid_request", http.StatusBadRequest)
		}
		info.OriginTaskID = videoID
	}

	if info.OriginTaskID == "" {
		return nil
	}

	// 查找原始任务
	originTask, exist, err := model.GetByTaskId(info.UserId, info.OriginTaskID)
	if err != nil {
		return service.TaskErrorWrapper(err, "get_origin_task_failed", http.StatusInternalServerError)
	}
	if !exist {
		return service.TaskErrorWrapperLocal(errors.New("task_origin_not_exist"), "task_not_exist", http.StatusBadRequest)
	}
	if originTask.Status != model.TaskStatusSuccess {
		return service.TaskErrorWrapperLocal(
			errors.New("only a completed task can be remixed"),
			"origin_task_not_completed",
			http.StatusBadRequest,
		)
	}
	// Clients address the local public task ID, while the provider remix
	// endpoint requires the account-scoped upstream ID. Legacy rows fall back
	// to TaskID through GetUpstreamTaskID.
	info.OriginTaskID = originTask.GetUpstreamTaskID()

	// A remix inherits the origin task's model. Allowing a client-supplied
	// replacement would apply the origin's duration/size billing snapshot to a
	// different model price and can also violate the provider's remix contract.
	originModelName := originTask.Properties.OriginModelName
	if originModelName == "" {
		originModelName = originTask.Properties.UpstreamModelName
	}
	if originModelName == "" {
		var taskData map[string]interface{}
		_ = common.Unmarshal(originTask.Data, &taskData)
		if modelName, ok := taskData["model"].(string); ok {
			originModelName = modelName
		}
	}
	if info.OriginModelName != "" && originModelName != "" && info.OriginModelName != originModelName {
		return service.TaskErrorWrapperLocal(
			errors.New("remix model does not match the origin task"),
			"origin_task_model_mismatch",
			http.StatusBadRequest,
		)
	}
	info.OriginModelName = originModelName
	info.OriginTaskUpstreamModelName = originTask.Properties.UpstreamModelName

	// 锁定到原始任务的渠道和 provider account。
	ch, err := model.GetChannelById(originTask.ChannelId, true)
	if err != nil {
		return service.TaskErrorWrapperLocal(err, "channel_not_found", http.StatusBadRequest)
	}
	if ch.Status != common.ChannelStatusEnabled {
		return service.TaskErrorWrapperLocal(errors.New("the channel of the origin task is disabled"), "task_channel_disable", http.StatusBadRequest)
	}
	if originTask.PrivateData.ChannelCreatedTime != 0 &&
		originTask.PrivateData.ChannelCreatedTime != ch.CreatedTime {
		return service.TaskErrorWrapperLocal(
			errors.New("the channel of the origin task was replaced"),
			"origin_task_channel_changed",
			http.StatusBadRequest,
		)
	}
	if originTask.PrivateData.RoutingSnapshotVersion > 0 {
		if originTask.PrivateData.ChannelType != ch.Type ||
			originTask.PrivateData.ChannelBaseURL != ch.GetBaseURL() {
			return service.TaskErrorWrapperLocal(
				errors.New("the provider route of the origin task is no longer configured"),
				"origin_task_route_changed",
				http.StatusBadRequest,
			)
		}
		pinnedKeyIndex := 0
		if ch.ChannelInfo.IsMultiKey {
			pinnedKeyIndex = -1
			for index, key := range ch.GetKeys() {
				if key == originTask.PrivateData.Key {
					pinnedKeyIndex = index
					break
				}
			}
		} else if ch.Key != originTask.PrivateData.Key {
			pinnedKeyIndex = -1
		}
		pinnedKeyStatus, hasPinnedKeyStatus := 0, false
		if ch.ChannelInfo.IsMultiKey {
			pinnedKeyStatus, hasPinnedKeyStatus = ch.ChannelInfo.MultiKeyStatusList[pinnedKeyIndex]
		}
		if pinnedKeyIndex < 0 ||
			(hasPinnedKeyStatus && pinnedKeyStatus != common.ChannelStatusEnabled) {
			return service.TaskErrorWrapperLocal(
				errors.New("the provider account of the origin task is no longer available"),
				"origin_task_account_unavailable",
				http.StatusBadRequest,
			)
		}
		info.LockedChannelKey = originTask.PrivateData.Key
		info.LockedChannelKeyIndex = pinnedKeyIndex
		info.LockedChannelKeyPinned = true
	}
	info.LockedChannel = ch

	// 提取 remix 参数（时长、分辨率 → OtherRatios）
	if info.Action == constant.TaskActionRemix {
		var originTaskPriceData types.PriceData
		if originTask.PrivateData.BillingContext != nil {
			// 新的 remix 逻辑：直接从原始任务的 BillingContext 中提取 OtherRatios（如果存在）
			for s, f := range originTask.PrivateData.BillingContext.OtherRatios {
				originTaskPriceData.AddOtherRatio(s, f)
				if persisted, ok := originTaskPriceData.OtherRatios()[s]; !ok || persisted != f {
					return service.TaskErrorWrapperLocal(
						errors.New("origin task contains an invalid billing multiplier"),
						"invalid_origin_task_billing",
						http.StatusBadRequest,
					)
				}
			}
		} else {
			// 旧的 remix 逻辑：直接从 task data 解析 seconds 和 size（如果存在）
			var taskData map[string]interface{}
			_ = common.Unmarshal(originTask.Data, &taskData)
			secondsStr, _ := taskData["seconds"].(string)
			seconds, _ := strconv.Atoi(secondsStr)
			if seconds <= 0 {
				seconds = 4
			}
			// 历史任务数据可能包含未经校验的时长，作为计费乘数前必须钳制
			if seconds > relaycommon.MaxTaskDurationSeconds {
				seconds = relaycommon.MaxTaskDurationSeconds
			}
			sizeStr, _ := taskData["size"].(string)
			originTaskPriceData.AddOtherRatio("seconds", float64(seconds))
			originTaskPriceData.AddOtherRatio("size", 1)
			if sizeStr == "1792x1024" || sizeStr == "1024x1792" {
				originTaskPriceData.AddOtherRatio("size", 1.666667)
			}
		}
		// ResolveOriginTask runs once before the controller retry loop. Freeze
		// the origin-derived ratios now; PriceData itself is rebuilt and mutated
		// for every provider attempt.
		if !info.OriginTaskBillingRatiosInitialized {
			info.OriginTaskBillingRatios = originTaskPriceData.OtherRatios()
			info.OriginTaskBillingRatiosInitialized = true
		}
		info.PriceData.ReplaceOtherRatios(info.OriginTaskBillingRatios)
	}

	return nil
}

// RelayTaskSubmit 完成 task 提交的全部流程（每次尝试调用一次）：
// 刷新渠道元数据 → 确定 platform/adaptor → 解析请求和确定 action → 映射模型 →
// 按最终模型验证请求 →
// 估算计费(EstimateBilling) → 计算价格 → 预扣费（仅首次）→
// 构建/发送/解析上游请求 → 提交后计费调整(AdjustBillingOnSubmit)。
// 控制器负责 defer Refund 和成功后 Settle。
func RelayTaskSubmit(c *gin.Context, info *relaycommon.RelayInfo) (*TaskSubmitResult, *dto.TaskError) {
	info.InitChannelMeta(c)

	// 1. 确定 platform → 创建适配器 → 验证请求
	platform := constant.TaskPlatform(c.GetString("platform"))
	if platform == "" {
		platform = GetTaskPlatform(c)
	}
	adaptor := GetTaskAdaptor(platform)
	if adaptor == nil {
		return nil, service.TaskErrorWrapperLocal(fmt.Errorf("invalid api platform: %s", platform), "invalid_api_platform", http.StatusBadRequest)
	}
	adaptor.Init(info)
	if taskErr := adaptor.ValidateRequestAndSetAction(c, info); taskErr != nil {
		return nil, taskErr
	}

	// 2. 确定模型名称
	modelName := info.OriginModelName
	if modelName == "" {
		modelName = service.CoverTaskActionToModelName(platform, info.Action)
	}

	// 2.5 应用渠道的模型映射（与同步任务对齐）
	info.OriginModelName = modelName
	info.UpstreamModelName = modelName
	if err := helper.ModelMappedHelper(c, info, nil); err != nil {
		return nil, service.TaskErrorWrapperLocal(err, "model_mapping_failed", http.StatusBadRequest)
	}
	if info.Action == constant.TaskActionRemix &&
		info.OriginTaskUpstreamModelName != "" &&
		info.UpstreamModelName != info.OriginTaskUpstreamModelName {
		return nil, service.TaskErrorWrapperLocal(
			errors.New("the origin task's upstream model mapping has changed"),
			"origin_task_model_mapping_changed",
			http.StatusBadRequest,
		)
	}
	if taskErr := adaptor.ValidateFinalRequest(c, info); taskErr != nil {
		return nil, taskErr
	}

	// 3. 预生成公开 task ID（仅首次）
	if info.PublicTaskID == "" {
		info.PublicTaskID = model.GenerateTaskID()
	}

	// 4. 价格计算：基础模型价格。Only remix requests inherit the immutable
	// ratios captured from their origin task. Attempt-specific estimates must
	// never become inherited inputs for a later retry.
	info.OriginModelName = modelName
	priceData, err := helper.ModelPriceHelperPerCall(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "model_price_error", http.StatusBadRequest)
	}
	if !beginTaskBillingAttempt(info, priceData) {
		return nil, service.TaskErrorWrapperLocal(
			errors.New("origin task contains an invalid billing multiplier"),
			"invalid_origin_task_billing",
			http.StatusBadRequest,
		)
	}

	// 5. 计费估算：让适配器根据用户请求提供 OtherRatios（时长、分辨率等）
	//    必须在 ModelPriceHelperPerCall 之后调用（它会重建 PriceData）。
	//    ResolveOriginTask 可能已在 remix 路径中预设了 OtherRatios，此处合并。
	if !mergeTaskBillingRatios(&info.PriceData, nil, adaptor.EstimateBilling(c, info)) {
		return nil, service.TaskErrorWrapperLocal(
			errors.New("task billing multiplier is outside the supported range"),
			"invalid_billing_parameters",
			http.StatusBadRequest,
		)
	}

	// 6. 将 OtherRatios 应用到基础额度（饱和转换，防止溢出成负数）
	if !common.StringsContains(constant.TaskPricePatches, modelName) {
		quota, clamp := calculateTaskQuotaWithRatios(float64(info.PriceData.Quota), &info.PriceData)
		noteTaskQuotaClamp(info, clamp)
		if clamp != nil {
			// A saturated pre-consume can still be affordable to an account at
			// the storage ceiling. Reject it before dispatch instead of sending
			// an upstream job whose real cost cannot be represented.
			return nil, service.TaskErrorWrapperLocal(
				clamp,
				"task_billing_out_of_range",
				http.StatusBadRequest,
			)
		}
		info.PriceData.Quota = quota
	}

	// 7. 预扣费。每次重试都可能选中不同的渠道、模型映射或计费倍率；
	// 已存在的会话也必须扩展到本次尝试重新计算出的额度后才能发送。
	if !info.PriceData.FreeModel {
		if info.Billing == nil {
			info.ForcePreConsume = true
			if apiErr := service.PreConsumeBilling(c, info.PriceData.Quota, info); apiErr != nil {
				return nil, service.TaskErrorFromAPIError(apiErr)
			}
		} else if err := info.Billing.Reserve(info.PriceData.Quota); err != nil {
			var apiErr *types.NewAPIError
			if errors.As(err, &apiErr) {
				return nil, service.TaskErrorFromAPIError(apiErr)
			}
			return nil, service.TaskErrorWrapperLocal(err, "reserve_task_quota_failed", http.StatusForbidden)
		}
	}

	// 8. 构建请求体
	requestBody, err := adaptor.BuildRequestBody(c, info)
	if err != nil {
		return nil, service.TaskErrorWrapper(err, "build_request_failed", http.StatusInternalServerError)
	}

	// 9. 发送请求
	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return nil, taskSubmitRequestError(resp, err)
	}
	if taskErr := validateTaskSubmitUpstreamResponse(resp); taskErr != nil {
		return nil, taskErr
	}
	// RelayTaskSubmit owns every successful upstream response for the duration
	// of adaptor parsing. Centralized closure also covers adaptor read errors.
	defer service.CloseResponseBodyGracefully(resp)

	// 10. 返回 OtherRatios 给下游（header 必须在 DoResponse 写 body 之前设置）
	otherRatios := info.PriceData.OtherRatios()
	if otherRatios == nil {
		otherRatios = map[string]float64{}
	}
	ratiosJSON, _ := common.Marshal(otherRatios)
	c.Header("X-New-Api-Other-Ratios", string(ratiosJSON))

	// 11. 解析响应
	upstreamTaskID, taskData, taskErr := parseAcceptedTaskSubmitResponse(c, adaptor, resp, info)
	if taskErr != nil {
		return nil, taskErr
	}

	// 11. 提交后计费调整：让适配器根据上游实际返回调整 OtherRatios
	finalQuota := info.PriceData.Quota
	if adjustedRatios := adaptor.AdjustBillingOnSubmit(info, taskData); len(adjustedRatios) > 0 {
		if adjustedQuota, ok := recalcQuotaFromRatios(info, adjustedRatios); ok {
			// 基于调整后的 ratios 重新计算 quota
			finalQuota = adjustedQuota
			info.PriceData.ReplaceOtherRatios(adjustedRatios)
			info.PriceData.Quota = finalQuota
		}
	}

	return &TaskSubmitResult{
		UpstreamTaskID: upstreamTaskID,
		TaskData:       taskData,
		Platform:       platform,
		Quota:          finalQuota,
	}, nil
}

// mergeTaskBillingRatios preserves ratios inherited from an origin task while
// allowing values explicitly estimated for the current attempt to take
// precedence for the same billing dimension.
func mergeTaskBillingRatios(priceData *types.PriceData, inherited, estimated map[string]float64) bool {
	if priceData == nil {
		return false
	}
	valid := true
	for key, ratio := range inherited {
		priceData.AddOtherRatio(key, ratio)
		if persisted, ok := priceData.OtherRatios()[key]; !ok || persisted != ratio {
			valid = false
		}
	}
	for key, ratio := range estimated {
		priceData.AddOtherRatio(key, ratio)
		if persisted, ok := priceData.OtherRatios()[key]; !ok || persisted != ratio {
			valid = false
		}
	}
	return valid
}

// beginTaskBillingAttempt rebuilds the mutable attempt PriceData from a fresh
// model-price result plus the one-time origin-task snapshot. This is the retry
// boundary that prevents stale adaptor estimates from crossing attempts.
func beginTaskBillingAttempt(info *relaycommon.RelayInfo, priceData types.PriceData) bool {
	if info == nil {
		return false
	}
	info.PriceData = priceData
	if info.TaskRelayInfo == nil ||
		info.Action != constant.TaskActionRemix ||
		!info.OriginTaskBillingRatiosInitialized {
		return true
	}
	return mergeTaskBillingRatios(&info.PriceData, info.OriginTaskBillingRatios, nil)
}

// parseAcceptedTaskSubmitResponse marks every response-processing error as
// non-retryable. A 2xx means the provider may already have created and billed
// the task, even when its response body is malformed or lacks a task ID.
func parseAcceptedTaskSubmitResponse(
	c *gin.Context,
	adaptor channel.TaskAdaptor,
	resp *http.Response,
	info *relaycommon.RelayInfo,
) (string, []byte, *dto.TaskError) {
	taskID, taskData, taskErr := adaptor.DoResponse(c, resp, info)
	if taskErr != nil {
		taskErr.SkipRetry = true
		return taskID, taskData, taskErr
	}
	if strings.TrimSpace(taskID) == "" {
		taskErr = service.TaskErrorWrapper(
			errors.New("task upstream returned an empty task ID"),
			"invalid_response",
			http.StatusBadGateway,
		)
		taskErr.SkipRetry = true
	}
	return taskID, taskData, taskErr
}

// taskSubmitRequestError owns any response returned alongside a transport
// error. Although net/http usually closes redirect-error bodies itself, task
// adaptors are allowed to implement DoRequest directly. A non-nil 2xx response
// also crosses the accepted boundary and must not be submitted again.
func taskSubmitRequestError(resp *http.Response, err error) *dto.TaskError {
	taskErr := service.TaskErrorWrapper(err, "do_request_failed", http.StatusInternalServerError)
	if resp == nil {
		return taskErr
	}
	service.CloseResponseBodyGracefully(resp)
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		taskErr.SkipRetry = true
	}
	return taskErr
}

const (
	maxTaskSubmitErrorBodyBytes = int64(1 << 20)
	taskSubmitErrorTruncated    = "\n[upstream error body truncated]"
)

// validateTaskSubmitUpstreamResponse rejects unusable responses before they
// reach a task adaptor. Error responses are consumed and closed here because
// no adaptor will take ownership of them.
func validateTaskSubmitUpstreamResponse(resp *http.Response) *dto.TaskError {
	if resp == nil {
		return service.TaskErrorWrapperLocal(errors.New("task upstream returned a nil response"), "empty_upstream_response", http.StatusBadGateway)
	}
	if resp.Body == nil {
		taskErr := service.TaskErrorWrapperLocal(errors.New("task upstream returned a nil response body"), "empty_upstream_response", http.StatusBadGateway)
		if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
			// A successful status means the provider may already have created
			// the task even though its response violates net/http's non-nil
			// Body contract.
			taskErr.SkipRetry = true
		}
		return taskErr
	}
	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	statusCode := resp.StatusCode
	if statusCode < 100 || statusCode > 599 {
		statusCode = http.StatusBadGateway
	}

	defer service.CloseResponseBodyGracefully(resp)
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, maxTaskSubmitErrorBodyBytes+1))
	if err != nil {
		return service.TaskErrorWrapper(err, "read_upstream_response_failed", http.StatusBadGateway)
	}
	truncated := int64(len(responseBody)) > maxTaskSubmitErrorBodyBytes
	if truncated {
		responseBody = responseBody[:maxTaskSubmitErrorBodyBytes]
	}
	message := strings.TrimSpace(string(responseBody))
	if message == "" {
		message = http.StatusText(statusCode)
	}
	if truncated {
		message += taskSubmitErrorTruncated
	}
	return service.TaskErrorWrapper(errors.New(message), "fail_to_fetch_task", statusCode)
}

// recalcQuotaFromRatios 根据 adjustedRatios 重新计算 quota。
// 公式: baseQuota × ∏(ratio) — 其中 baseQuota 是不含 OtherRatios 的基础额度。
func recalcQuotaFromRatios(info *relaycommon.RelayInfo, ratios map[string]float64) (int, bool) {
	// 从 PriceData 获取不含 OtherRatios 的基础价格
	baseQuota := info.PriceData.RemoveOtherRatiosFromFloat(float64(info.PriceData.Quota))
	priceData := info.PriceData
	if !priceData.ReplaceOtherRatios(ratios) || len(priceData.OtherRatios()) != len(ratios) {
		return 0, false
	}
	// 应用新的 ratios
	quota, clamp := calculateTaskQuotaWithRatios(baseQuota, &priceData)
	noteTaskQuotaClamp(info, clamp)
	if clamp != nil {
		// This hook runs after the provider accepted the task. Preserve the
		// validated pre-dispatch estimate and persist the saturation marker;
		// never turn an unrepresentable provider claim into a MaxQuota charge.
		return 0, false
	}
	return quota, true
}

func calculateTaskQuotaWithRatios(baseQuota float64, priceData *types.PriceData) (int, *common.QuotaClamp) {
	if priceData == nil {
		return 0, nil
	}
	return common.QuotaFromFloatChecked(priceData.ApplyOtherRatiosToFloat(baseQuota))
}

// noteTaskQuotaClamp records the first quota saturation event onto the task's
// RelayInfo so LogTaskConsumption can surface it on the submit log's
// admin_info. First non-nil clamp wins.
func noteTaskQuotaClamp(info *relaycommon.RelayInfo, clamp *common.QuotaClamp) {
	if clamp == nil || info == nil {
		return
	}
	if info.QuotaClamp == nil {
		info.QuotaClamp = clamp
	}
}

var fetchRespBuilders = map[int]func(c *gin.Context) (respBody []byte, taskResp *dto.TaskError){
	relayconstant.RelayModeSunoFetchByID:  sunoFetchByIDRespBodyBuilder,
	relayconstant.RelayModeSunoFetch:      sunoFetchRespBodyBuilder,
	relayconstant.RelayModeVideoFetchByID: videoFetchByIDRespBodyBuilder,
}

func RelayTaskFetch(c *gin.Context, relayMode int) (taskResp *dto.TaskError) {
	respBuilder, ok := fetchRespBuilders[relayMode]
	if !ok {
		return service.TaskErrorWrapperLocal(errors.New("invalid_relay_mode"), "invalid_relay_mode", http.StatusBadRequest)
	}

	respBody, taskErr := respBuilder(c)
	if taskErr != nil {
		return taskErr
	}
	if len(respBody) == 0 {
		respBody = []byte("{\"code\":\"success\",\"data\":null}")
	}

	c.Writer.Header().Set("Content-Type", "application/json")
	_, err := io.Copy(c.Writer, bytes.NewBuffer(respBody))
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "copy_response_body_failed", http.StatusInternalServerError)
		return
	}
	return
}

func sunoFetchRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	userId := c.GetInt("id")
	var condition = struct {
		IDs    []any  `json:"ids"`
		Action string `json:"action"`
	}{}
	err := c.BindJSON(&condition)
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "invalid_request", http.StatusBadRequest)
		return
	}
	var tasks []any
	if len(condition.IDs) > 0 {
		taskModels, err := model.GetByTaskIds(userId, condition.IDs)
		if err != nil {
			taskResp = service.TaskErrorWrapper(err, "get_tasks_failed", http.StatusInternalServerError)
			return
		}
		for _, task := range taskModels {
			tasks = append(tasks, TaskModel2Dto(task))
		}
	} else {
		tasks = make([]any, 0)
	}
	respBody, err = common.Marshal(dto.TaskResponse[[]any]{
		Code: "success",
		Data: tasks,
	})
	return
}

func sunoFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("id")
	userId := c.GetInt("id")

	originTask, exist, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
		return
	}
	if !exist {
		taskResp = service.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusBadRequest)
		return
	}

	respBody, err = common.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: TaskModel2Dto(originTask),
	})
	return
}

func videoFetchByIDRespBodyBuilder(c *gin.Context) (respBody []byte, taskResp *dto.TaskError) {
	taskId := c.Param("task_id")
	if taskId == "" {
		taskId = c.GetString("task_id")
	}
	userId := c.GetInt("id")

	originTask, exist, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "get_task_failed", http.StatusInternalServerError)
		return
	}
	if !exist {
		taskResp = service.TaskErrorWrapperLocal(errors.New("task_not_exist"), "task_not_exist", http.StatusBadRequest)
		return
	}

	isOpenAIVideoAPI := strings.HasPrefix(c.Request.RequestURI, "/v1/videos/")

	// Gemini/Vertex 支持实时查询：用户 fetch 时直接从上游拉取最新状态
	if realtimeResp := tryRealtimeFetch(c.Request.Context(), originTask, isOpenAIVideoAPI); len(realtimeResp) > 0 {
		respBody = realtimeResp
		return
	}

	// OpenAI Video API 格式: 走各 adaptor 的 ConvertToOpenAIVideo
	if isOpenAIVideoAPI {
		adaptor := GetTaskAdaptor(originTask.Platform)
		if adaptor == nil {
			taskResp = service.TaskErrorWrapperLocal(fmt.Errorf("invalid channel id: %d", originTask.ChannelId), "invalid_channel_id", http.StatusBadRequest)
			return
		}
		if converter, ok := adaptor.(channel.OpenAIVideoConverter); ok {
			openAIVideoData, err := converter.ConvertToOpenAIVideo(originTask)
			if err != nil {
				taskResp = service.TaskErrorWrapper(err, "convert_to_openai_video_failed", http.StatusInternalServerError)
				return
			}
			respBody = openAIVideoData
			return
		}
		taskResp = service.TaskErrorWrapperLocal(fmt.Errorf("not_implemented:%s", originTask.Platform), "not_implemented", http.StatusNotImplemented)
		return
	}

	// 通用 TaskDto 格式
	respBody, err = common.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: TaskModel2Dto(originTask),
	})
	if err != nil {
		taskResp = service.TaskErrorWrapper(err, "marshal_response_failed", http.StatusInternalServerError)
	}
	return
}

// tryRealtimeFetch 尝试从上游实时拉取 Gemini/Vertex 任务状态。
// 仅当渠道类型为 Gemini 或 Vertex 时触发；其他渠道或出错时返回 nil。
// 当非 OpenAI Video API 时，还会构建自定义格式的响应体。
func tryRealtimeFetch(ctx context.Context, task *model.Task, isOpenAIVideoAPI bool) []byte {
	channelModel, err := model.GetChannelById(task.ChannelId, true)
	if err != nil {
		return nil
	}
	if channelModel.Type != constant.ChannelTypeVertexAi && channelModel.Type != constant.ChannelTypeGemini {
		return nil
	}
	if task.PrivateData.ChannelCreatedTime != 0 &&
		task.PrivateData.ChannelCreatedTime != channelModel.CreatedTime {
		// A hard-deleted channel can be recreated with the same numeric ID.
		// Never query a replacement provider account for an older task; the
		// background poller will atomically fail and refund this task.
		return nil
	}

	baseURL := constant.ChannelBaseURLs[channelModel.Type]
	if channelModel.GetBaseURL() != "" {
		baseURL = channelModel.GetBaseURL()
	}
	proxy := channelModel.GetSetting().Proxy
	adaptor := GetTaskAdaptor(constant.TaskPlatform(strconv.Itoa(channelModel.Type)))
	if adaptor == nil {
		return nil
	}

	key := channelModel.Key
	if task.PrivateData.RoutingSnapshotVersion > 0 {
		baseURL = task.PrivateData.ChannelBaseURL
		key = task.PrivateData.Key
		proxy = task.PrivateData.ChannelProxy
	} else if task.PrivateData.Key != "" {
		key = task.PrivateData.Key
	}
	resp, err := adaptor.FetchTask(ctx, baseURL, key, map[string]any{
		"task_id": task.GetUpstreamTaskID(),
		"action":  task.Action,
	}, proxy)
	if err != nil || resp == nil {
		return nil
	}
	if resp.Body == nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil
	}
	body, err := service.ReadUpstreamResponseBody(resp.Body)
	if err != nil {
		return nil
	}

	ti, err := adaptor.ParseTaskResult(body)
	if err != nil || ti == nil {
		return nil
	}
	if err := service.ApplyTaskPollingResult(ctx, adaptor, task, ti, body); err != nil {
		return nil
	}

	// OpenAI Video API 由调用者的 ConvertToOpenAIVideo 分支处理
	if isOpenAIVideoAPI {
		return nil
	}

	// 非 OpenAI Video API: 构建自定义格式响应
	format := detectVideoFormat(body)
	out := map[string]any{
		"error":    nil,
		"format":   format,
		"metadata": nil,
		"status":   mapTaskStatusToSimple(task.Status),
		"task_id":  task.TaskID,
		"url":      task.GetResultURL(),
	}
	respBody, _ := common.Marshal(dto.TaskResponse[any]{
		Code: "success",
		Data: out,
	})
	return respBody
}

// detectVideoFormat 从 Gemini/Vertex 原始响应中探测视频格式
func detectVideoFormat(rawBody []byte) string {
	var raw map[string]any
	if err := common.Unmarshal(rawBody, &raw); err != nil {
		return "mp4"
	}
	respObj, ok := raw["response"].(map[string]any)
	if !ok {
		return "mp4"
	}
	vids, ok := respObj["videos"].([]any)
	if !ok || len(vids) == 0 {
		return "mp4"
	}
	v0, ok := vids[0].(map[string]any)
	if !ok {
		return "mp4"
	}
	mt, ok := v0["mimeType"].(string)
	if !ok || mt == "" || strings.Contains(mt, "mp4") {
		return "mp4"
	}
	return mt
}

// mapTaskStatusToSimple 将内部 TaskStatus 映射为简化状态字符串
func mapTaskStatusToSimple(status model.TaskStatus) string {
	switch status {
	case model.TaskStatusSuccess:
		return "succeeded"
	case model.TaskStatusFailure:
		return "failed"
	case model.TaskStatusQueued, model.TaskStatusSubmitted:
		return "queued"
	default:
		return "processing"
	}
}

func TaskModel2Dto(task *model.Task) *dto.TaskDto {
	return &dto.TaskDto{
		ID:         task.ID,
		CreatedAt:  task.CreatedAt,
		UpdatedAt:  task.UpdatedAt,
		TaskID:     task.TaskID,
		Platform:   string(task.Platform),
		UserId:     task.UserId,
		Group:      task.Group,
		ChannelId:  task.ChannelId,
		Quota:      task.Quota,
		Action:     task.Action,
		Status:     string(task.Status),
		FailReason: task.FailReason,
		ResultURL:  task.GetResultURL(),
		SubmitTime: task.SubmitTime,
		StartTime:  task.StartTime,
		FinishTime: task.FinishTime,
		Progress:   task.Progress,
		Properties: task.Properties,
		Username:   task.Username,
		Data:       task.Data,
	}
}
