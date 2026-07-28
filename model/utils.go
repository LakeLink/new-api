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

var batchUpdateStores []map[int]int64
var batchUpdateLocks []sync.Mutex
var batchUpdaterOnce sync.Once

func init() {
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateStores = append(batchUpdateStores, make(map[int]int64))
		batchUpdateLocks = append(batchUpdateLocks, sync.Mutex{})
	}
}

func InitBatchUpdater() {
	batchUpdaterOnce.Do(func() {
		gopool.Go(func() {
			for {
				time.Sleep(common.SafeIntervalDuration(
					common.BatchUpdateInterval,
					time.Second,
					5*time.Second,
					"batch update",
				))
				if err := FlushBatchUpdates(); err != nil {
					common.SysLog("batch update finished with pending retries: " + err.Error())
				}
			}
		})
	})
}

func addNewRecord(type_ int, id int, value int) {
	addBatchUpdateDelta(type_, id, int64(value))
}

func addBatchUpdateDelta(type_ int, id int, value int64) {
	if type_ < 0 || type_ >= BatchUpdateTypeCount {
		common.SysError(fmt.Sprintf("invalid batch update type: %d", type_))
		return
	}
	batchUpdateLocks[type_].Lock()
	defer batchUpdateLocks[type_].Unlock()
	current := batchUpdateStores[type_][id]
	if value > 0 && current > math.MaxInt64-value {
		common.SysError(fmt.Sprintf("batch update delta overflow was saturated: type=%d id=%d current=%d delta=%d", type_, id, current, value))
		batchUpdateStores[type_][id] = math.MaxInt64
		return
	}
	if value < 0 && current < math.MinInt64-value {
		common.SysError(fmt.Sprintf("batch update delta underflow was saturated: type=%d id=%d current=%d delta=%d", type_, id, current, value))
		batchUpdateStores[type_][id] = math.MinInt64
		return
	}
	batchUpdateStores[type_][id] = current + value
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
	stores := make([]map[int]int64, BatchUpdateTypeCount)
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		stores[i] = batchUpdateStores[i]
		batchUpdateStores[i] = make(map[int]int64)
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
				err = applyBatchedTokenQuotaDelta(key, value)
			case BatchUpdateTypeChannelUsedQuota:
				err = applyChannelUsedQuotaDelta(key, value)
			}
			if err != nil {
				addBatchUpdateDelta(i, key, value)
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
		err := applyUserMetricDeltas(key, quota, usedQuota, requestCount)
		if err != nil {
			addBatchUpdateDelta(BatchUpdateTypeUserQuota, key, quota)
			addBatchUpdateDelta(BatchUpdateTypeUsedQuota, key, usedQuota)
			addBatchUpdateDelta(BatchUpdateTypeRequestCount, key, requestCount)
			flushErrors = append(flushErrors, fmt.Errorf("batch user id %d: %w", key, err))
		}
	}
	common.SysLog("batch update finished")
	return errors.Join(flushErrors...)
}

func applyBatchedTokenQuotaDelta(id int, delta int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var token Token
		result := lockForUpdate(tx).Where("id = ?", id).Limit(1).Find(&token)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		remainQuota, err := checkedQuotaBalanceDelta64(token.RemainQuota, delta)
		if err != nil {
			return err
		}
		if delta == math.MinInt64 {
			return errors.New("batched token quota delta underflow")
		}
		usedQuota, err := checkedQuotaBalanceDelta64(token.UsedQuota, -delta)
		if err != nil {
			return err
		}
		return tx.Model(&token).Updates(map[string]interface{}{
			"remain_quota":  remainQuota,
			"used_quota":    usedQuota,
			"accessed_time": common.GetTimestamp(),
		}).Error
	})
}

func applyChannelUsedQuotaDelta(id int, delta int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		result := lockForUpdate(tx).Where("id = ?", id).Limit(1).Find(&channel)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		usedQuota := saturatingInt64Aggregate(channel.UsedQuota, delta, "batched channel used quota")
		return tx.Model(&channel).Update("used_quota", usedQuota).Error
	})
}

