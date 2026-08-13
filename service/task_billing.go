package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

// BuildTaskSubmissionBillingFinalization snapshots the accepted task's
// settlement, aggregates, and consume log before the task and outbox are
// inserted atomically. Later task recalculation may adjust used quota, but the
// accepted submission is the only phase that increments request_count.
func BuildTaskSubmissionBillingFinalization(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task, actualQuota int) (model.TaskBillingFinalizationPayload, error) {
	if c == nil || info == nil || task == nil {
		return model.TaskBillingFinalizationPayload{}, fmt.Errorf("task submission billing context is incomplete")
	}
	tokenName := c.GetString("token_name")
	logContent := fmt.Sprintf("操作 %s", info.Action)
	// 支持任务仅按次计费
	if common.StringsContains(constant.TaskPricePatches, info.OriginModelName) {
		logContent = fmt.Sprintf("%s，按次计费", logContent)
	} else {
		if otherRatios := info.PriceData.OtherRatios(); len(otherRatios) > 0 {
			var contents []string
			for key, ra := range otherRatios {
				if 1.0 != ra {
					contents = append(contents, fmt.Sprintf("%s: %.2f", key, ra))
				}
			}
			if len(contents) > 0 {
				logContent = fmt.Sprintf("%s, 计算参数：%s", logContent, strings.Join(contents, ", "))
			}
		}
	}
	other := make(map[string]interface{})
	other["is_task"] = true
	other["request_path"] = c.Request.URL.Path
	other["model_price"] = info.PriceData.ModelPrice
	if info.PriceData.ModelRatio > 0 {
		other["model_ratio"] = info.PriceData.ModelRatio
	}
	other["group_ratio"] = info.PriceData.GroupRatioInfo.GroupRatio
	if info.PriceData.GroupRatioInfo.HasSpecialRatio {
		other["user_group_ratio"] = info.PriceData.GroupRatioInfo.GroupSpecialRatio
	}
	if info.IsModelMapped {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = info.UpstreamModelName
	}
	other["task_id"] = task.TaskID
	attachQuotaSaturation(c, info, other)
	ip := ""
	if userSetting, err := model.GetUserSetting(info.UserId, false); err == nil && userSetting.RecordIpLog {
		ip = c.ClientIP()
	}
	log := model.TaskBillingFinalizationLog{
		UserID:            info.UserId,
		LogType:           model.LogTypeConsume,
		Content:           logContent,
		ChannelID:         info.ChannelId,
		ModelName:         info.OriginModelName,
		Quota:             actualQuota,
		TokenID:           info.TokenId,
		TokenName:         tokenName,
		Group:             info.UsingGroup,
		IP:                ip,
		RequestID:         c.GetString(common.RequestIdKey),
		UpstreamRequestID: c.GetString(common.UpstreamRequestIdKey),
		Username:          c.GetString("username"),
		Other:             other,
		NodeName:          task.PrivateData.NodeName,
		CreatedAt:         common.GetTimestamp(),
	}
	return BuildBillingFinalization(info, actualQuota, actualQuota, actualQuota, true, log)
}

// PersistTaskSubmissionBillingFinalization retries the idempotent transaction
// that adopts an upstream-accepted task. In particular, a database COMMIT can
// succeed while its acknowledgement is lost; retrying must discover the
// canonical task/outbox instead of refunding and orphaning the provider job.
func PersistTaskSubmissionBillingFinalization(task *model.Task, payload model.TaskBillingFinalizationPayload) (string, error) {
	var finalizationID string
	err := retryBillingOperation(func() error {
		var persistErr error
		finalizationID, persistErr = model.InsertTaskWithBillingFinalization(task, payload)
		return persistErr
	})
	return finalizationID, err
}

// ---------------------------------------------------------------------------
// 异步任务计费辅助函数
// ---------------------------------------------------------------------------

// taskIsSubscription 判断任务是否通过订阅计费。
func taskIsSubscription(task *model.Task) bool {
	return task.PrivateData.BillingSource == BillingSourceSubscription && task.PrivateData.SubscriptionId > 0
}

