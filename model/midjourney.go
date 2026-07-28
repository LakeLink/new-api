package model

import (
	"errors"
	"fmt"
	"strings"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

type Midjourney struct {
	Id          int    `json:"id"`
	Code        int    `json:"code"`
	UserId      int    `json:"user_id" gorm:"index"`
	Action      string `json:"action" gorm:"type:varchar(40);index"`
	MjId        string `json:"mj_id" gorm:"index"`
	Prompt      string `json:"prompt"`
	PromptEn    string `json:"prompt_en"`
	Description string `json:"description"`
	State       string `json:"state"`
	SubmitTime  int64  `json:"submit_time" gorm:"index"`
	StartTime   int64  `json:"start_time" gorm:"index"`
	FinishTime  int64  `json:"finish_time" gorm:"index"`
	ImageUrl    string `json:"image_url"`
	VideoUrl    string `json:"video_url"`
	VideoUrls   string `json:"video_urls"`
	Status      string `json:"status" gorm:"type:varchar(20);index"`
	Progress    string `json:"progress" gorm:"type:varchar(30);index"`
	FailReason  string `json:"fail_reason"`
	ChannelId   int    `json:"channel_id"`
	// ChannelCreatedTime pins an accepted task to the exact channel row that
	// submitted it. Channel IDs can be reused after deletion, so polling and
	// delayed accounting must not send an old provider task ID to a replacement
	// channel or attribute usage to it.
	ChannelCreatedTime int64  `json:"-" gorm:"bigint"`
	Quota              int    `json:"quota"`
	Buttons            string `json:"buttons"`
	Properties         string `json:"properties"`

	// Billing context is private persistence metadata used to reverse the exact
	// durable charge when an asynchronous task later fails.
	BillingRequestId      string `json:"-" gorm:"index"`
	BillingPurpose        string `json:"-"`
	BillingSource         string `json:"-"`
	BillingSubscriptionId int    `json:"-" gorm:"index"`
	BillingTaskId         string `json:"-" gorm:"type:varchar(64)"`
	BillingTokenId        int    `json:"-"`
	BillingIsPlayground   bool   `json:"-"`
	BillingModelName      string `json:"-" gorm:"type:varchar(191)"`
	BillingTokenName      string `json:"-" gorm:"type:varchar(191)"`
	BillingGroup          string `json:"-" gorm:"type:varchar(64)"`
	BillingNodeName       string `json:"-" gorm:"type:varchar(191)"`
	BillingLogContent     string `json:"-" gorm:"type:text"`
	BillingLogOther       string `json:"-" gorm:"type:text"`
	BillingFinalized      bool   `json:"-"`
	BillingRefunded       bool   `json:"-"`
}

// midjourneyStatusUpdateColumns are the provider-owned lifecycle fields that
// polling and notify callbacks may change. Billing identity and reconciliation
// markers are intentionally excluded so a stale provider snapshot cannot
// reopen an already finalized charge or refund.
var midjourneyStatusUpdateColumns = []string{
	"code",
	"prompt_en",
	"description",
	"state",
	"submit_time",
	"start_time",
	"finish_time",
	"image_url",
	"video_url",
	"video_urls",
	"status",
	"progress",
	"fail_reason",
	"buttons",
	"properties",
}

// TaskQueryParams 用于包含所有搜索条件的结构体，可以根据需求添加更多字段
type TaskQueryParams struct {
	ChannelID      string
	MjID           string
	StartTimestamp string
	EndTimestamp   string
}

func GetAllUserTask(userId int, startIdx int, num int, queryParams TaskQueryParams) ([]*Midjourney, error) {
	var tasks []*Midjourney

	// 初始化查询构建器
	query := DB.Where("user_id = ?", userId)

	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		// 假设您已将前端传来的时间戳转换为数据库所需的时间格式，并处理了时间戳的验证和解析
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	if err := query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error; err != nil {
		return nil, err
	}

	return tasks, nil
}

func GetAllTasks(startIdx int, num int, queryParams TaskQueryParams) ([]*Midjourney, error) {
	var tasks []*Midjourney

	// 初始化查询构建器
	query := DB

	// 添加过滤条件
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}

	// 获取数据
	if err := query.Order("id desc").Limit(num).Offset(startIdx).Find(&tasks).Error; err != nil {
		return nil, err
	}

	return tasks, nil
}