func applyUserMetricDeltas(id int, quotaDelta int64, usedQuotaDelta int64, requestCountDelta int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		var user User
		result := lockForUpdate(tx).Where("id = ?", id).Limit(1).Find(&user)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}
		quota, err := checkedQuotaBalanceDelta64(user.Quota, quotaDelta)
		if err != nil {
			return err
		}
		usedQuota := saturatingQuotaAggregate64(user.UsedQuota, usedQuotaDelta, "batched user used quota")
		requestCount := saturatingQuotaAggregate64(user.RequestCount, requestCountDelta, "batched user request count")
		return tx.Model(&user).Updates(map[string]interface{}{
			"quota":         quota,
			"used_quota":    usedQuota,
			"request_count": requestCount,
		}).Error
	})
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
	TokenKeyHash          string `json:"token_key_hash,omitempty"`
	FundingDelta          int    `json:"funding_delta"`
	TokenDelta            int    `json:"token_delta"`
	ExtraReserved         int64  `json:"extra_reserved,omitempty"`
	// ReversalOfTaskID makes this adjustment the exact inverse of a completed
	// settlement. The original persisted result is used so subscription charges
	// that overflowed into the wallet are reversed atomically and accurately.
	ReversalOfTaskID string `json:"reversal_of_task_id,omitempty"`
}

// BillingAdjustmentResult records how a durable adjustment was distributed.
// A subscription settlement can cross the plan boundary: plans that allow
// wallet overflow consume the remaining subscription quota first and charge
// the rest to the wallet, while strict plans retain the overage on the
// subscription so a delivered response is accounted for without touching the
// wallet.
type BillingAdjustmentResult struct {
	SubscriptionDelta        int   `json:"subscription_delta,omitempty"`
	SubscriptionPeriodStart  int64 `json:"subscription_period_start,omitempty"`
	SubscriptionResetVersion int64 `json:"subscription_reset_version,omitempty"`
	WalletDelta              int   `json:"wallet_delta,omitempty"`
	TokenDelta               int   `json:"token_delta,omitempty"`
	// AlreadyProcessed is transient caller metadata. It is true when the
	// durable task had already succeeded before this processing attempt, which
	// lets callers avoid duplicating consume logs and aggregate counters.
	AlreadyProcessed bool `json:"-"`
}

func billingAdjustmentTaskID(requestID string, kind string) string {
	digest := sha256.Sum256([]byte(requestID + "\x00" + kind))
	return fmt.Sprintf("billing_%x", digest[:24])
}

// BillingAdjustmentTaskID returns the deterministic task ID for a durable
// adjustment. Callers use it to reference a charge that a later reversal must
// wait for and reverse exactly.
func BillingAdjustmentTaskID(requestID string, kind string) string {
	return billingAdjustmentTaskID(requestID, kind)
}

// BillingAdjustmentReversalTaskID returns the one canonical reversal task ID
// for a completed settlement. Every caller reversing the same settlement must
// converge on this ID regardless of its local failure reason.
func BillingAdjustmentReversalTaskID(settlementTaskID string) string {
	return billingAdjustmentTaskID(settlementTaskID, "reversal")
}

// BillingTokenKeyHash is a non-secret immutable identity for a token row.
// Durable work stores this instead of the API key so a delayed adjustment
// cannot mutate a deleted token's same-user replacement if its numeric primary
// key is reused. Empty supports playground and legacy records.
func BillingTokenKeyHash(tokenKey string) string {
	if tokenKey == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(tokenKey))
	return fmt.Sprintf("%x", digest[:])
}