func taskBillingAdjustment(task *model.Task, kind string, delta int) model.BillingAdjustment {
	fundingSource := model.BillingAdjustmentWallet
	if taskIsSubscription(task) {
		fundingSource = model.BillingAdjustmentSubscription
	}
	adjustment := model.BillingAdjustment{
		RequestID:      fmt.Sprintf("async-task:%d:%s:%d:%d", task.ID, task.TaskID, task.UserId, task.ChannelId),
		Kind:           kind,
		FundingSource:  fundingSource,
		UserID:         task.UserId,
		SubscriptionID: task.PrivateData.SubscriptionId,
		TokenID:        task.PrivateData.TokenId,
		TokenKeyHash:   task.PrivateData.TokenKeyHash,
		FundingDelta:   delta,
		TokenDelta:     delta,
	}
	if task.PrivateData.TokenId <= 0 {
		adjustment.TokenDelta = 0
	}
	if fundingSource == model.BillingAdjustmentSubscription {
		adjustment.SubscriptionRequestID = task.PrivateData.BillingRequestId
	}
	return adjustment
}

func processTaskBillingFinalization(payload model.TaskBillingFinalizationPayload) (model.BillingAdjustmentResult, error) {
	var finalizationID string
	if err := retryBillingOperation(func() error {
		var enqueueErr error
		finalizationID, enqueueErr = model.EnqueueTaskBillingFinalization(payload)
		return enqueueErr
	}); err != nil {
		return model.BillingAdjustmentResult{}, fmt.Errorf("persist task billing finalization: %w", err)
	}
	result, err := model.ProcessTaskBillingFinalization(finalizationID)
	if err != nil {
		return result, fmt.Errorf("task billing finalization queued for retry: %w", err)
	}
	return result, nil
}

func processPersistedTaskBillingFinalization(ctx context.Context, task *model.Task, finalizationID string) {
	if finalizationID == "" {
		return
	}
	if _, err := model.ProcessTaskBillingFinalization(finalizationID); err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("任务计费结算已进入重试队列 task %s: %s", task.TaskID, err.Error()))
	}
}

// taskBillingOther 从 task 的 BillingContext 构建日志 Other 字段。
func taskBillingOther(task *model.Task) map[string]interface{} {
	other := make(map[string]interface{})
	if bc := task.PrivateData.BillingContext; bc != nil {
		other["model_price"] = bc.ModelPrice
		if bc.ModelRatio > 0 {
			other["model_ratio"] = bc.ModelRatio
		}
		other["group_ratio"] = bc.GroupRatio
		if priceData := taskBillingContextPriceData(bc); priceData != nil {
			for k, v := range priceData.OtherRatios() {
				other[k] = v
			}
		}
	}
	props := task.Properties
	if props.UpstreamModelName != "" && props.UpstreamModelName != props.OriginModelName {
		other["is_model_mapped"] = true
		other["upstream_model_name"] = props.UpstreamModelName
	}
	return other
}

func taskBillingContextPriceData(bc *model.TaskBillingContext) *types.PriceData {
	if bc == nil || len(bc.OtherRatios) == 0 {
		return nil
	}
	priceData := &types.PriceData{}
	if !priceData.ReplaceOtherRatios(bc.OtherRatios) {
		return nil
	}
	return priceData
}

// taskModelName 从 BillingContext 或 Properties 中获取模型名称。
func taskModelName(task *model.Task) string {
	if bc := task.PrivateData.BillingContext; bc != nil && bc.OriginModelName != "" {
		return bc.OriginModelName
	}
	return task.Properties.OriginModelName
}

func taskRefundFinalization(task *model.Task, reason string) *model.TaskBillingFinalizationPayload {
	if task == nil {
		return nil
	}
	quota := task.Quota
	if quota <= 0 || quota > common.MaxQuota {
		return nil
	}

	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["reason"] = reason
	payload := &model.TaskBillingFinalizationPayload{
		Adjustment:         taskBillingAdjustment(task, model.BillingAdjustmentRefund, -quota),
		ChannelCreatedTime: task.PrivateData.ChannelCreatedTime,
		Log: model.TaskBillingFinalizationLog{
			UserID:    task.UserId,
			LogType:   model.LogTypeRefund,
			ChannelID: task.ChannelId,
			ModelName: taskModelName(task),
			Quota:     quota,
			TokenID:   task.PrivateData.TokenId,
			Group:     task.Group,
			Other:     other,
			NodeName:  task.PrivateData.NodeName,
			CreatedAt: common.GetTimestamp(),
		},
	}
	if task.ID > 0 {
		payload.TaskDatabaseID = task.ID
		payload.UpdateTaskQuota = true
		payload.TargetTaskQuota = 0
	}
	return payload
}