func GetAllUnFinishTasks() ([]*Midjourney, error) {
	var tasks []*Midjourney
	// Billing finalization is part of the durable task lifecycle. A terminal
	// upstream task remains actionable until its charge (and, for failures, its
	// reversal) has been reconciled into logs and aggregate counters.
	err := DB.Where(
		"progress IS NULL OR progress <> ? OR (billing_purpose <> ? AND (billing_finalized IS NULL OR billing_finalized = ?)) OR (billing_purpose <> ? AND status = ? AND (billing_refunded IS NULL OR billing_refunded = ?))",
		"100%", "", false, "", "FAILURE", false,
	).Find(&tasks).Error
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// HasUnfinishedMidjourneyTasks reports whether at least one Midjourney task is
// still in progress. It is a cheap existence check (LIMIT 1) used to decide
// whether the midjourney_poll system task needs to run; when no task is pending
// the scheduler skips creating a row entirely.
func HasUnfinishedMidjourneyTasks() (bool, error) {
	var id int
	err := DB.Model(&Midjourney{}).
		Where(
			"progress IS NULL OR progress <> ? OR (billing_purpose <> ? AND (billing_finalized IS NULL OR billing_finalized = ?)) OR (billing_purpose <> ? AND status = ? AND (billing_refunded IS NULL OR billing_refunded = ?))",
			"100%", "", false, "", "FAILURE", false,
		).
		Limit(1).
		Pluck("id", &id).Error
	if err != nil {
		return false, err
	}
	return id != 0, nil
}

var errMidjourneyBillingReversalNotSucceeded = errors.New("Midjourney billing reversal has not succeeded")

func loadSucceededMidjourneySettlementTx(tx *gorm.DB, task *Midjourney) (*SystemTask, *BillingAdjustment, error) {
	if tx == nil || task == nil || task.BillingTaskId == "" {
		return nil, nil, errors.New("Midjourney settlement identity is missing")
	}
	if task.Quota < 0 || task.Quota > common.MaxQuota {
		return nil, nil, errors.New("Midjourney settlement quota exceeds storage range")
	}
	billingPurpose := strings.TrimSpace(task.BillingPurpose)
	if task.BillingRequestId == "" || billingPurpose == "" {
		return nil, nil, errors.New("Midjourney settlement request context is missing")
	}
	settlementRequestID := fmt.Sprintf("%s\x00purpose:%s", task.BillingRequestId, billingPurpose)
	if BillingAdjustmentTaskID(settlementRequestID, BillingAdjustmentSettle) != task.BillingTaskId {
		return nil, nil, errors.New("Midjourney settlement identity does not match persisted request context")
	}
	var settlementTask SystemTask
	query := tx.Where("task_id = ? AND type = ?", task.BillingTaskId, SystemTaskTypeBillingAdjustment).
		Limit(1).
		Find(&settlementTask)
	if query.Error != nil {
		return nil, nil, query.Error
	}
	if query.RowsAffected == 0 || settlementTask.Status != SystemTaskStatusSucceeded {
		return nil, nil, errors.New("Midjourney settlement has not succeeded")
	}
	var settlement BillingAdjustment
	if err := common.UnmarshalJsonStr(settlementTask.Payload, &settlement); err != nil {
		return nil, nil, fmt.Errorf("inspect Midjourney settlement %s: %w", settlementTask.TaskID, err)
	}
	if settlement.Kind != BillingAdjustmentSettle || settlement.ReversalOfTaskID != "" ||
		settlement.RequestID != settlementRequestID ||
		settlement.UserID != task.UserId || settlement.FundingSource != task.BillingSource ||
		settlement.SubscriptionID != task.BillingSubscriptionId || settlement.TokenID != task.BillingTokenId ||
		settlement.FundingDelta != task.Quota ||
		BillingAdjustmentTaskID(settlement.RequestID, settlement.Kind) != task.BillingTaskId {
		return nil, nil, errors.New("Midjourney settlement context does not match persisted task")
	}
	if settlement.FundingSource != BillingAdjustmentWallet && settlement.FundingSource != BillingAdjustmentSubscription {
		return nil, nil, errors.New("Midjourney settlement funding source is invalid")
	}
	return &settlementTask, &settlement, nil
}

func loadSucceededMidjourneyReversalTx(tx *gorm.DB, task *Midjourney) (*SystemTask, *BillingAdjustment, error) {
	if _, _, err := loadSucceededMidjourneySettlementTx(tx, task); err != nil {
		return nil, nil, err
	}
	var reversalTask SystemTask
	query := tx.Where("task_id = ? AND type = ?", BillingAdjustmentReversalTaskID(task.BillingTaskId), SystemTaskTypeBillingAdjustment).
		Limit(1).
		Find(&reversalTask)
	if query.Error != nil {
		return nil, nil, query.Error
	}
	if query.RowsAffected == 0 || reversalTask.Status != SystemTaskStatusSucceeded {
		return nil, nil, errMidjourneyBillingReversalNotSucceeded
	}
	var reversal BillingAdjustment
	if err := common.UnmarshalJsonStr(reversalTask.Payload, &reversal); err != nil {
		return nil, nil, fmt.Errorf("inspect Midjourney billing reversal %s: %w", reversalTask.TaskID, err)
	}
	if reversal.Kind != BillingAdjustmentRefund ||
		reversal.FundingSource != task.BillingSource ||
		reversal.UserID != task.UserId ||
		reversal.SubscriptionID != task.BillingSubscriptionId ||
		reversal.TokenID != task.BillingTokenId ||
		reversal.ReversalOfTaskID != task.BillingTaskId {
		return nil, nil, errors.New("Midjourney billing reversal context does not match persisted task")
	}
	return &reversalTask, &reversal, nil
}

func ensureMidjourneyBillingLogTx(tx *gorm.DB, task *Midjourney, requestID string, logType int, quota int, content string, other string) error {
	if tx == nil || task == nil || requestID == "" {
		return errors.New("invalid Midjourney billing log context")
	}
	if logType == LogTypeConsume && !common.GetLegacyOptionBool("LogConsumeEnabled", &common.LogConsumeEnabled) {
		return nil
	}
	logDB := LOG_DB
	if logDB == nil {
		return errors.New("log database is not initialized")
	}
	if LOG_DB == DB {
		logDB = tx
	}
	var existing Log
	query := logDB.Where("request_id = ? AND type = ?", requestID, logType).Limit(1).Find(&existing)
	if query.Error != nil {
		return query.Error
	}
	if query.RowsAffected != 0 {
		if existing.UserId != task.UserId || existing.ChannelId != task.ChannelId ||
			existing.TokenId != task.BillingTokenId || existing.Quota != quota ||
			existing.ModelName != task.BillingModelName || existing.TokenName != task.BillingTokenName ||
			existing.Group != task.BillingGroup {
			return errors.New("Midjourney billing log idempotency key has conflicting context")
		}
		return nil
	}

	var username string
	if err := tx.Unscoped().Model(&User{}).Where("id = ?", task.UserId).Select("username").Find(&username).Error; err != nil {
		return err
	}
	log := Log{
		UserId:         task.UserId,
		Username:       username,
		CreatedAt:      common.GetTimestamp(),
		Type:           logType,
		Content:        content,
		TokenName:      task.BillingTokenName,
		ModelName:      task.BillingModelName,
		Quota:          quota,
		ChannelId:      task.ChannelId,
		TokenId:        task.BillingTokenId,
		Group:          task.BillingGroup,
		RequestId:      requestID,
		BillingEventId: &requestID,
		Other:          other,
		IsStream:       false,
		PromptTokens:   0,
	}
	_, err := createBillingEventLogWithDB(logDB, &log)
	return err
}

// FinalizeMidjourneyBilling reconciles the non-balance accounting side effects
// of a succeeded durable settlement. The Midjourney row is the durable outbox:
// a missing charge can be recreated from it, and retries make the consume log
// idempotent while committing user/channel aggregates with the finalized bit.
func FinalizeMidjourneyBilling(midjourneyID int, billingTaskID string) (bool, error) {
	if midjourneyID <= 0 || billingTaskID == "" {
		return false, errors.New("invalid Midjourney billing finalization identity")
	}
	finalized := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var task Midjourney
		if err := lockForUpdate(tx).Where("id = ?", midjourneyID).First(&task).Error; err != nil {
			return err
		}
		if task.BillingTaskId != billingTaskID {
			return errors.New("Midjourney billing finalization identity mismatch")
		}
		if task.Quota < 0 || task.Quota > common.MaxQuota {
			return errors.New("Midjourney billing quota exceeds storage range")
		}
		if task.BillingFinalized {
			return nil
		}
		if _, _, err := loadSucceededMidjourneySettlementTx(tx, &task); err != nil {
			return err
		}
		if err := ensureMidjourneyBillingLogTx(tx, &task, task.BillingTaskId, LogTypeConsume, task.Quota, task.BillingLogContent, task.BillingLogOther); err != nil {
			return err
		}

		var user User
		if err := lockForUpdate(tx.Unscoped()).Where("id = ?", task.UserId).First(&user).Error; err != nil {
			return err
		}
		usedQuota := saturatingQuotaAggregate(user.UsedQuota, task.Quota, "Midjourney user used quota")
		requestCount := saturatingQuotaAggregate(user.RequestCount, 1, "Midjourney user request count")
		if err := tx.Unscoped().Model(&user).Updates(map[string]any{
			"used_quota":    usedQuota,
			"request_count": requestCount,
		}).Error; err != nil {
			return err
		}

		var channel Channel
		channelQuery := lockForUpdate(tx).Where("id = ?", task.ChannelId).Limit(1).Find(&channel)
		if channelQuery.Error != nil {
			return channelQuery.Error
		}
		if channelQuery.RowsAffected != 0 {
			usedQuota := saturatingInt64Aggregate(channel.UsedQuota, int64(task.Quota), "Midjourney channel used quota")
			if err := tx.Model(&channel).Update("used_quota", usedQuota).Error; err != nil {
				return err
			}
		}
		if err := tx.Model(&task).Update("billing_finalized", true).Error; err != nil {
			return err
		}
		finalized = true
		return nil
	})
	return finalized, err
}

