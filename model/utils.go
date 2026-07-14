package model

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"

	"github.com/bytedance/gopkg/util/gopool"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BatchUpdateTypeUserQuota = iota
	BatchUpdateTypeTokenQuota
	BatchUpdateTypeUsedQuota
	BatchUpdateTypeChannelUsedQuota
	BatchUpdateTypeRequestCount
	BatchUpdateTypeCount // if you add a new type, you need to add a new map and a new lock
)

var batchUpdateStores []map[int]int
var batchUpdateLocks []sync.Mutex
var batchUpdaterOnce sync.Once

func init() {
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateStores = append(batchUpdateStores, make(map[int]int))
		batchUpdateLocks = append(batchUpdateLocks, sync.Mutex{})
	}
}

func InitBatchUpdater() {
	batchUpdaterOnce.Do(func() {
		gopool.Go(func() {
			for {
				time.Sleep(time.Duration(common.BatchUpdateInterval) * time.Second)
				if err := FlushBatchUpdates(); err != nil {
					common.SysLog("batch update finished with pending retries: " + err.Error())
				}
			}
		})
	})
}

func addNewRecord(type_ int, id int, value int) {
	batchUpdateLocks[type_].Lock()
	defer batchUpdateLocks[type_].Unlock()
	if _, ok := batchUpdateStores[type_][id]; !ok {
		batchUpdateStores[type_][id] = value
	} else {
		batchUpdateStores[type_][id] += value
	}
}

// FlushBatchUpdates persists one snapshot of the in-memory metric counters.
// Failed deltas are merged back into the live stores so a transient database
// failure cannot silently discard them. User and token balances deliberately
// bypass this mechanism; financial mutations must be durable before their
// callers are told that they succeeded.
func FlushBatchUpdates() error {
	// check if there's any data to update
	hasData := false
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		if len(batchUpdateStores[i]) > 0 {
			hasData = true
			batchUpdateLocks[i].Unlock()
			break
		}
		batchUpdateLocks[i].Unlock()
	}

	if !hasData {
		return nil
	}

	common.SysLog("batch update started")
	stores := make([]map[int]int, BatchUpdateTypeCount)
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		stores[i] = batchUpdateStores[i]
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}

	var flushErrors []error
	for i, store := range stores {
		if i == BatchUpdateTypeUserQuota || i == BatchUpdateTypeUsedQuota || i == BatchUpdateTypeRequestCount {
			continue
		}
		for key, value := range store {
			var err error
			switch i {
			case BatchUpdateTypeTokenQuota:
				err = increaseTokenQuota(key, value)
			case BatchUpdateTypeChannelUsedQuota:
				err = DB.Model(&Channel{}).Where("id = ?", key).
					Update("used_quota", gorm.Expr("used_quota + ?", value)).Error
			}
			if err != nil {
				addNewRecord(i, key, value)
				flushErrors = append(flushErrors, fmt.Errorf("batch type %d id %d: %w", i, key, err))
			}
		}
	}

	userQuotaStore := stores[BatchUpdateTypeUserQuota]
	usedQuotaStore := stores[BatchUpdateTypeUsedQuota]
	requestCountStore := stores[BatchUpdateTypeRequestCount]

	userIDs := make(map[int]struct{}, len(userQuotaStore)+len(usedQuotaStore)+len(requestCountStore))
	for key := range userQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range usedQuotaStore {
		userIDs[key] = struct{}{}
	}
	for key := range requestCountStore {
		userIDs[key] = struct{}{}
	}
	for key := range userIDs {
		quota := userQuotaStore[key]
		usedQuota := usedQuotaStore[key]
		requestCount := requestCountStore[key]
		if quota == 0 && usedQuota == 0 && requestCount == 0 {
			continue
		}
		err := DB.Model(&User{}).Where("id = ?", key).Updates(
			map[string]interface{}{
				"quota":         gorm.Expr("quota + ?", quota),
				"used_quota":    gorm.Expr("used_quota + ?", usedQuota),
				"request_count": gorm.Expr("request_count + ?", requestCount),
			},
		).Error
		if err != nil {
			addNewRecord(BatchUpdateTypeUserQuota, key, quota)
			addNewRecord(BatchUpdateTypeUsedQuota, key, usedQuota)
			addNewRecord(BatchUpdateTypeRequestCount, key, requestCount)
			flushErrors = append(flushErrors, fmt.Errorf("batch user id %d: %w", key, err))
		}
	}
	common.SysLog("batch update finished")
	return errors.Join(flushErrors...)
}

