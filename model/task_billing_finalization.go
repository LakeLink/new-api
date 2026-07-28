package model

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	taskBillingFinalizationPending    = "pending"
	taskBillingFinalizationProcessing = "processing"
	taskBillingFinalizationSucceeded  = "succeeded"
	taskBillingLogClaimSeconds        = int64(60)
)

// TaskBillingFinalization is an outbox entry created before an asynchronous
// task balance adjustment can run. It closes the crash window between the
// atomic balance update and the task quota, usage aggregates, and billing log.
type TaskBillingFinalization struct {
	ID             int64  `json:"id" gorm:"primaryKey"`
	FinalizationID string `json:"finalization_id" gorm:"type:varchar(64);uniqueIndex"`
	Payload        string `json:"payload" gorm:"type:text"`
	MainStatus     string `json:"main_status" gorm:"type:varchar(16);index"`
	LogStatus      string `json:"log_status" gorm:"type:varchar(16);index"`
	LogClaimToken  string `json:"-" gorm:"type:varchar(32)"`
	LogClaimedAt   int64  `json:"log_claimed_at" gorm:"bigint"`
	CreatedAt      int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt      int64  `json:"updated_at" gorm:"bigint;index"`
}

func (f *TaskBillingFinalization) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	f.CreatedAt = now
	f.UpdatedAt = now
	return nil
}

type TaskBillingFinalizationLog struct {
	UserID                                int                    `json:"user_id"`
	LogType                               int                    `json:"log_type"`
	Content                               string                 `json:"content"`
	ChannelID                             int                    `json:"channel_id"`
	ModelName                             string                 `json:"model_name"`
	Quota                                 int                    `json:"quota"`
	PromptTokens                          int                    `json:"prompt_tokens,omitempty"`
	CompletionTokens                      int                    `json:"completion_tokens,omitempty"`
	TokenID                               int                    `json:"token_id"`
	TokenName                             string                 `json:"token_name,omitempty"`
	Group                                 string                 `json:"group"`
	UseTime                               int                    `json:"use_time,omitempty"`
	IsStream                              bool                   `json:"is_stream,omitempty"`
	IP                                    string                 `json:"ip,omitempty"`
	RequestID                             string                 `json:"request_id,omitempty"`
	UpstreamRequestID                     string                 `json:"upstream_request_id,omitempty"`
	Username                              string                 `json:"username,omitempty"`
	SubscriptionPreConsumed               int64                  `json:"subscription_pre_consumed,omitempty"`
	SubscriptionAmountTotal               int64                  `json:"subscription_amount_total,omitempty"`
	SubscriptionAmountUsedAfterPreConsume int64                  `json:"subscription_amount_used_after_pre_consume,omitempty"`
	Other                                 map[string]interface{} `json:"other,omitempty"`
	NodeName                              string                 `json:"node_name,omitempty"`
	CreatedAt                             int64                  `json:"created_at"`
}

// TaskBillingFinalizationPayload is a canonical snapshot of every side effect
// that must follow an asynchronous task balance adjustment.
type TaskBillingFinalizationPayload struct {
	Adjustment                BillingAdjustment          `json:"adjustment"`
	TaskDatabaseID            int64                      `json:"task_database_id,omitempty"`
	UpdateTaskQuota           bool                       `json:"update_task_quota"`
	TargetTaskQuota           int                        `json:"target_task_quota,omitempty"`
	UserUsedQuotaDelta        int                        `json:"user_used_quota_delta,omitempty"`
	IncrementUserRequestCount bool                       `json:"increment_user_request_count"`
	ChannelUsedQuotaDelta     int                        `json:"channel_used_quota_delta,omitempty"`
	ChannelCreatedTime        int64                      `json:"channel_created_time,omitempty"`
	Log                       TaskBillingFinalizationLog `json:"log"`
}

func taskBillingFinalizationID(payload TaskBillingFinalizationPayload) string {
	return BillingAdjustmentTaskID(payload.Adjustment.RequestID, payload.Adjustment.Kind)
}