// FinalizeMidjourneyBillingRefund records a succeeded exact reversal once and
// marks the failed job reconciled. Repeated polling and worker-delayed reversal
// processing converge on the deterministic reversal task and refund log IDs.
func FinalizeMidjourneyBillingRefund(midjourneyID int, billingTaskID string, reason string) (bool, error) {
	if midjourneyID <= 0 || billingTaskID == "" {
		return false, errors.New("invalid Midjourney refund finalization identity")
	}
	finalized := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var task Midjourney
		if err := lockForUpdate(tx).Where("id = ?", midjourneyID).First(&task).Error; err != nil {
			return err
		}
		if task.BillingTaskId != billingTaskID {
			return errors.New("Midjourney refund finalization identity mismatch")
		}
		if task.BillingRefunded {
			return nil
		}
		if !task.BillingFinalized {
			return errors.New("Midjourney charge must be finalized before its refund")
		}
		reversalTask, reversal, err := loadSucceededMidjourneyReversalTx(tx, &task)
		if err != nil {
			return err
		}
		result, err := billingAdjustmentResultFromTask(reversalTask, *reversal)
		if err != nil {
			return err
		}
		refundQuotaValue := -(int64(result.SubscriptionDelta) + int64(result.WalletDelta))
		if refundQuotaValue < 0 || refundQuotaValue > int64(common.MaxQuota) {
			return errors.New("Midjourney reversal contains an invalid refund result")
		}
		refundQuota := common.QuotaFromFloat(float64(refundQuotaValue))
		other := common.MapToJsonStr(map[string]any{
			"task_id":        task.MjId,
			"reason":         reason,
			"billing_source": task.BillingSource,
		})
		if err := ensureMidjourneyBillingLogTx(tx, &task, reversalTask.TaskID, LogTypeRefund, refundQuota, "", other); err != nil {
			return err
		}
		if err := tx.Model(&task).Update("billing_refunded", true).Error; err != nil {
			return err
		}
		finalized = true
		return nil
	})
	return finalized, err
}