const (
	SystemTaskTypeBillingAdjustment = "billing_adjustment"
	BillingAdjustmentSettle         = "settle"
	BillingAdjustmentRefund         = "refund"
	BillingAdjustmentWallet         = "wallet"
	BillingAdjustmentSubscription   = "subscription"
)

// BillingAdjustment is a durable, idempotent accounting operation. Positive
// deltas consume quota and negative deltas refund it. The adjustment and its
// completion marker are committed in the same database transaction.
type BillingAdjustment struct {
	RequestID             string `json:"request_id"`
	Kind                  string `json:"kind"`
	FundingSource         string `json:"funding_source"`
	UserID                int    `json:"user_id"`
	SubscriptionID        int    `json:"subscription_id,omitempty"`
	SubscriptionRequestID string `json:"subscription_request_id,omitempty"`
	TokenID               int    `json:"token_id,omitempty"`
	FundingDelta          int    `json:"funding_delta"`
	TokenDelta            int    `json:"token_delta"`
	ExtraReserved         int64  `json:"extra_reserved,omitempty"`
}

// BillingAdjustmentResult records how a durable adjustment was distributed.
// A subscription settlement can cross the plan boundary: plans that allow
// wallet overflow consume the remaining subscription quota first and charge
// the rest to the wallet, while strict plans retain the overage on the
// subscription so a delivered response is accounted for without touching the
// wallet.
type BillingAdjustmentResult struct {
	SubscriptionDelta int `json:"subscription_delta,omitempty"`
	WalletDelta       int `json:"wallet_delta,omitempty"`
	TokenDelta        int `json:"token_delta,omitempty"`
}

func billingAdjustmentTaskID(requestID string, kind string) string {
	digest := sha256.Sum256([]byte(requestID + "\x00" + kind))
	return fmt.Sprintf("billing_%x", digest[:24])
}

// EnqueueBillingAdjustment persists an adjustment before any balance changes
// are attempted. Reusing the same request/kind pair is safe and returns the
// original task, while a payload mismatch is rejected.
func EnqueueBillingAdjustment(adjustment BillingAdjustment) (string, error) {
	if adjustment.RequestID == "" {
		return "", errors.New("billing adjustment request id is empty")
	}
	if adjustment.Kind != BillingAdjustmentSettle && adjustment.Kind != BillingAdjustmentRefund {
		return "", fmt.Errorf("invalid billing adjustment kind: %s", adjustment.Kind)
	}
	if adjustment.FundingSource != BillingAdjustmentWallet && adjustment.FundingSource != BillingAdjustmentSubscription {
		return "", fmt.Errorf("invalid billing adjustment funding source: %s", adjustment.FundingSource)
	}
	if adjustment.UserID <= 0 {
		return "", errors.New("billing adjustment user id is invalid")
	}
	if adjustment.FundingSource == BillingAdjustmentSubscription && adjustment.SubscriptionID <= 0 {
		return "", errors.New("billing adjustment subscription id is invalid")
	}
	if adjustment.FundingDelta > common.MaxQuota || adjustment.FundingDelta < common.MinQuota ||
		adjustment.TokenDelta > common.MaxQuota || adjustment.TokenDelta < common.MinQuota {
		return "", errors.New("billing adjustment delta exceeds quota storage range")
	}
	if adjustment.Kind == BillingAdjustmentRefund && (adjustment.FundingDelta > 0 || adjustment.TokenDelta > 0 || adjustment.ExtraReserved < 0) {
		return "", errors.New("billing refund contains an invalid delta")
	}
	payload, err := common.Marshal(adjustment)
	if err != nil {
		return "", err
	}
	taskID := billingAdjustmentTaskID(adjustment.RequestID, adjustment.Kind)
	task := &SystemTask{
		TaskID:  taskID,
		Type:    SystemTaskTypeBillingAdjustment,
		Status:  SystemTaskStatusPending,
		Payload: string(payload),
	}
	if err := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(task).Error; err != nil {
		return "", err
	}

	var persisted SystemTask
	if err := DB.Where("task_id = ? AND type = ?", taskID, SystemTaskTypeBillingAdjustment).First(&persisted).Error; err != nil {
		return "", err
	}
	if persisted.Payload != string(payload) {
		return "", errors.New("billing adjustment idempotency key reused with different payload")
	}
	return taskID, nil
}