func validateTaskBillingFinalizationPayload(payload TaskBillingFinalizationPayload) error {
	if err := validateBillingAdjustment(payload.Adjustment); err != nil {
		return fmt.Errorf("task billing finalization adjustment is invalid: %w", err)
	}
	if payload.UserUsedQuotaDelta < 0 || payload.UserUsedQuotaDelta > common.MaxQuota ||
		payload.ChannelUsedQuotaDelta < 0 || payload.ChannelUsedQuotaDelta > common.MaxQuota {
		return errors.New("task billing finalization aggregate delta is invalid")
	}
	if payload.ChannelUsedQuotaDelta != payload.UserUsedQuotaDelta {
		return errors.New("task billing finalization aggregate fields are inconsistent")
	}
	if payload.ChannelCreatedTime < 0 {
		return errors.New("task billing finalization channel creation time is invalid")
	}
	// A successful zero-cost request is still a request, so a zero quota delta
	// may increment request_count. Positive synchronous usage must increment it
	// too. Async task recalculation is the sole exception because task submit
	// already counted the request; those payloads update the persisted task quota.
	if payload.UserUsedQuotaDelta > 0 && !payload.IncrementUserRequestCount && !payload.UpdateTaskQuota {
		return errors.New("task billing finalization positive synchronous usage must increment request count")
	}
	if payload.UpdateTaskQuota {
		if payload.TaskDatabaseID <= 0 || payload.TargetTaskQuota < 0 || payload.TargetTaskQuota > common.MaxQuota {
			return errors.New("task billing finalization target quota is invalid")
		}
	} else if payload.TaskDatabaseID < 0 || payload.TargetTaskQuota != 0 {
		return errors.New("task billing finalization task reference is invalid")
	}
	if payload.Log.UserID != payload.Adjustment.UserID || payload.Log.UserID <= 0 {
		return errors.New("task billing finalization log user does not match adjustment")
	}
	if payload.Log.LogType != LogTypeConsume && payload.Log.LogType != LogTypeRefund {
		return errors.New("task billing finalization log type is invalid")
	}
	if payload.Log.Quota < 0 || payload.Log.Quota > common.MaxQuota {
		return errors.New("task billing finalization log quota is invalid")
	}
	if payload.Log.PromptTokens < 0 || payload.Log.PromptTokens > common.MaxTokensLimit ||
		payload.Log.CompletionTokens < 0 || payload.Log.CompletionTokens > common.MaxTokensLimit ||
		payload.Log.PromptTokens > common.MaxTokensLimit-payload.Log.CompletionTokens {
		return errors.New("task billing finalization log token count is invalid")
	}
	if payload.Log.UseTime < 0 {
		return errors.New("task billing finalization log duration is invalid")
	}
	if payload.Log.SubscriptionPreConsumed < 0 || payload.Log.SubscriptionAmountTotal < 0 ||
		payload.Log.SubscriptionAmountUsedAfterPreConsume < 0 {
		return errors.New("task billing finalization subscription log snapshot is invalid")
	}
	return nil
}

// taskSubmissionIdentityMatches compares only immutable accepted-task
// identity. Polling legitimately changes status, quota, result data, and the
// result URL after insertion, so those fields cannot participate in a retry
// check after an ambiguous transaction commit.
func taskSubmissionIdentityMatches(persisted *Task, candidate *Task) bool {
	if persisted == nil || candidate == nil {
		return false
	}
	return persisted.TaskID == candidate.TaskID &&
		persisted.Platform == candidate.Platform &&
		persisted.UserId == candidate.UserId &&
		persisted.Group == candidate.Group &&
		persisted.ChannelId == candidate.ChannelId &&
		persisted.Action == candidate.Action &&
		persisted.Properties == candidate.Properties &&
		persisted.PrivateData.Key == candidate.PrivateData.Key &&
		persisted.PrivateData.ChannelType == candidate.PrivateData.ChannelType &&
		persisted.PrivateData.ChannelBaseURL == candidate.PrivateData.ChannelBaseURL &&
		persisted.PrivateData.ChannelProxy == candidate.PrivateData.ChannelProxy &&
		persisted.PrivateData.RoutingSnapshotVersion == candidate.PrivateData.RoutingSnapshotVersion &&
		persisted.PrivateData.UpstreamTaskID == candidate.PrivateData.UpstreamTaskID &&
		persisted.PrivateData.BillingSource == candidate.PrivateData.BillingSource &&
		persisted.PrivateData.BillingRequestId == candidate.PrivateData.BillingRequestId &&
		persisted.PrivateData.SubscriptionId == candidate.PrivateData.SubscriptionId &&
		persisted.PrivateData.TokenId == candidate.PrivateData.TokenId &&
		persisted.PrivateData.TokenKeyHash == candidate.PrivateData.TokenKeyHash &&
		persisted.PrivateData.ChannelCreatedTime == candidate.PrivateData.ChannelCreatedTime &&
		persisted.PrivateData.NodeName == candidate.PrivateData.NodeName &&
		reflect.DeepEqual(persisted.PrivateData.BillingContext, candidate.PrivateData.BillingContext)
}