func GetByOnlyMJId(mjId string) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("mj_id = ?", mjId).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetByMJId(userId int, mjId string) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("user_id = ? and mj_id = ?", userId, mjId).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetByMJIds(userId int, mjIds []string) []*Midjourney {
	var mj []*Midjourney
	var err error
	err = DB.Where("user_id = ? and mj_id in (?)", userId, mjIds).Find(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func GetMjByuId(id int) *Midjourney {
	var mj *Midjourney
	var err error
	err = DB.Where("id = ?", id).First(&mj).Error
	if err != nil {
		return nil
	}
	return mj
}

func UpdateProgress(id int, progress string) error {
	return DB.Model(&Midjourney{}).Where("id = ?", id).Update("progress", progress).Error
}

func (midjourney *Midjourney) Insert() error {
	if midjourney == nil {
		return errors.New("Midjourney task is nil")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := validateMidjourneyChannelSnapshotTx(tx, midjourney); err != nil {
			return err
		}
		if midjourney.UserId <= 0 {
			return errors.New("Midjourney user is invalid")
		}
		// Free and explicitly unbilled tasks do not have a billing reservation
		// to serialize against account deletion. Lock the active user row so a
		// deletion either observes this new task or wins first and makes the
		// insertion fail, rather than leaving an orphaned task.
		var user User
		if err := lockForUpdate(tx).
			Select("id").
			Where("id = ?", midjourney.UserId).
			First(&user).Error; err != nil {
			return err
		}
		return tx.Create(midjourney).Error
	})
}

func validateMidjourneyChannelSnapshotTx(tx *gorm.DB, midjourney *Midjourney) error {
	if tx == nil || midjourney == nil {
		return errors.New("Midjourney channel snapshot context is invalid")
	}
	if midjourney.ChannelCreatedTime == 0 {
		return nil
	}
	if midjourney.ChannelCreatedTime < 0 || midjourney.ChannelId <= 0 {
		return errors.New("Midjourney channel snapshot is invalid")
	}

	var channel Channel
	query := lockForUpdate(tx).
		Select("id", "created_time").
		Where("id = ?", midjourney.ChannelId).
		Limit(1).
		Find(&channel)
	if query.Error != nil {
		return query.Error
	}
	if query.RowsAffected == 0 {
		return fmt.Errorf("Midjourney channel %d no longer exists", midjourney.ChannelId)
	}
	if channel.CreatedTime != midjourney.ChannelCreatedTime {
		return fmt.Errorf(
			"Midjourney channel %d was replaced: expected created_time %d, got %d",
			midjourney.ChannelId,
			midjourney.ChannelCreatedTime,
			channel.CreatedTime,
		)
	}
	return nil
}

// InsertWithBillingReservation atomically persists an upstream-accepted task
// and adopts its pre-dispatch reservation as the canonical Midjourney charge.
// The succeeded adjustment record is proof of the already-applied reservation;
// it must not apply the same balance delta a second time. Existing polling and
// failure-reversal paths can then reconcile the task from that durable proof.
func (midjourney *Midjourney) InsertWithBillingReservation() error {
	if midjourney == nil {
		return errors.New("Midjourney task is nil")
	}
	if midjourney.BillingRequestId == "" || strings.TrimSpace(midjourney.BillingPurpose) == "" ||
		midjourney.BillingTaskId == "" {
		return errors.New("Midjourney reservation billing context is incomplete")
	}
	if midjourney.Quota < 0 || midjourney.Quota > common.MaxQuota {
		return errors.New("Midjourney reservation quota exceeds storage range")
	}
	if midjourney.BillingSource != BillingAdjustmentWallet &&
		midjourney.BillingSource != BillingAdjustmentSubscription {
		return errors.New("Midjourney reservation funding source is invalid")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		// Lock the submitting channel before the task is inserted. Channel
		// deletion and identity-changing updates take the same row lock, which
		// closes the check-then-delete race with an accepted async submission.
		if err := validateMidjourneyChannelSnapshotTx(tx, midjourney); err != nil {
			return err
		}

		var reservation BillingReservation
		if err := lockForUpdate(tx).
			Where("request_id = ?", midjourney.BillingRequestId).
			First(&reservation).Error; err != nil {
			return err
		}
		if reservation.UserID != midjourney.UserId ||
			reservation.TokenID != midjourney.BillingTokenId ||
			reservation.FundingSource != midjourney.BillingSource ||
			reservation.SubscriptionID != midjourney.BillingSubscriptionId ||
			reservation.IsPlayground != midjourney.BillingIsPlayground ||
			reservation.ReservedQuota != midjourney.Quota {
			return errors.New("Midjourney task does not match its billing reservation")
		}
		if reservation.Status != "" &&
			reservation.Status != billingReservationStatusPending &&
			reservation.Status != billingReservationStatusSettled {
			return fmt.Errorf("Midjourney billing reservation has terminal status %s", reservation.Status)
		}

		settlementRequestID := fmt.Sprintf(
			"%s\x00purpose:%s",
			midjourney.BillingRequestId,
			strings.TrimSpace(midjourney.BillingPurpose),
		)
		if BillingAdjustmentTaskID(settlementRequestID, BillingAdjustmentSettle) != midjourney.BillingTaskId {
			return errors.New("Midjourney settlement identity does not match its reservation")
		}
		tokenDelta := reservation.TokenReserved
		if tokenDelta < 0 || tokenDelta > common.MaxQuota ||
			(!midjourney.BillingIsPlayground && tokenDelta != midjourney.Quota) ||
			(midjourney.BillingIsPlayground && tokenDelta != 0) {
			return errors.New("Midjourney token reservation does not match its charge")
		}

		adjustment := BillingAdjustment{
			RequestID:      settlementRequestID,
			Kind:           BillingAdjustmentSettle,
			FundingSource:  midjourney.BillingSource,
			UserID:         midjourney.UserId,
			SubscriptionID: midjourney.BillingSubscriptionId,
			TokenID:        midjourney.BillingTokenId,
			TokenKeyHash:   reservation.TokenKeyHash,
			FundingDelta:   midjourney.Quota,
			TokenDelta:     tokenDelta,
		}
		result := BillingAdjustmentResult{TokenDelta: tokenDelta}
		switch midjourney.BillingSource {
		case BillingAdjustmentWallet:
			result.WalletDelta = midjourney.Quota
		case BillingAdjustmentSubscription:
			var record SubscriptionPreConsumeRecord
			if err := lockForUpdate(tx).
				Where("request_id = ?", midjourney.BillingRequestId).
				First(&record).Error; err != nil {
				return err
			}
			if record.UserId != midjourney.UserId ||
				record.UserSubscriptionId != midjourney.BillingSubscriptionId ||
				record.PreConsumed != int64(midjourney.Quota) ||
				(record.Status != "consumed" && record.Status != "settled") {
				return errors.New("Midjourney subscription reservation proof is invalid")
			}
			subscription, err := lockBillingSubscriptionTx(
				tx,
				midjourney.BillingSubscriptionId,
				midjourney.UserId,
			)
			if err != nil {
				return err
			}
			if subscription.QuotaResetVersion < record.QuotaResetVersion {
				return errors.New("Midjourney subscription reset version moved backwards")
			}
			result.SubscriptionDelta = midjourney.Quota
			result.SubscriptionPeriodStart = subscription.LastResetTime
			result.SubscriptionResetVersion = record.QuotaResetVersion
			if record.Status != "settled" {
				if err := tx.Model(&record).Updates(map[string]interface{}{
					"status":     "settled",
					"updated_at": common.GetTimestamp(),
				}).Error; err != nil {
					return err
				}
			}
		}

		payload, err := common.Marshal(adjustment)
		if err != nil {
			return err
		}
		resultJSON, err := common.Marshal(result)
		if err != nil {
			return err
		}
		settlementTask := &SystemTask{
			TaskID:  midjourney.BillingTaskId,
			Type:    SystemTaskTypeBillingAdjustment,
			Status:  SystemTaskStatusSucceeded,
			Payload: string(payload),
			Result:  string(resultJSON),
		}
		if err := persistBillingAdjustmentTask(tx, settlementTask); err != nil {
			return err
		}
		var persistedSettlement SystemTask
		if err := tx.Where("task_id = ?", midjourney.BillingTaskId).
			First(&persistedSettlement).Error; err != nil {
			return err
		}
		if persistedSettlement.Type != SystemTaskTypeBillingAdjustment ||
			persistedSettlement.Status != SystemTaskStatusSucceeded ||
			persistedSettlement.Payload != settlementTask.Payload ||
			persistedSettlement.Result != settlementTask.Result {
			return errors.New("Midjourney settlement proof conflicts with persisted billing task")
		}

		var persisted Midjourney
		existing := tx.Where("billing_request_id = ?", midjourney.BillingRequestId).
			Limit(1).
			Find(&persisted)
		if existing.Error != nil {
			return existing.Error
		}
		if existing.RowsAffected == 0 {
			if err := tx.Create(midjourney).Error; err != nil {
				return err
			}
		} else {
			if persisted.UserId != midjourney.UserId ||
				persisted.ChannelId != midjourney.ChannelId ||
				persisted.ChannelCreatedTime != midjourney.ChannelCreatedTime ||
				persisted.Action != midjourney.Action ||
				persisted.MjId != midjourney.MjId ||
				persisted.Quota != midjourney.Quota ||
				persisted.BillingPurpose != midjourney.BillingPurpose ||
				persisted.BillingSource != midjourney.BillingSource ||
				persisted.BillingSubscriptionId != midjourney.BillingSubscriptionId ||
				persisted.BillingTaskId != midjourney.BillingTaskId ||
				persisted.BillingTokenId != midjourney.BillingTokenId ||
				persisted.BillingIsPlayground != midjourney.BillingIsPlayground ||
				persisted.BillingModelName != midjourney.BillingModelName ||
				persisted.BillingTokenName != midjourney.BillingTokenName ||
				persisted.BillingGroup != midjourney.BillingGroup ||
				persisted.BillingNodeName != midjourney.BillingNodeName ||
				persisted.BillingLogContent != midjourney.BillingLogContent ||
				persisted.BillingLogOther != midjourney.BillingLogOther {
				return errors.New("Midjourney request ID was reused with a different accepted task")
			}
			*midjourney = persisted
		}
		return markBillingReservationTerminalTx(
			tx,
			midjourney.BillingRequestId,
			BillingAdjustmentSettle,
		)
	})
}