func validateBillingAdjustment(adjustment BillingAdjustment) error {
	if adjustment.RequestID == "" {
		return errors.New("billing adjustment request id is empty")
	}
	if adjustment.Kind != BillingAdjustmentSettle && adjustment.Kind != BillingAdjustmentRefund {
		return fmt.Errorf("invalid billing adjustment kind: %s", adjustment.Kind)
	}
	if adjustment.FundingSource != BillingAdjustmentWallet && adjustment.FundingSource != BillingAdjustmentSubscription {
		return fmt.Errorf("invalid billing adjustment funding source: %s", adjustment.FundingSource)
	}
	if adjustment.UserID <= 0 {
		return errors.New("billing adjustment user id is invalid")
	}
	if adjustment.FundingSource == BillingAdjustmentSubscription && adjustment.SubscriptionID <= 0 {
		return errors.New("billing adjustment subscription id is invalid")
	}
	if err := validateSubscriptionAdjustmentProof(adjustment); err != nil {
		return err
	}
	if adjustment.FundingDelta > common.MaxQuota || adjustment.FundingDelta < -common.MaxQuota ||
		adjustment.TokenDelta > common.MaxQuota || adjustment.TokenDelta < -common.MaxQuota {
		return errors.New("billing adjustment delta exceeds quota storage range")
	}
	if adjustment.TokenDelta != 0 {
		if adjustment.TokenID <= 0 {
			return errors.New("billing adjustment token delta requires a token id")
		}
		if adjustment.TokenDelta != adjustment.FundingDelta {
			return errors.New("billing adjustment token and funding deltas are inconsistent")
		}
	}
	if adjustment.TokenKeyHash != "" {
		if adjustment.TokenID <= 0 || len(adjustment.TokenKeyHash) != sha256.Size*2 {
			return errors.New("billing adjustment token key hash is invalid")
		}
		for _, char := range adjustment.TokenKeyHash {
			if !((char >= '0' && char <= '9') || (char >= 'a' && char <= 'f')) {
				return errors.New("billing adjustment token key hash is invalid")
			}
		}
	}
	if adjustment.ExtraReserved > int64(common.MaxQuota) {
		return errors.New("billing adjustment extra reservation exceeds quota storage range")
	}
	if adjustment.ExtraReserved != 0 &&
		(adjustment.Kind != BillingAdjustmentRefund ||
			adjustment.FundingSource != BillingAdjustmentSubscription ||
			adjustment.SubscriptionRequestID == "") {
		return errors.New("billing adjustment extra reservation has no subscription refund proof")
	}
	if adjustment.Kind == BillingAdjustmentRefund && (adjustment.FundingDelta > 0 || adjustment.TokenDelta > 0 || adjustment.ExtraReserved < 0) {
		return errors.New("billing refund contains an invalid delta")
	}
	if adjustment.ReversalOfTaskID != "" {
		if adjustment.Kind != BillingAdjustmentRefund {
			return errors.New("billing reversal must use refund kind")
		}
		if adjustment.FundingDelta != 0 || adjustment.TokenDelta != 0 || adjustment.ExtraReserved != 0 || adjustment.SubscriptionRequestID != "" {
			return errors.New("billing reversal cannot contain explicit deltas")
		}
	}
	return nil
}