func findPersistedTaskSubmissionTx(tx *gorm.DB, task *Task) (*Task, error) {
	if tx == nil || task == nil {
		return nil, errors.New("task billing finalization task is nil")
	}
	if task.ID > 0 {
		var persisted Task
		result := tx.Where("id = ?", task.ID).Limit(1).Find(&persisted)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected == 0 {
			return nil, nil
		}
		if !taskSubmissionIdentityMatches(&persisted, task) {
			return nil, errors.New("task submission retry conflicts with the persisted task id")
		}
		return &persisted, nil
	}

	var candidates []Task
	if err := tx.Where("task_id = ? AND user_id = ?", task.TaskID, task.UserId).
		Order("id asc").
		Limit(2).
		Find(&candidates).Error; err != nil {
		return nil, err
	}
	var matched *Task
	for i := range candidates {
		if !taskSubmissionIdentityMatches(&candidates[i], task) {
			continue
		}
		if matched != nil {
			return nil, errors.New("task submission retry matched multiple persisted tasks")
		}
		matched = &candidates[i]
	}
	if matched != nil {
		return matched, nil
	}
	if len(candidates) != 0 {
		return nil, errors.New("task submission id was reused with different accepted task context")
	}
	return nil, nil
}

func taskBillingFinalizationAccountingMatches(a TaskBillingFinalizationPayload, b TaskBillingFinalizationPayload) bool {
	return a.Adjustment == b.Adjustment &&
		a.TaskDatabaseID == b.TaskDatabaseID &&
		a.UpdateTaskQuota == b.UpdateTaskQuota &&
		a.TargetTaskQuota == b.TargetTaskQuota &&
		a.UserUsedQuotaDelta == b.UserUsedQuotaDelta &&
		a.IncrementUserRequestCount == b.IncrementUserRequestCount &&
		a.ChannelUsedQuotaDelta == b.ChannelUsedQuotaDelta &&
		a.ChannelCreatedTime == b.ChannelCreatedTime &&
		a.Log.UserID == b.Log.UserID &&
		a.Log.LogType == b.Log.LogType &&
		a.Log.ChannelID == b.Log.ChannelID &&
		a.Log.ModelName == b.Log.ModelName &&
		a.Log.Quota == b.Log.Quota &&
		a.Log.PromptTokens == b.Log.PromptTokens &&
		a.Log.CompletionTokens == b.Log.CompletionTokens &&
		a.Log.TokenID == b.Log.TokenID &&
		a.Log.TokenName == b.Log.TokenName &&
		a.Log.Group == b.Log.Group &&
		a.Log.IsStream == b.Log.IsStream &&
		a.Log.IP == b.Log.IP &&
		a.Log.RequestID == b.Log.RequestID &&
		a.Log.UpstreamRequestID == b.Log.UpstreamRequestID &&
		a.Log.Username == b.Log.Username &&
		a.Log.SubscriptionPreConsumed == b.Log.SubscriptionPreConsumed &&
		a.Log.SubscriptionAmountTotal == b.Log.SubscriptionAmountTotal &&
		a.Log.SubscriptionAmountUsedAfterPreConsume == b.Log.SubscriptionAmountUsedAfterPreConsume &&
		a.Log.NodeName == b.Log.NodeName
}