func lockBillingSubscriptionTx(tx *gorm.DB, subscriptionID int, userID int) (*UserSubscription, error) {
	if subscriptionID <= 0 {
		return nil, errors.New("billing adjustment subscription id is invalid")
	}
	var subscription UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", subscriptionID).First(&subscription).Error; err != nil {
		return nil, err
	}
	if userID <= 0 || subscription.UserId != userID {
		return nil, errors.New("billing adjustment subscription does not belong to user")
	}
	return &subscription, nil
}

func updateSubscriptionUsedTx(tx *gorm.DB, subscription *UserSubscription, delta int64, capAtTotal bool) (int64, error) {
	if subscription == nil {
		return 0, errors.New("billing adjustment subscription is nil")
	}
	if subscription.AmountUsed < 0 || subscription.AmountTotal < 0 {
		return 0, errors.New("subscription contains invalid quota values")
	}
	if delta > 0 && subscription.AmountUsed > math.MaxInt64-delta {
		return 0, errors.New("subscription used quota overflow")
	}
	if delta < 0 && subscription.AmountUsed < math.MinInt64-delta {
		return 0, errors.New("subscription used quota underflow")
	}
	newUsed := subscription.AmountUsed + delta
	if newUsed < 0 {
		newUsed = 0
	}
	if capAtTotal && subscription.AmountTotal > 0 && newUsed > subscription.AmountTotal {
		newUsed = subscription.AmountTotal
		if subscription.AmountUsed > newUsed {
			newUsed = subscription.AmountUsed
		}
	}
	applied := newUsed - subscription.AmountUsed
	if applied == 0 {
		return 0, nil
	}
	if err := tx.Model(subscription).Update("amount_used", newUsed).Error; err != nil {
		return 0, err
	}
	return applied, nil
}

func checkedQuotaBalanceDelta(current int, delta int) (int, error) {
	value := int64(current) + int64(delta)
	if value > int64(common.MaxQuota) || value < int64(common.MinQuota) {
		return 0, fmt.Errorf("quota balance overflow: current=%d delta=%d", current, delta)
	}
	return int(value), nil
}

func applyWalletBillingDeltaTx(tx *gorm.DB, userID int, delta int) error {
	if delta == 0 {
		return nil
	}
	var user User
	if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
		return err
	}
	quota, err := checkedQuotaBalanceDelta(user.Quota, -delta)
	if err != nil {
		return err
	}
	return tx.Model(&user).Update("quota", quota).Error
}