// EnqueueBillingAdjustment persists an adjustment before any balance changes
// are attempted. Reusing the same request/kind pair is safe and returns the
// original task, while a payload mismatch is rejected.
func EnqueueBillingAdjustment(adjustment BillingAdjustment) (string, error) {
	if err := validateBillingAdjustment(adjustment); err != nil {
		return "", err
	}
	payload, err := common.Marshal(adjustment)
	if err != nil {
		return "", err
	}
	taskID := billingAdjustmentTaskID(adjustment.RequestID, adjustment.Kind)
	if adjustment.ReversalOfTaskID == taskID {
		return "", errors.New("billing adjustment cannot reverse itself")
	}
	if adjustment.ReversalOfTaskID != "" {
		// The original settlement is the idempotency boundary for a reversal.
		// Different callers or reasons must not be able to mint multiple refund
		// tasks for the same completed charge.
		taskID = BillingAdjustmentReversalTaskID(adjustment.ReversalOfTaskID)
		if adjustment.ReversalOfTaskID == taskID {
			return "", errors.New("billing adjustment cannot reverse itself")
		}
	}
	task := &SystemTask{
		TaskID:  taskID,
		Type:    SystemTaskTypeBillingAdjustment,
		Status:  SystemTaskStatusPending,
		Payload: string(payload),
	}
	err = DB.Transaction(func(tx *gorm.DB) error {
		// Persist before taking funding locks. A processor locks the task before
		// user/subscription rows, while INSERT ... ON CONFLICT may wait on that
		// same task row. Reversing those operations avoids an otherwise possible
		// funding -> task / task -> funding deadlock. The transaction cannot
		// commit unless the live funding owner is locked below, so account and
		// subscription deletion remain serialized with new outbox work.
		if err := persistBillingAdjustmentTask(tx, task); err != nil {
			return err
		}
		// A settled subscription proof can be selected for cleanup while a new
		// async adjustment is being enqueued. Lock the live proof before funding
		// rows so cleanup cannot delete the reset-period evidence between this
		// enqueue and processing. Missing proofs remain allowed for legacy late
		// refunds, whose processor restores only components it can prove safely.
		if adjustment.SubscriptionRequestID != "" {
			var proof SubscriptionPreConsumeRecord
			result := lockForUpdate(tx).
				Where("request_id = ?", adjustment.SubscriptionRequestID).
				Limit(1).
				Find(&proof)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 0 &&
				(proof.UserId != adjustment.UserID ||
					proof.UserSubscriptionId != adjustment.SubscriptionID) {
				return errors.New("billing adjustment subscription proof does not match adjustment")
			}
		}
		// User -> subscription is the project-wide funding lock order. Provider
		// lifecycle and administrative cancellation already need the user row
		// before an entitlement; taking subscription first here could deadlock a
		// post-response settlement with either path.
		if err := lockBillingUserTx(tx, adjustment.UserID); err != nil {
			return err
		}
		if adjustment.FundingSource == BillingAdjustmentSubscription {
			if _, err := lockBillingSubscriptionTx(tx, adjustment.SubscriptionID, adjustment.UserID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return taskID, nil
}

func persistBillingAdjustmentTask(tx *gorm.DB, task *SystemTask) error {
	if tx == nil || task == nil {
		return errors.New("billing adjustment task is nil")
	}

	// Read an existing task without taking a row lock. The create-on-conflict
	// path below serializes concurrent first inserts, while callers establish
	// the canonical task -> user -> subscription order before committing.
	var persisted SystemTask
	result := tx.Where("task_id = ?", task.TaskID).Limit(1).Find(&persisted)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 0 {
		if persisted.Type != SystemTaskTypeBillingAdjustment {
			return errors.New("billing adjustment idempotency key reused by another task type")
		}
		if persisted.Payload != task.Payload {
			return errors.New("billing adjustment idempotency key reused with different payload")
		}
		return nil
	}

	if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(task).Error; err != nil {
		return err
	}
	if err := tx.Where("task_id = ?", task.TaskID).First(&persisted).Error; err != nil {
		return err
	}
	if persisted.Type != SystemTaskTypeBillingAdjustment {
		return errors.New("billing adjustment idempotency key reused by another task type")
	}
	if persisted.Payload != task.Payload {
		return errors.New("billing adjustment idempotency key reused with different payload")
	}
	return nil
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

func lockBillingUserTx(tx *gorm.DB, userID int) error {
	if tx == nil {
		return errors.New("billing transaction is nil")
	}
	if userID <= 0 {
		return errors.New("billing user id is invalid")
	}
	var user User
	return lockForUpdate(tx).
		Select("id").
		Where("id = ?", userID).
		First(&user).Error
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

func validateSubscriptionAdjustmentProof(adjustment BillingAdjustment) error {
	if adjustment.FundingSource == BillingAdjustmentSubscription && adjustment.ReversalOfTaskID == "" &&
		(adjustment.FundingDelta < 0 || adjustment.TokenDelta < 0) && adjustment.SubscriptionRequestID == "" {
		return errors.New("negative subscription adjustment requires reservation proof")
	}
	return nil
}

func checkedQuotaBalanceDelta(current int, delta int) (int, error) {
	return checkedQuotaBalanceDelta64(current, int64(delta))
}

func checkedQuotaBalanceDelta64(current int, delta int64) (int, error) {
	if delta > 0 && int64(current) > math.MaxInt64-delta {
		return 0, fmt.Errorf("quota balance overflow: current=%d delta=%d", current, delta)
	}
	if delta < 0 && int64(current) < math.MinInt64-delta {
		return 0, fmt.Errorf("quota balance overflow: current=%d delta=%d", current, delta)
	}
	value := int64(current) + delta
	if value > int64(common.MaxQuota) || value < int64(common.MinQuota) {
		return 0, fmt.Errorf("quota balance overflow: current=%d delta=%d", current, delta)
	}
	return int(value), nil
}

// saturatingQuotaAggregate keeps non-financial lifetime counters from wedging
// a durable billing outbox after the balance adjustment has already committed.
// Financial balances continue to use checkedQuotaBalanceDelta and fail closed.
func saturatingQuotaAggregate(current int, delta int, label string) int {
	return saturatingQuotaAggregate64(current, int64(delta), label)
}

func saturatingQuotaAggregate64(current int, delta int64, label string) int {
	if current < 0 {
		common.SysError(fmt.Sprintf("%s was negative and was repaired during billing finalization: %d", label, current))
		current = 0
	}
	if delta < 0 && (delta == math.MinInt64 || int64(current) < -delta) {
		common.SysError(fmt.Sprintf("%s aggregate underflow was saturated: current=%d delta=%d", label, current, delta))
		return 0
	}
	if delta > int64(common.MaxQuota)-int64(current) {
		common.SysError(fmt.Sprintf("%s aggregate overflow was saturated: current=%d delta=%d", label, current, delta))
		return common.MaxQuota
	}
	return current + int(delta)
}

func saturatingInt64Aggregate(current int64, delta int64, label string) int64 {
	if current < 0 {
		common.SysError(fmt.Sprintf("%s was negative and was repaired during billing finalization: %d", label, current))
		current = 0
	}
	if delta > 0 && current > math.MaxInt64-delta {
		common.SysError(fmt.Sprintf("%s aggregate overflow was saturated: current=%d delta=%d", label, current, delta))
		return math.MaxInt64
	}
	if delta < 0 && (delta == math.MinInt64 || current < -delta) {
		common.SysError(fmt.Sprintf("%s aggregate underflow was saturated: current=%d delta=%d", label, current, delta))
		return 0
	}
	return current + delta
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

func applyTokenBillingDeltaTx(tx *gorm.DB, userID int, tokenID int, tokenKeyHash string, delta int) (int, error) {
	if delta == 0 {
		return 0, nil
	}
	var token Token
	if err := lockForUpdate(tx.Unscoped()).Where("id = ?", tokenID).First(&token).Error; err != nil {
		// A token can be deleted after the provider response was delivered. Its
		// absence must not prevent reconciling the user's durable funding source.
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	if token.UserId != userID {
		// SQLite may reuse an explicitly deleted highest integer primary key,
		// and restored/imported data can reuse identifiers on every dialect.
		// A delayed settlement must never mutate a replacement token owned by a
		// different account. Treat the original token as deleted so durable
		// funding reconciliation can still complete.
		common.SysError(fmt.Sprintf(
			"billing token owner changed before adjustment; token=%d expected_user=%d actual_user=%d",
			tokenID,
			userID,
			token.UserId,
		))
		return 0, nil
	}
	if tokenKeyHash != "" && BillingTokenKeyHash(token.Key) != tokenKeyHash {
		common.SysError(fmt.Sprintf(
			"billing token identity changed before adjustment; token=%d user=%d",
			tokenID,
			userID,
		))
		return 0, nil
	}
	remainQuota, err := checkedQuotaBalanceDelta(token.RemainQuota, -delta)
	if err != nil {
		return 0, err
	}
	usedQuota, err := checkedQuotaBalanceDelta(token.UsedQuota, delta)
	if err != nil {
		return 0, err
	}
	if err := tx.Model(&token).Updates(map[string]interface{}{
		"remain_quota":  remainQuota,
		"used_quota":    usedQuota,
		"accessed_time": common.GetTimestamp(),
	}).Error; err != nil {
		return 0, err
	}
	return delta, nil
}

func applyBillingAdjustmentTx(tx *gorm.DB, adjustment BillingAdjustment) (BillingAdjustmentResult, error) {
	result := BillingAdjustmentResult{}
	if err := validateSubscriptionAdjustmentProof(adjustment); err != nil {
		return result, err
	}
	var settlementRecord *SubscriptionPreConsumeRecord
	if adjustment.FundingSource == BillingAdjustmentSubscription &&
		adjustment.Kind == BillingAdjustmentSettle && adjustment.SubscriptionRequestID != "" {
		var record SubscriptionPreConsumeRecord
		err := lockForUpdate(tx).Where("request_id = ?", adjustment.SubscriptionRequestID).First(&record).Error
		if err == nil {
			if record.UserId != adjustment.UserID || record.UserSubscriptionId != adjustment.SubscriptionID {
				return BillingAdjustmentResult{}, errors.New("subscription settlement record does not match adjustment")
			}
			if record.PreConsumed < 0 || record.PreConsumed > int64(common.MaxQuota) {
				return BillingAdjustmentResult{}, errors.New("subscription pre-consume record contains invalid quota")
			}
			switch record.Status {
			case "consumed", "settled":
				settlementRecord = &record
			case "refunded":
				// A terminal task refund can win the race with the accepted task's
				// initial zero-delta settlement outbox. Let that outbox finish its
				// aggregates and log without restoring the refunded reservation.
				if adjustment.FundingDelta != 0 || adjustment.TokenDelta != 0 {
					return BillingAdjustmentResult{}, errors.New("refunded subscription reservation only permits zero-delta settlement replay")
				}
			default:
				return BillingAdjustmentResult{}, errors.New("subscription settlement record does not match adjustment")
			}
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return BillingAdjustmentResult{}, err
		}
	}
	if adjustment.FundingSource == BillingAdjustmentSubscription {
		// ProcessBillingAdjustmentWithResult has already locked any reservation
		// and subscription proof. Lock the account before an entitlement so this
		// transaction follows reservation -> proof -> user -> subscription ->
		// token, matching provider/admin entitlement lifecycle operations.
		if err := lockBillingUserTx(tx, adjustment.UserID); err != nil {
			return BillingAdjustmentResult{}, err
		}
	}
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
				// Cleanup can remove the only durable proof of which quota period
				// carried the reservation. Do not guess and risk turning a late
				// refund into a credit against new-period usage. The adjustment still
				// restores its wallet/token components below.
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
					applied, err := refundSubscriptionPreConsumeUsageTx(tx, &record, refundAmount)
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
		settlementHandled := false
		if adjustment.FundingDelta < 0 && adjustment.SubscriptionRequestID != "" {
			// A negative settlement returns part of the request's reservation. If
			// a reset already cleared that old-period reservation, only the token
			// delta below remains to be restored.
			settlementHandled = true
			if settlementRecord != nil {
				refundAmount := -int64(adjustment.FundingDelta)
				if settlementRecord.PreConsumed < refundAmount {
					return BillingAdjustmentResult{}, errors.New("subscription settlement refund exceeds reservation")
				}
				applied, err := refundSubscriptionPreConsumeUsageTx(tx, settlementRecord, refundAmount)
				if err != nil {
					return BillingAdjustmentResult{}, err
				}
				result.SubscriptionDelta = int(applied)
				settlementRecord.PreConsumed -= refundAmount
			}
		}
		if !settlementHandled {
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
			result.SubscriptionPeriodStart = subscription.LastResetTime
			result.SubscriptionResetVersion = subscription.QuotaResetVersion
			result.SubscriptionDelta = int(applied)
			overflow := adjustment.FundingDelta - result.SubscriptionDelta
			if overflow > 0 {
				if err := applyWalletBillingDeltaTx(tx, adjustment.UserID, overflow); err != nil {
					return BillingAdjustmentResult{}, err
				}
				result.WalletDelta = overflow
			}
		}
	}

	if settlementRecord != nil {
		if err := tx.Model(settlementRecord).Updates(map[string]interface{}{
			"pre_consumed": settlementRecord.PreConsumed,
			"status":       "settled",
			"updated_at":   common.GetTimestamp(),
		}).Error; err != nil {
			return BillingAdjustmentResult{}, err
		}
	}

	if adjustment.TokenDelta != 0 {
		applied, err := applyTokenBillingDeltaTx(
			tx,
			adjustment.UserID,
			adjustment.TokenID,
			adjustment.TokenKeyHash,
			adjustment.TokenDelta,
		)
		if err != nil {
			return BillingAdjustmentResult{}, err
		}
		result.TokenDelta = applied
	}
	return result, nil
}

func billingAdjustmentResultFromTask(task *SystemTask, adjustment BillingAdjustment) (BillingAdjustmentResult, error) {
	if task.Result != "" {
		var result BillingAdjustmentResult
		if err := common.UnmarshalJsonStr(task.Result, &result); err != nil {
			return BillingAdjustmentResult{}, err
		}
		return result, nil
	}

	// Adjustments completed before split results were persisted remain valid
	// idempotency markers. They could only have charged one funding source.
	result := BillingAdjustmentResult{TokenDelta: adjustment.TokenDelta}
	if adjustment.FundingSource == BillingAdjustmentWallet {
		result.WalletDelta = adjustment.FundingDelta
	} else {
		result.SubscriptionDelta = adjustment.FundingDelta
	}
	return result, nil
}

func applyBillingAdjustmentReversalTx(tx *gorm.DB, reversal BillingAdjustment) (BillingAdjustmentResult, error) {
	var originalTask SystemTask
	if err := lockForUpdate(tx).
		Where("task_id = ? AND type = ?", reversal.ReversalOfTaskID, SystemTaskTypeBillingAdjustment).
		First(&originalTask).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return BillingAdjustmentResult{}, fmt.Errorf("billing reversal dependency %s is not ready", reversal.ReversalOfTaskID)
		}
		return BillingAdjustmentResult{}, err
	}
	if originalTask.Status != SystemTaskStatusSucceeded {
		return BillingAdjustmentResult{}, fmt.Errorf("billing reversal dependency %s is not ready: status %s", reversal.ReversalOfTaskID, originalTask.Status)
	}

	var original BillingAdjustment
	if err := common.UnmarshalJsonStr(originalTask.Payload, &original); err != nil {
		return BillingAdjustmentResult{}, err
	}
	if original.Kind != BillingAdjustmentSettle || original.ReversalOfTaskID != "" {
		return BillingAdjustmentResult{}, errors.New("billing reversal dependency is not a settlement")
	}
	if original.UserID != reversal.UserID || original.FundingSource != reversal.FundingSource ||
		original.SubscriptionID != reversal.SubscriptionID || original.TokenID != reversal.TokenID {
		return BillingAdjustmentResult{}, errors.New("billing reversal context does not match original settlement")
	}
	if original.FundingDelta < 0 || original.TokenDelta < 0 {
		return BillingAdjustmentResult{}, errors.New("billing reversal dependency contains a negative charge")
	}

	originalResult, err := billingAdjustmentResultFromTask(&originalTask, original)
	if err != nil {
		return BillingAdjustmentResult{}, err
	}
	if originalResult.SubscriptionDelta < 0 || originalResult.WalletDelta < 0 || originalResult.TokenDelta < 0 {
		return BillingAdjustmentResult{}, errors.New("billing reversal dependency contains an invalid result")
	}
	if int64(originalResult.SubscriptionDelta)+int64(originalResult.WalletDelta) != int64(original.FundingDelta) {
		return BillingAdjustmentResult{}, errors.New("billing reversal dependency funding split is inconsistent")
	}
	if originalResult.TokenDelta != 0 && originalResult.TokenDelta != original.TokenDelta {
		return BillingAdjustmentResult{}, errors.New("billing reversal dependency token delta is inconsistent")
	}
	if original.FundingSource == BillingAdjustmentWallet && originalResult.SubscriptionDelta != 0 {
		return BillingAdjustmentResult{}, errors.New("wallet settlement has an invalid subscription split")
	}

	result := BillingAdjustmentResult{}
	if originalResult.SubscriptionDelta != 0 {
		// A split subscription settlement can also have a wallet component.
		// Lock user before subscription to avoid the inverse of provider and
		// administrative entitlement lifecycle transactions.
		if err := lockBillingUserTx(tx, original.UserID); err != nil {
			return BillingAdjustmentResult{}, err
		}
		subscription, err := lockBillingSubscriptionTx(tx, original.SubscriptionID, original.UserID)
		if err != nil {
			return BillingAdjustmentResult{}, err
		}
		if subscription.QuotaResetVersion < originalResult.SubscriptionResetVersion {
			return BillingAdjustmentResult{}, errors.New("subscription quota reset version moved backwards")
		}
		periodAdvanced := subscription.QuotaResetVersion > originalResult.SubscriptionResetVersion
		if originalResult.SubscriptionPeriodStart != 0 {
			periodAdvanced = periodAdvanced || subscription.LastResetTime > originalResult.SubscriptionPeriodStart
		} else {
			// Results written before period metadata was introduced can still be
			// classified safely by comparing the reset boundary with the original
			// task creation time.
			periodAdvanced = periodAdvanced || subscription.LastResetTime > originalTask.CreatedAt
		}
		if !periodAdvanced {
			applied, err := updateSubscriptionUsedTx(tx, subscription, -int64(originalResult.SubscriptionDelta), false)
			if err != nil {
				return BillingAdjustmentResult{}, err
			}
			if applied != -int64(originalResult.SubscriptionDelta) {
				return BillingAdjustmentResult{}, errors.New("billing reversal exceeds subscription usage")
			}
			result.SubscriptionDelta = int(applied)
		}
	}
	if originalResult.WalletDelta != 0 {
		if err := applyWalletBillingDeltaTx(tx, original.UserID, -originalResult.WalletDelta); err != nil {
			return BillingAdjustmentResult{}, err
		}
		result.WalletDelta = -originalResult.WalletDelta
	}
	if originalResult.TokenDelta != 0 {
		applied, err := applyTokenBillingDeltaTx(
			tx,
			original.UserID,
			original.TokenID,
			original.TokenKeyHash,
			-originalResult.TokenDelta,
		)
		if err != nil {
			return BillingAdjustmentResult{}, err
		}
		result.TokenDelta = applied
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
		if err := common.UnmarshalJsonStr(task.Payload, &adjustment); err != nil {
			return err
		}
		if err := validateBillingAdjustment(adjustment); err != nil {
			return err
		}
		if adjustment.ReversalOfTaskID == "" {
			// Extensions lock their reservation before the subscription
			// pre-consume proof. Lock every reservation that this adjustment may
			// touch in the same order before applyBillingAdjustmentTx takes the
			// proof lock, preventing reservation -> proof / proof -> reservation
			// deadlocks between a final reserve and settlement.
			requestIDs := []string{adjustment.RequestID}
			if adjustment.SubscriptionRequestID != "" && adjustment.SubscriptionRequestID != adjustment.RequestID {
				requestIDs = append(requestIDs, adjustment.SubscriptionRequestID)
			}
			var reservations []BillingReservation
			if err := lockForUpdate(tx).
				Where("request_id IN ?", requestIDs).
				Order("request_id asc").
				Find(&reservations).Error; err != nil {
				return err
			}
		}
		if task.Status == SystemTaskStatusSucceeded {
			result, err := billingAdjustmentResultFromTask(&task, adjustment)
			if err != nil {
				return err
			}
			if adjustment.ReversalOfTaskID == "" {
				if err := markBillingAdjustmentReservationTerminalTx(tx, adjustment); err != nil {
					return err
				}
			}
			appliedResult = result
			appliedResult.AlreadyProcessed = true
			return nil
		}
		if task.Status != SystemTaskStatusPending {
			return fmt.Errorf("billing adjustment %s has invalid status %s", taskID, task.Status)
		}
		var result BillingAdjustmentResult
		var err error
		if adjustment.ReversalOfTaskID != "" {
			result, err = applyBillingAdjustmentReversalTx(tx, adjustment)
		} else {
			result, err = applyBillingAdjustmentTx(tx, adjustment)
		}
		if err != nil {
			return err
		}
		if adjustment.ReversalOfTaskID == "" {
			if err := markBillingAdjustmentReservationTerminalTx(tx, adjustment); err != nil {
				return err
			}
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
		if recordErr := DB.Model(&SystemTask{}).
			Where("task_id = ? AND status = ?", taskID, SystemTaskStatusPending).
			Updates(map[string]interface{}{
				"error": err.Error(),
				// Move a failed task behind work created in the same database
				// second so one poison row cannot monopolize a bounded batch.
				"updated_at": gorm.Expr(mainDatabaseUnixTimestampSQL() + " + 1"),
			}).Error; recordErr != nil {
			common.SysError(fmt.Sprintf("failed to record billing adjustment failure for task %q: %v", taskID, recordErr))
		}
		return BillingAdjustmentResult{}, err
	}

	if common.RedisEnabled && adjustment.UserID > 0 {
		if err := invalidateUserCache(adjustment.UserID); err != nil {
			common.SysError(fmt.Sprintf("failed to invalidate user cache after billing adjustment for user %d: %v", adjustment.UserID, err))
		}
	}
	if common.RedisEnabled && adjustment.TokenID > 0 {
		var tokenKey string
		if err := DB.Model(&Token{}).Where("id = ?", adjustment.TokenID).Select(commonKeyCol).Find(&tokenKey).Error; err != nil {
			common.SysError(fmt.Sprintf("failed to load token cache key after billing adjustment for token %d: %v", adjustment.TokenID, err))
		} else if tokenKey != "" {
			if err := cacheDeleteToken(tokenKey); err != nil {
				common.SysError(fmt.Sprintf("failed to invalidate token cache after billing adjustment for token %d: %v", adjustment.TokenID, err))
			}
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
		Order("updated_at asc").Order("id asc").Limit(limit).Find(&tasks).Error; err != nil {
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