// EnqueueTaskBillingFinalization persists the outbox before its balance
// adjustment is enqueued. A retry may vary descriptive text, but every
// accounting field must match the first canonical payload.
func enqueueTaskBillingFinalization(tx *gorm.DB, payload TaskBillingFinalizationPayload) (string, error) {
	if err := validateTaskBillingFinalizationPayload(payload); err != nil {
		return "", err
	}
	payloadJSON, err := common.Marshal(payload)
	if err != nil {
		return "", err
	}
	finalizationID := taskBillingFinalizationID(payload)
	record := TaskBillingFinalization{
		FinalizationID: finalizationID,
		Payload:        string(payloadJSON),
		MainStatus:     taskBillingFinalizationPending,
		LogStatus:      taskBillingFinalizationPending,
	}
	if err := tx.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "finalization_id"}},
		DoNothing: true,
	}).Create(&record).Error; err != nil {
		return "", err
	}

	// Insert before taking the user lock. A finalization processor locks the
	// outbox row before the user aggregate, and INSERT ... ON CONFLICT can wait
	// on that row even with DoNothing. Holding the user first would therefore
	// create a user -> outbox / outbox -> user deadlock. Account deletion remains
	// safe: it holds the user while scanning outboxes, so an uncommitted insert
	// either acquires the still-live user and becomes visible atomically or rolls
	// back after deletion wins.
	if err := lockBillingUserTx(tx, payload.Adjustment.UserID); err != nil {
		return "", err
	}

	var persisted TaskBillingFinalization
	if err := tx.Where("finalization_id = ?", finalizationID).First(&persisted).Error; err != nil {
		return "", err
	}
	var canonical TaskBillingFinalizationPayload
	if err := common.UnmarshalJsonStr(persisted.Payload, &canonical); err != nil {
		return "", err
	}
	if !taskBillingFinalizationAccountingMatches(canonical, payload) {
		return "", errors.New("task billing finalization idempotency key reused with different accounting payload")
	}
	return finalizationID, nil
}

// EnqueueTaskBillingFinalization persists an independently created task
// billing finalization. Terminal polling transitions should instead use
// UpdateWithStatusAndBillingFinalization so the terminal state and outbox row
// commit atomically.
func EnqueueTaskBillingFinalization(payload TaskBillingFinalizationPayload) (string, error) {
	var finalizationID string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		finalizationID, err = enqueueTaskBillingFinalization(tx, payload)
		return err
	})
	if err != nil {
		return "", err
	}
	return finalizationID, nil
}

// InsertTaskWithBillingFinalization atomically persists a newly submitted
// asynchronous task and its billing outbox. If either write fails, neither is
// visible, so an accepted upstream task cannot be left permanently charged
// without the durable aggregate/log finalization needed to account for it.
func InsertTaskWithBillingFinalization(task *Task, payload TaskBillingFinalizationPayload) (string, error) {
	if task == nil {
		return "", errors.New("task billing finalization task is nil")
	}
	if err := validateTaskBillingFinalizationPayload(payload); err != nil {
		return "", err
	}
	if payload.UpdateTaskQuota || payload.TaskDatabaseID != 0 {
		return "", errors.New("task submission finalization cannot update an existing task quota")
	}
	if task.UserId != payload.Adjustment.UserID {
		return "", errors.New("task submission owner does not match billing finalization")
	}
	if task.ChannelId != payload.Log.ChannelID {
		return "", errors.New("task submission channel does not match billing finalization")
	}
	var finalizationID string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		// The outbox is the accepted submission's durable idempotency boundary.
		// Persist it first so concurrent retries serialize before either can
		// insert a duplicate task row. enqueueTaskBillingFinalization then locks
		// the user, establishing outbox -> user as the common billing order.
		finalizationID, err = enqueueTaskBillingFinalization(tx, payload)
		if err != nil {
			return err
		}

		// A commit error can be ambiguous: the database may have committed even
		// though the client did not receive the acknowledgement. Replaying with
		// the same task/outbox must therefore return the canonical task instead
		// of failing and causing the caller to refund an accepted upstream job.
		// Check this before consulting mutable channel lifecycle state: the
		// accepted row remains canonical even if its channel was deleted after
		// the original transaction committed.
		persisted, err := findPersistedTaskSubmissionTx(tx, task)
		if err != nil {
			return err
		}
		if persisted != nil {
			*task = *persisted
			return nil
		}

		if task.PrivateData.ChannelCreatedTime != 0 {
			var channel Channel
			if err := lockForUpdate(tx).
				Select("id", "created_time").
				Where("id = ?", task.ChannelId).
				First(&channel).Error; err != nil {
				return err
			}
			if channel.CreatedTime != task.PrivateData.ChannelCreatedTime {
				return errors.New("task submission channel identity changed before persistence")
			}
		}

		return tx.Create(task).Error
	})
	if err != nil {
		return "", err
	}
	return finalizationID, nil
}