func applyBillingAdjustmentTx(tx *gorm.DB, adjustment BillingAdjustment) (BillingAdjustmentResult, error) {
	result := BillingAdjustmentResult{}
	if adjustment.FundingSource == BillingAdjustmentWallet {
		if adjustment.FundingDelta != 0 {
			if err := applyWalletBillingDeltaTx(tx, adjustment.UserID, adjustment.FundingDelta); err != nil {
				return BillingAdjustmentResult{}, err
			}
			result.WalletDelta = adjustment.FundingDelta
		}
	} else if adjustment.Kind == BillingAdjustmentRefund {
		if adjustment.SubscriptionRequestID == "" {
			if adjustment.FundingDelta != 0 {
				subscription, err := lockBillingSubscriptionTx(tx, adjustment.SubscriptionID, adjustment.UserID)
				if err != nil {
					return BillingAdjustmentResult{}, err
				}
				applied, err := updateSubscriptionUsedTx(tx, subscription, int64(adjustment.FundingDelta), false)
				if err != nil {
					return BillingAdjustmentResult{}, err
				}
				result.SubscriptionDelta = int(applied)
			}
		} else {
			var record SubscriptionPreConsumeRecord
			err := lockForUpdate(tx).Where("request_id = ?", adjustment.SubscriptionRequestID).First(&record).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				// Cleanup may remove an old pre-consume record while its durable
				// refund is still pending. The task itself is the idempotency marker,
				// so the captured delta remains safe to apply transactionally.
				if adjustment.FundingDelta < 0 {
					subscription, err := lockBillingSubscriptionTx(tx, adjustment.SubscriptionID, adjustment.UserID)
					if err != nil {
						return BillingAdjustmentResult{}, err
					}
					applied, err := updateSubscriptionUsedTx(tx, subscription, int64(adjustment.FundingDelta), false)
					if err != nil {
						return BillingAdjustmentResult{}, err
					}
					result.SubscriptionDelta = int(applied)
				}
			} else if err != nil {
				return BillingAdjustmentResult{}, err
			} else if record.Status != "refunded" {
				if record.UserId != adjustment.UserID || record.UserSubscriptionId != adjustment.SubscriptionID {
					return BillingAdjustmentResult{}, errors.New("subscription refund record does not match adjustment")
				}
				if record.PreConsumed < 0 {
					return BillingAdjustmentResult{}, errors.New("subscription pre-consume record contains negative quota")
				}
				if adjustment.ExtraReserved > math.MaxInt64-record.PreConsumed {
					return BillingAdjustmentResult{}, errors.New("subscription refund quota overflow")
				}
				refundAmount := record.PreConsumed + adjustment.ExtraReserved
				if int64(adjustment.FundingDelta) != -refundAmount {
					return BillingAdjustmentResult{}, errors.New("subscription refund delta does not match pre-consume record")
				}
				if refundAmount > 0 {
					subscription, err := lockBillingSubscriptionTx(tx, record.UserSubscriptionId, adjustment.UserID)
					if err != nil {
						return BillingAdjustmentResult{}, err
					}
					applied, err := updateSubscriptionUsedTx(tx, subscription, -refundAmount, false)
					if err != nil {
						return BillingAdjustmentResult{}, err
					}
					result.SubscriptionDelta = int(applied)
				}
				if err := tx.Model(&record).Updates(map[string]interface{}{
					"status":     "refunded",
					"updated_at": common.GetTimestamp(),
				}).Error; err != nil {
					return BillingAdjustmentResult{}, err
				}
			}
		}
	} else if adjustment.FundingDelta != 0 {
		subscription, err := lockBillingSubscriptionTx(tx, adjustment.SubscriptionID, adjustment.UserID)
		if err != nil {
			return BillingAdjustmentResult{}, err
		}
		capAtTotal := false
		if adjustment.FundingDelta > 0 && subscription.AllowWalletOverflow {
			var strictCount int64
			if err := tx.Model(&UserSubscription{}).
				Where("user_id = ? AND status = ? AND end_time > ? AND allow_wallet_overflow = ?",
					adjustment.UserID, "active", getDBTimestampTx(tx), false).
				Count(&strictCount).Error; err != nil {
				return BillingAdjustmentResult{}, err
			}
			capAtTotal = strictCount == 0
		}
		applied, err := updateSubscriptionUsedTx(tx, subscription, int64(adjustment.FundingDelta), capAtTotal)
		if err != nil {
			return BillingAdjustmentResult{}, err
		}
		result.SubscriptionDelta = int(applied)
		overflow := adjustment.FundingDelta - result.SubscriptionDelta
		if overflow > 0 {
			if err := applyWalletBillingDeltaTx(tx, adjustment.UserID, overflow); err != nil {
				return BillingAdjustmentResult{}, err
			}
			result.WalletDelta = overflow
		}
	}

	if adjustment.TokenDelta != 0 {
		var token Token
		if err := lockForUpdate(tx.Unscoped()).Where("id = ?", adjustment.TokenID).First(&token).Error; err != nil {
			// A token can be deleted after the provider response was delivered.
			// Its absence must not prevent charging/refunding the user's durable
			// funding source; there is no remaining token balance to reconcile.
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return result, nil
			}
			return BillingAdjustmentResult{}, err
		}
		remainQuota, err := checkedQuotaBalanceDelta(token.RemainQuota, -adjustment.TokenDelta)
		if err != nil {
			return BillingAdjustmentResult{}, err
		}
		usedQuota, err := checkedQuotaBalanceDelta(token.UsedQuota, adjustment.TokenDelta)
		if err != nil {
			return BillingAdjustmentResult{}, err
		}
		if err := tx.Model(&token).Updates(map[string]interface{}{
			"remain_quota":  remainQuota,
			"used_quota":    usedQuota,
			"accessed_time": common.GetTimestamp(),
		}).Error; err != nil {
			return BillingAdjustmentResult{}, err
		}
		result.TokenDelta = adjustment.TokenDelta
	}
	return result, nil
}