func (midjourney *Midjourney) Update() error {
	return DB.Model(&Midjourney{}).
		Where("id = ?", midjourney.Id).
		Select(midjourneyStatusUpdateColumns).
		Updates(midjourney).Error
}

// UpdateWithStatus performs a conditional UPDATE guarded by fromStatus (CAS).
// Only provider-owned lifecycle fields are updated; durable billing context is
// maintained by the billing reconciliation transaction.
// Returns (true, nil) if this caller won the update, (false, nil) if another
// process already moved the task out of fromStatus.
func (midjourney *Midjourney) UpdateWithStatus(fromStatus string) (bool, error) {
	result := DB.Model(&Midjourney{}).
		Where("id = ? AND status = ?", midjourney.Id, fromStatus).
		Select(midjourneyStatusUpdateColumns).
		Updates(midjourney)
	if result.Error != nil {
		return false, result.Error
	}
	return result.RowsAffected > 0, nil
}

func MjBulkUpdate(mjIds []string, params map[string]any) error {
	return DB.Model(&Midjourney{}).
		Where("mj_id in (?)", mjIds).
		Updates(params).Error
}

func MjBulkUpdateByTaskIds(taskIDs []int, params map[string]any) error {
	return DB.Model(&Midjourney{}).
		Where("id in (?)", taskIDs).
		Updates(params).Error
}

// CountAllTasks returns total midjourney tasks for admin query
func CountAllTasks(queryParams TaskQueryParams) (int64, error) {
	var total int64
	query := DB.Model(&Midjourney{})
	if queryParams.ChannelID != "" {
		query = query.Where("channel_id = ?", queryParams.ChannelID)
	}
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	err := query.Count(&total).Error
	return total, err
}

// CountAllUserTask returns total midjourney tasks for user
func CountAllUserTask(userId int, queryParams TaskQueryParams) (int64, error) {
	var total int64
	query := DB.Model(&Midjourney{}).Where("user_id = ?", userId)
	if queryParams.MjID != "" {
		query = query.Where("mj_id = ?", queryParams.MjID)
	}
	if queryParams.StartTimestamp != "" {
		query = query.Where("submit_time >= ?", queryParams.StartTimestamp)
	}
	if queryParams.EndTimestamp != "" {
		query = query.Where("submit_time <= ?", queryParams.EndTimestamp)
	}
	err := query.Count(&total).Error
	return total, err
}