// UpdateWithStatusAndBillingFinalization applies a task CAS and, when payload
// is non-nil, creates its billing outbox entry in the same main-database
// transaction. A lost CAS never creates an outbox row, while an enqueue error
// rolls the task transition back so a later polling pass can retry it.
func (t *Task) UpdateWithStatusAndBillingFinalization(fromStatus TaskStatus, payload *TaskBillingFinalizationPayload) (bool, string, error) {
	if t.ID <= 0 {
		return false, "", errors.New("task billing transition requires a persisted task")
	}
	if payload != nil {
		if payload.TaskDatabaseID != 0 && payload.TaskDatabaseID != t.ID {
			return false, "", errors.New("task billing transition finalization targets another task")
		}
		if payload.Adjustment.UserID != t.UserId {
			return false, "", errors.New("task billing transition finalization targets another user")
		}
		if payload.Log.ChannelID != t.ChannelId {
			return false, "", errors.New("task billing transition finalization targets another channel")
		}
		canonicalPayload := *payload
		canonicalPayload.TaskDatabaseID = t.ID
		payload = &canonicalPayload
	}

	var won bool
	var finalizationID string
	err := DB.Transaction(func(tx *gorm.DB) error {
		update := tx.Model(t).Where("status = ?", fromStatus).Select("*").Updates(t)
		if update.Error != nil {
			return update.Error
		}
		if update.RowsAffected == 0 {
			return nil
		}
		won = true
		if payload == nil {
			return nil
		}

		var err error
		finalizationID, err = enqueueTaskBillingFinalization(tx, *payload)
		return err
	})
	if err != nil {
		return false, "", err
	}
	return won, finalizationID, nil
}

func loadTaskBillingFinalization(finalizationID string) (*TaskBillingFinalization, *TaskBillingFinalizationPayload, error) {
	var record TaskBillingFinalization
	if err := DB.Where("finalization_id = ?", finalizationID).First(&record).Error; err != nil {
		return nil, nil, err
	}
	var payload TaskBillingFinalizationPayload
	if err := common.UnmarshalJsonStr(record.Payload, &payload); err != nil {
		return nil, nil, err
	}
	if err := validateTaskBillingFinalizationPayload(payload); err != nil {
		return nil, nil, err
	}
	if taskBillingFinalizationID(payload) != finalizationID {
		return nil, nil, errors.New("task billing finalization id does not match payload")
	}
	return &record, &payload, nil
}

func validateTaskBillingFinalizationReference(payload *TaskBillingFinalizationPayload) error {
	if payload == nil || payload.TaskDatabaseID <= 0 {
		return nil
	}
	var task Task
	if err := DB.Select("id", "user_id", "channel_id").
		Where("id = ?", payload.TaskDatabaseID).
		First(&task).Error; err != nil {
		return err
	}
	if task.UserId != payload.Adjustment.UserID {
		return errors.New("task billing finalization task owner does not match adjustment")
	}
	if task.ChannelId != payload.Log.ChannelID {
		return errors.New("task billing finalization task channel does not match log")
	}
	return nil
}