// ProcessBillingAdjustmentWithResult applies one queued operation exactly
// once and returns its persisted funding split. Row locking prevents multiple
// nodes from processing it concurrently, and the balance changes roll back if
// the succeeded marker cannot be written.
func ProcessBillingAdjustmentWithResult(taskID string) (BillingAdjustmentResult, error) {
	var adjustment BillingAdjustment
	var appliedResult BillingAdjustmentResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var task SystemTask
		if err := lockForUpdate(tx).Where("task_id = ? AND type = ?", taskID, SystemTaskTypeBillingAdjustment).First(&task).Error; err != nil {
			return err
		}
		if task.Status == SystemTaskStatusSucceeded {
			if task.Result == "" {
				// Adjustments completed before split results were persisted remain
				// valid idempotency markers. They could only have charged their one
				// declared funding source, so reconstruct that legacy result.
				if err := common.UnmarshalJsonStr(task.Payload, &adjustment); err != nil {
					return err
				}
				appliedResult.TokenDelta = adjustment.TokenDelta
				if adjustment.FundingSource == BillingAdjustmentWallet {
					appliedResult.WalletDelta = adjustment.FundingDelta
				} else {
					appliedResult.SubscriptionDelta = adjustment.FundingDelta
				}
				return nil
			}
			return common.UnmarshalJsonStr(task.Result, &appliedResult)
		}
		if task.Status != SystemTaskStatusPending {
			return fmt.Errorf("billing adjustment %s has invalid status %s", taskID, task.Status)
		}
		if err := common.UnmarshalJsonStr(task.Payload, &adjustment); err != nil {
			return err
		}
		result, err := applyBillingAdjustmentTx(tx, adjustment)
		if err != nil {
			return err
		}
		appliedResult = result
		resultJSON, err := common.Marshal(result)
		if err != nil {
			return err
		}
		updateResult := tx.Model(&SystemTask{}).
			Where("id = ? AND status = ?", task.ID, SystemTaskStatusPending).
			Updates(map[string]interface{}{
				"status":     SystemTaskStatusSucceeded,
				"active_key": nil,
				"error":      "",
				"result":     string(resultJSON),
				"updated_at": common.GetTimestamp(),
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return errors.New("billing adjustment status changed concurrently")
		}
		return nil
	})
	if err != nil {
		_ = DB.Model(&SystemTask{}).
			Where("task_id = ? AND status = ?", taskID, SystemTaskStatusPending).
			Updates(map[string]interface{}{"error": err.Error(), "updated_at": common.GetTimestamp()}).Error
		return BillingAdjustmentResult{}, err
	}

	if common.RedisEnabled && adjustment.UserID > 0 {
		_ = invalidateUserCache(adjustment.UserID)
	}
	if common.RedisEnabled && adjustment.TokenID > 0 {
		var tokenKey string
		if err := DB.Model(&Token{}).Where("id = ?", adjustment.TokenID).Select(commonKeyCol).Find(&tokenKey).Error; err == nil && tokenKey != "" {
			_ = cacheDeleteToken(tokenKey)
		}
	}
	return appliedResult, nil
}

func ProcessBillingAdjustment(taskID string) error {
	_, err := ProcessBillingAdjustmentWithResult(taskID)
	return err
}

// ProcessPendingBillingAdjustments retries durable operations left behind by
// transient failures or an earlier process shutdown.
func ProcessPendingBillingAdjustments(limit int) error {
	if limit <= 0 {
		limit = 100
	}
	var tasks []SystemTask
	if err := DB.Where("type = ? AND status = ?", SystemTaskTypeBillingAdjustment, SystemTaskStatusPending).
		Order("id asc").Limit(limit).Find(&tasks).Error; err != nil {
		return err
	}
	var processErrors []error
	for _, task := range tasks {
		if err := ProcessBillingAdjustment(task.TaskID); err != nil {
			processErrors = append(processErrors, fmt.Errorf("%s: %w", task.TaskID, err))
		}
	}
	return errors.Join(processErrors...)
}

func RecordExist(err error) (bool, error) {
	if err == nil {
		return true, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return false, err
}

func shouldUpdateRedis(fromDB bool, err error) bool {
	return common.RedisEnabled && fromDB && err == nil
}