func taskRecalculationFinalization(task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) *model.TaskBillingFinalizationPayload {
	if task == nil ||
		task.Quota < 0 ||
		task.Quota > common.MaxQuota ||
		actualQuota <= 0 ||
		actualQuota > common.MaxQuota ||
		actualQuota == task.Quota {
		return nil
	}

	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota
	logType := model.LogTypeRefund
	logQuota := -quotaDelta
	if quotaDelta > 0 {
		logType = model.LogTypeConsume
		logQuota = quotaDelta
	}
	other := taskBillingOther(task)
	other["task_id"] = task.TaskID
	other["pre_consumed_quota"] = preConsumedQuota
	other["actual_quota"] = actualQuota
	for _, clamp := range clamps {
		attachQuotaSaturationToOther(other, clamp)
	}
	payload := &model.TaskBillingFinalizationPayload{
		Adjustment:         taskBillingAdjustment(task, model.BillingAdjustmentSettle, quotaDelta),
		TaskDatabaseID:     task.ID,
		UpdateTaskQuota:    true,
		TargetTaskQuota:    actualQuota,
		ChannelCreatedTime: task.PrivateData.ChannelCreatedTime,
		Log: model.TaskBillingFinalizationLog{
			UserID:    task.UserId,
			LogType:   logType,
			Content:   reason,
			ChannelID: task.ChannelId,
			ModelName: taskModelName(task),
			Quota:     logQuota,
			TokenID:   task.PrivateData.TokenId,
			Group:     task.Group,
			Other:     other,
			NodeName:  task.PrivateData.NodeName,
			CreatedAt: common.GetTimestamp(),
		},
	}
	if quotaDelta > 0 {
		payload.UserUsedQuotaDelta = quotaDelta
		payload.ChannelUsedQuotaDelta = quotaDelta
	}
	return payload
}