func finalizeTaskBillingMain(finalizationID string, payload *TaskBillingFinalizationPayload) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var record TaskBillingFinalization
		if err := lockForUpdate(tx).Where("finalization_id = ?", finalizationID).First(&record).Error; err != nil {
			return err
		}
		if record.MainStatus == taskBillingFinalizationSucceeded {
			return nil
		}

		var adjustmentTask SystemTask
		if err := tx.Where("task_id = ? AND type = ?", finalizationID, SystemTaskTypeBillingAdjustment).First(&adjustmentTask).Error; err != nil {
			return err
		}
		if adjustmentTask.Status != SystemTaskStatusSucceeded {
			return fmt.Errorf("task billing adjustment %s is not complete", finalizationID)
		}

		if payload.TaskDatabaseID > 0 {
			var task Task
			if err := lockForUpdate(tx).Where("id = ?", payload.TaskDatabaseID).First(&task).Error; err != nil {
				return err
			}
			if task.UserId != payload.Adjustment.UserID {
				return errors.New("task billing finalization task owner does not match adjustment")
			}
			if task.ChannelId != payload.Log.ChannelID {
				return errors.New("task billing finalization task channel does not match log")
			}
			if payload.UpdateTaskQuota && task.Quota != payload.TargetTaskQuota {
				update := tx.Model(&task).Update("quota", payload.TargetTaskQuota)
				if update.Error != nil {
					return update.Error
				}
				if update.RowsAffected == 0 {
					return errors.New("task billing finalization task quota update was not applied")
				}
			}
		}
		if payload.UserUsedQuotaDelta != 0 || payload.IncrementUserRequestCount {
			var user User
			if err := lockForUpdate(tx.Unscoped()).Where("id = ?", payload.Adjustment.UserID).First(&user).Error; err != nil {
				return err
			}
			requestCountDelta := 0
			if payload.IncrementUserRequestCount {
				requestCountDelta = 1
			}
			usedQuota := saturatingQuotaAggregate(user.UsedQuota, payload.UserUsedQuotaDelta, "user used quota")
			requestCount := saturatingQuotaAggregate(user.RequestCount, requestCountDelta, "user request count")
			update := tx.Unscoped().Model(&user).Updates(map[string]interface{}{
				"used_quota":    usedQuota,
				"request_count": requestCount,
			})
			if update.Error != nil {
				return update.Error
			}
			// The row was read under lock above. Do not interpret RowsAffected
			// here: MySQL reports zero for a matched no-op update when both
			// aggregates were already saturated, while PostgreSQL and SQLite
			// report the matched row. Saturation must not wedge the outbox.
		}
		if payload.ChannelUsedQuotaDelta != 0 {
			var channel Channel
			err := lockForUpdate(tx).Where("id = ?", payload.Log.ChannelID).First(&channel).Error
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if err == nil {
				if payload.ChannelCreatedTime != 0 && channel.CreatedTime != payload.ChannelCreatedTime {
					// Channels can be hard-deleted and imported with an explicit
					// reused ID. A delayed outbox must not attribute the original
					// request's usage to that replacement. Channel aggregates are
					// non-financial, so skip the stale target and let the durable
					// balance/log phases complete.
					common.SysError(fmt.Sprintf(
						"billing channel identity changed before finalization; channel=%d expected_created=%d actual_created=%d",
						channel.Id,
						payload.ChannelCreatedTime,
						channel.CreatedTime,
					))
				} else {
					usedQuota := saturatingInt64Aggregate(channel.UsedQuota, int64(payload.ChannelUsedQuotaDelta), "channel used quota")
					update := tx.Model(&channel).Update("used_quota", usedQuota)
					if update.Error != nil {
						return update.Error
					}
					// As with user aggregates, a saturated value is a legitimate
					// no-op and MySQL may report zero affected rows for it.
				}
			}
		}
		return tx.Model(&record).Updates(map[string]interface{}{
			"main_status": taskBillingFinalizationSucceeded,
			"updated_at":  common.GetTimestamp(),
		}).Error
	})
}

func claimTaskBillingLog(finalizationID string) (string, bool, error) {
	claimToken := common.GetUUID()
	nowSQL := mainDatabaseUnixTimestampSQL()
	update := DB.Model(&TaskBillingFinalization{}).
		Where(
			"finalization_id = ? AND (log_status = ? OR (log_status = ? AND log_claimed_at <= ("+nowSQL+") - ?))",
			finalizationID,
			taskBillingFinalizationPending,
			taskBillingFinalizationProcessing,
			taskBillingLogClaimSeconds,
		).
		Updates(map[string]interface{}{
			"log_status":      taskBillingFinalizationProcessing,
			"log_claim_token": claimToken,
			"log_claimed_at":  gorm.Expr(nowSQL),
			"updated_at":      gorm.Expr(nowSQL),
		})
	if update.Error != nil || update.RowsAffected != 0 {
		return claimToken, update.RowsAffected != 0, update.Error
	}
	var record TaskBillingFinalization
	if err := DB.Select("id").Where("finalization_id = ?", finalizationID).First(&record).Error; err != nil {
		return claimToken, false, err
	}
	return claimToken, false, nil
}

func resetTaskBillingLogClaim(finalizationID string, claimToken string) {
	if err := DB.Model(&TaskBillingFinalization{}).
		Where("finalization_id = ? AND log_claim_token = ? AND log_status = ?", finalizationID, claimToken, taskBillingFinalizationProcessing).
		Updates(map[string]interface{}{
			"log_status":      taskBillingFinalizationPending,
			"log_claim_token": "",
			"log_claimed_at":  0,
			"updated_at":      gorm.Expr(mainDatabaseUnixTimestampSQL()),
		}).Error; err != nil {
		common.SysError(fmt.Sprintf("failed to reset task billing log claim %q: %v", finalizationID, err))
	}
}