// RefundTaskQuota 统一的任务失败退款逻辑。
// 当异步任务失败时，将预扣的 quota 退还给用户（支持钱包和订阅），并退还令牌额度。
func RefundTaskQuota(ctx context.Context, task *model.Task, reason string) {
	payload := taskRefundFinalization(task, reason)
	if payload == nil {
		return
	}
	_, err := processTaskBillingFinalization(*payload)
	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("持久化任务退款失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	task.Quota = 0
}

// RecalculateTaskQuota 通用的异步差额结算。
// actualQuota 是任务完成后的实际应扣额度，与预扣额度 (task.Quota) 做差额结算。
// reason 用于日志记录（例如 "token重算" 或 "adaptor调整"）。
// clamps 可选：若计算 actualQuota 时发生额度饱和，将其记入日志 admin_info（仅管理员可见）。
func RecalculateTaskQuota(ctx context.Context, task *model.Task, actualQuota int, reason string, clamps ...*common.QuotaClamp) {
	if task == nil || actualQuota <= 0 || actualQuota > common.MaxQuota {
		return
	}
	preConsumedQuota := task.Quota
	quotaDelta := actualQuota - preConsumedQuota

	if quotaDelta == 0 {
		logger.LogInfo(ctx, fmt.Sprintf("任务 %s 预扣费准确（%s，%s）",
			task.TaskID, logger.LogQuota(actualQuota), reason))
		return
	}

	logger.LogInfo(ctx, fmt.Sprintf("任务 %s 差额结算：delta=%s（实际：%s，预扣：%s，%s）",
		task.TaskID,
		logger.LogQuota(quotaDelta),
		logger.LogQuota(actualQuota),
		logger.LogQuota(preConsumedQuota),
		reason,
	))

	payload := taskRecalculationFinalization(task, actualQuota, reason, clamps...)
	if payload == nil {
		return
	}
	if _, err := processTaskBillingFinalization(*payload); err != nil {
		logger.LogError(ctx, fmt.Sprintf("持久化任务差额结算失败 task %s: %s", task.TaskID, err.Error()))
		return
	}
	task.Quota = actualQuota
}

// RecalculateTaskQuotaByTokens 根据实际 token 消耗重新计费（异步差额结算）。
// 当任务成功且返回了 totalTokens 时，根据模型倍率和分组倍率重新计算实际扣费额度，
// 与预扣费的差额进行补扣或退还。支持钱包和订阅计费来源。
func RecalculateTaskQuotaByTokens(ctx context.Context, task *model.Task, totalTokens int) {
	actualQuota, reason, clamp, ok, err := calculateTaskQuotaByTokens(task, totalTokens)
	if err != nil {
		taskID := ""
		if task != nil {
			taskID = task.TaskID
		}
		logger.LogError(ctx, fmt.Sprintf("计算任务 token 结算失败 task %s: %s", taskID, err.Error()))
		return
	}
	if !ok {
		return
	}
	RecalculateTaskQuota(ctx, task, actualQuota, reason, clamp)
}

func calculateTaskQuotaByTokens(task *model.Task, totalTokens int) (int, string, *common.QuotaClamp, bool, error) {
	if task == nil || totalTokens <= 0 {
		return 0, "", nil, false, nil
	}
	if totalTokens > common.MaxTokensLimit {
		return 0, "", nil, false, fmt.Errorf("provider token total exceeds %d", common.MaxTokensLimit)
	}

	modelName := taskModelName(task)

	var modelRatio float64
	var finalGroupRatio float64
	if billingContext := task.PrivateData.BillingContext; billingContext != nil {
		// TaskBillingContext is the pricing snapshot captured when the provider
		// accepted the task. Long-running tasks must not be repriced when an
		// administrator changes the live model or group ratio.
		modelRatio = billingContext.ModelRatio
		finalGroupRatio = billingContext.GroupRatio
	} else {
		// Tasks created before billing snapshots were introduced have no
		// BillingContext. Retain the historical live-settings lookup for those
		// records only.
		var hasRatioSetting bool
		modelRatio, hasRatioSetting, _ = ratio_setting.GetModelRatio(modelName)
		if !hasRatioSetting {
			return 0, "", nil, false, nil
		}

		group := task.Group
		if group == "" {
			user, err := model.GetUserById(task.UserId, false)
			if err != nil {
				return 0, "", nil, false, fmt.Errorf("load legacy task user group: %w", err)
			}
			group = user.Group
		}
		if group == "" {
			return 0, "", nil, false, nil
		}

		groupRatio := ratio_setting.GetGroupRatio(group)
		userGroupRatio, hasUserGroupRatio := ratio_setting.GetGroupGroupRatio(group, group)
		if hasUserGroupRatio {
			finalGroupRatio = userGroupRatio
		} else {
			finalGroupRatio = groupRatio
		}
	}
	// Only ratio-priced tasks can be recalculated from token usage.
	if math.IsNaN(modelRatio) || math.IsInf(modelRatio, 0) {
		return 0, "", nil, false, errors.New("task billing model ratio is non-finite")
	}
	if modelRatio <= 0 {
		return 0, "", nil, false, nil
	}
	if finalGroupRatio <= 0 || math.IsNaN(finalGroupRatio) || math.IsInf(finalGroupRatio, 0) {
		return 0, "", nil, false, errors.New("task billing group ratio is invalid")
	}

	// 计算 OtherRatios 乘积（视频折扣、时长等）
	otherMultiplier := 1.0
	if billingContext := task.PrivateData.BillingContext; billingContext != nil {
		for key, ratio := range billingContext.OtherRatios {
			if ratio <= 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
				return 0, "", nil, false, fmt.Errorf("task billing ratio %q is invalid", key)
			}
		}
	}
	if priceData := taskBillingContextPriceData(task.PrivateData.BillingContext); priceData != nil {
		otherMultiplier = priceData.OtherRatioMultiplier()
	}
	if otherMultiplier <= 0 || math.IsNaN(otherMultiplier) || math.IsInf(otherMultiplier, 0) {
		return 0, "", nil, false, errors.New("task billing multiplier is invalid")
	}

	// 计算实际应扣费额度: totalTokens * modelRatio * groupRatio * otherMultiplier（饱和转换，防止溢出成负数）
	actualQuota, clamp := common.QuotaFromFloatChecked(float64(totalTokens) * modelRatio * finalGroupRatio * otherMultiplier)
	if actualQuota == 0 && clamp == nil {
		actualQuota = 1
	}

	reason := fmt.Sprintf("token重算：tokens=%d, modelRatio=%.2f, groupRatio=%.2f, otherMultiplier=%.4f", totalTokens, modelRatio, finalGroupRatio, otherMultiplier)
	return actualQuota, reason, clamp, true, nil
}