func finalizeTaskBillingLog(finalizationID string, payload *TaskBillingFinalizationPayload, adjustmentResult BillingAdjustmentResult) error {
	claimToken, claimed, err := claimTaskBillingLog(finalizationID)
	if err != nil || !claimed {
		return err
	}

	other := payload.Log.Other
	if other == nil {
		other = map[string]interface{}{}
	}
	if payload.Adjustment.FundingSource == BillingAdjustmentSubscription {
		if adjustmentResult.SubscriptionDelta != 0 {
			other["subscription_post_delta"] = adjustmentResult.SubscriptionDelta
		} else {
			delete(other, "subscription_post_delta")
		}
		if adjustmentResult.WalletDelta > 0 {
			other["subscription_wallet_overflow"] = adjustmentResult.WalletDelta
		} else {
			delete(other, "subscription_wallet_overflow")
		}
		other["wallet_quota_deducted"] = adjustmentResult.WalletDelta

		consumed := saturatingInt64Aggregate(
			payload.Log.SubscriptionPreConsumed,
			int64(adjustmentResult.SubscriptionDelta),
			"task billing log subscription consumed",
		)
		if consumed > 0 {
			other["subscription_consumed"] = consumed
		} else {
			delete(other, "subscription_consumed")
		}
		usedFinal := saturatingInt64Aggregate(
			payload.Log.SubscriptionAmountUsedAfterPreConsume,
			int64(adjustmentResult.SubscriptionDelta),
			"task billing log subscription used",
		)
		if payload.Log.SubscriptionAmountTotal > 0 {
			remaining := payload.Log.SubscriptionAmountTotal - usedFinal
			if remaining < 0 {
				remaining = 0
			}
			other["subscription_total"] = payload.Log.SubscriptionAmountTotal
			other["subscription_used"] = usedFinal
			other["subscription_remain"] = remaining
		}
	}

	err = RecordTaskBillingLogWithRequestID(RecordTaskBillingLogParams{
		UserId:            payload.Log.UserID,
		LogType:           payload.Log.LogType,
		Content:           payload.Log.Content,
		ChannelId:         payload.Log.ChannelID,
		ModelName:         payload.Log.ModelName,
		Quota:             payload.Log.Quota,
		PromptTokens:      payload.Log.PromptTokens,
		CompletionTokens:  payload.Log.CompletionTokens,
		TokenId:           payload.Log.TokenID,
		TokenName:         payload.Log.TokenName,
		Group:             payload.Log.Group,
		UseTime:           payload.Log.UseTime,
		IsStream:          payload.Log.IsStream,
		IP:                payload.Log.IP,
		RequestID:         payload.Log.RequestID,
		UpstreamRequestID: payload.Log.UpstreamRequestID,
		Username:          payload.Log.Username,
		Other:             other,
		NodeName:          payload.Log.NodeName,
	}, finalizationID, payload.Log.CreatedAt)
	if err != nil {
		resetTaskBillingLogClaim(finalizationID, claimToken)
		return err
	}

	update := DB.Model(&TaskBillingFinalization{}).
		Where("finalization_id = ? AND log_claim_token = ? AND log_status = ?", finalizationID, claimToken, taskBillingFinalizationProcessing).
		Updates(map[string]interface{}{
			"log_status":      taskBillingFinalizationSucceeded,
			"log_claim_token": "",
			"log_claimed_at":  0,
			"updated_at":      gorm.Expr(mainDatabaseUnixTimestampSQL()),
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected == 0 {
		return errors.New("task billing finalization log claim was lost")
	}
	return nil
}

// ProcessTaskBillingFinalization replays every phase. The balance adjustment
// remains exactly once, main-database aggregates are committed with their
// marker, and a deterministic log request ID makes a post-insert crash safe to
// retry even when LOG_DB is separate.
func ProcessTaskBillingFinalization(finalizationID string) (BillingAdjustmentResult, error) {
	_, payload, err := loadTaskBillingFinalization(finalizationID)
	if err != nil {
		return BillingAdjustmentResult{}, err
	}
	// Validate immutable task ownership before applying the balance adjustment.
	// Rechecking under the task lock in finalizeTaskBillingMain protects the
	// later quota write as well.
	if err := validateTaskBillingFinalizationReference(payload); err != nil {
		return BillingAdjustmentResult{}, err
	}
	adjustmentTaskID, err := EnqueueBillingAdjustment(payload.Adjustment)
	if err != nil {
		return BillingAdjustmentResult{}, err
	}
	if adjustmentTaskID != finalizationID {
		return BillingAdjustmentResult{}, errors.New("task billing finalization adjustment id mismatch")
	}
	result, err := ProcessBillingAdjustmentWithResult(adjustmentTaskID)
	if err != nil {
		return BillingAdjustmentResult{}, err
	}
	if err := finalizeTaskBillingMain(finalizationID, payload); err != nil {
		return result, err
	}
	if err := finalizeTaskBillingLog(finalizationID, payload, result); err != nil {
		return result, err
	}
	return result, nil
}

func ProcessPendingTaskBillingFinalizations(limit int) error {
	if limit <= 0 {
		limit = 100
	}
	var records []TaskBillingFinalization
	if err := DB.Where("main_status <> ? OR log_status <> ?", taskBillingFinalizationSucceeded, taskBillingFinalizationSucceeded).
		Order("updated_at asc").Order("id asc").Limit(limit).Find(&records).Error; err != nil {
		return err
	}
	var processErrors []error
	for _, record := range records {
		if _, err := ProcessTaskBillingFinalization(record.FinalizationID); err != nil {
			// A future-by-one-second retry timestamp deterministically rotates a
			// failed row behind work created during the same database second.
			if rotateErr := DB.Model(&TaskBillingFinalization{}).
				Where("id = ? AND (main_status <> ? OR log_status <> ?)",
					record.ID, taskBillingFinalizationSucceeded, taskBillingFinalizationSucceeded).
				Update("updated_at", gorm.Expr(mainDatabaseUnixTimestampSQL()+" + 1")).Error; rotateErr != nil {
				processErrors = append(processErrors, fmt.Errorf("%s retry rotation: %w", record.FinalizationID, rotateErr))
			}
			processErrors = append(processErrors, fmt.Errorf("%s: %w", record.FinalizationID, err))
		}
	}
	return errors.Join(processErrors...)
}

func CleanupTaskBillingFinalizations(olderThanSeconds int64) (int64, error) {
	// A succeeded row is the exactly-once marker for user/channel aggregates.
	// The corresponding billing SystemTask is also retained permanently. If
	// this marker were deleted, a delayed replay would reuse the completed
	// balance task but apply the aggregate deltas again.
	return 0, nil
}
