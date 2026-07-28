package model

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

var (
	ErrBillingReservationInsufficientWallet       = errors.New("billing reservation wallet quota insufficient")
	ErrBillingReservationInsufficientToken        = errors.New("billing reservation token quota insufficient")
	ErrBillingReservationNoActiveSubscription     = errors.New("billing reservation has no active subscription")
	ErrBillingReservationInsufficientSubscription = errors.New("billing reservation subscription quota insufficient")
	ErrBillingReservationContextMismatch          = errors.New("billing reservation idempotency context mismatch")
	ErrBillingReservationTerminal                 = errors.New("billing reservation is already terminal")
)

const (
	billingReservationStatusPending  = "pending"
	billingReservationStatusSettled  = "settled"
	billingReservationStatusRefunded = "refunded"
)

// BillingReservation is the durable idempotency boundary for the initial
// request reservation and every cumulative extension made before an upstream
// response is delivered. Funding and token balances are updated in the same
// transaction as this row.
type BillingReservation struct {
	ID             int    `json:"id"`
	RequestID      string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	ClaimToken     string `json:"-" gorm:"type:varchar(32)"`
	UserID         int    `json:"user_id" gorm:"index"`
	TokenID        int    `json:"token_id" gorm:"index"`
	TokenKeyHash   string `json:"token_key_hash" gorm:"type:char(64)"`
	FundingSource  string `json:"funding_source" gorm:"type:varchar(16);index"`
	ModelName      string `json:"model_name" gorm:"type:varchar(255)"`
	IsPlayground   bool   `json:"is_playground"`
	RequestedQuota int    `json:"requested_quota"`
	InitialQuota   int    `json:"initial_quota"`
	TrustBypass    bool   `json:"trust_bypass"`
	ReservedQuota  int    `json:"reserved_quota"`
	TokenReserved  int    `json:"token_reserved"`
	SubscriptionID int    `json:"subscription_id" gorm:"index"`
	Status         string `json:"status" gorm:"type:varchar(16);index"`

	SubscriptionAmountTotal     int64 `json:"subscription_amount_total" gorm:"type:bigint"`
	SubscriptionAmountUsedAfter int64 `json:"subscription_amount_used_after" gorm:"type:bigint"`
	CreatedAt                   int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt                   int64 `json:"updated_at" gorm:"bigint;index"`
}

func (r *BillingReservation) BeforeCreate(_ *gorm.DB) error {
	now := common.GetTimestamp()
	if r.Status == "" {
		r.Status = billingReservationStatusPending
	}
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

// BillingReservationRequest contains every immutable piece of billing context
// needed to distinguish a legitimate retry from request-ID reuse.
type BillingReservationRequest struct {
	RequestID      string
	UserID         int
	TokenID        int
	TokenKey       string
	FundingSource  string
	ModelName      string
	IsPlayground   bool
	RequestedQuota int
	InitialQuota   int
	TrustBypass    bool
}

type BillingReservationExtension struct {
	RequestID      string
	UserID         int
	TokenID        int
	TokenKey       string
	FundingSource  string
	ModelName      string
	IsPlayground   bool
	SubscriptionID int
	TargetQuota    int
}

// BillingReservationResult returns persisted totals. Applied deltas are zero
// for an idempotent retry, while the canonical totals always describe the
// original committed reservation.
type BillingReservationResult struct {
	FundingSource                string
	UserID                       int
	TokenID                      int
	TokenKeyHash                 string
	SubscriptionID               int
	ReservedQuota                int
	TokenReserved                int
	SubscriptionAmountTotal      int64
	SubscriptionAmountUsedAfter  int64
	AppliedFundingDelta          int
	AppliedTokenDelta            int
	AlreadyReserved              bool
	TrustBypass                  bool
	tokenKeyForCacheInvalidation string
}

func validateBillingReservationRequest(request BillingReservationRequest) error {
	if strings.TrimSpace(request.RequestID) == "" {
		return errors.New("billing reservation request id is empty")
	}
	if len(request.RequestID) > 64 {
		return errors.New("billing reservation request id exceeds 64 bytes")
	}
	if request.UserID <= 0 {
		return errors.New("billing reservation user id is invalid")
	}
	if request.TokenID <= 0 && !request.IsPlayground {
		return errors.New("billing reservation token id is invalid")
	}
	if request.FundingSource != BillingAdjustmentWallet && request.FundingSource != BillingAdjustmentSubscription {
		return fmt.Errorf("invalid billing reservation funding source: %s", request.FundingSource)
	}
	if len(request.ModelName) > 255 {
		return errors.New("billing reservation model name exceeds 255 bytes")
	}
	if request.RequestedQuota < 0 || request.RequestedQuota > common.MaxQuota {
		return fmt.Errorf("billing reservation requested quota is out of range: %d", request.RequestedQuota)
	}
	if request.InitialQuota < 0 || request.InitialQuota > common.MaxQuota {
		return fmt.Errorf("billing reservation quota is out of range: %d", request.InitialQuota)
	}
	if request.TrustBypass {
		if request.FundingSource != BillingAdjustmentWallet || request.InitialQuota != 0 {
			return errors.New("billing reservation has an invalid trust bypass")
		}
	} else if request.InitialQuota != request.RequestedQuota {
		return errors.New("billing reservation requested and applied quotas differ")
	}
	if request.FundingSource == BillingAdjustmentSubscription && request.InitialQuota == 0 {
		return errors.New("subscription billing reservation quota must be positive")
	}
	return nil
}

func validateBillingReservationExtension(extension BillingReservationExtension) error {
	return validateBillingReservationRequest(BillingReservationRequest{
		RequestID:      extension.RequestID,
		UserID:         extension.UserID,
		TokenID:        extension.TokenID,
		TokenKey:       extension.TokenKey,
		FundingSource:  extension.FundingSource,
		ModelName:      extension.ModelName,
		IsPlayground:   extension.IsPlayground,
		RequestedQuota: extension.TargetQuota,
		InitialQuota:   extension.TargetQuota,
	})
}

func validateBillingReservationContext(record *BillingReservation, request BillingReservationRequest) error {
	if record == nil {
		return errors.New("billing reservation is nil")
	}
	if record.RequestID != request.RequestID || record.UserID != request.UserID ||
		record.TokenID != request.TokenID || record.FundingSource != request.FundingSource ||
		record.ModelName != request.ModelName || record.IsPlayground != request.IsPlayground ||
		record.RequestedQuota != request.RequestedQuota || record.InitialQuota != request.InitialQuota ||
		record.TrustBypass != request.TrustBypass {
		return ErrBillingReservationContextMismatch
	}
	expectedTokenKeyHash := ""
	if request.TokenID > 0 && !request.IsPlayground {
		expectedTokenKeyHash = BillingTokenKeyHash(request.TokenKey)
	}
	if record.TokenKeyHash != "" && record.TokenKeyHash != expectedTokenKeyHash {
		return ErrBillingReservationContextMismatch
	}
	if record.RequestedQuota < 0 || record.RequestedQuota > common.MaxQuota ||
		record.InitialQuota < 0 || record.InitialQuota > common.MaxQuota ||
		record.ReservedQuota < 0 || record.ReservedQuota > common.MaxQuota ||
		record.TokenReserved < 0 || record.TokenReserved > common.MaxQuota {
		return errors.New("billing reservation contains an invalid quota")
	}
	if record.TrustBypass {
		if record.FundingSource != BillingAdjustmentWallet || record.InitialQuota != 0 ||
			record.ReservedQuota != 0 || record.TokenReserved != 0 {
			return errors.New("billing reservation contains an invalid trust bypass")
		}
	} else if record.InitialQuota != record.RequestedQuota {
		return errors.New("billing reservation contains mismatched initial quotas")
	}
	if record.IsPlayground {
		if record.TokenReserved != 0 {
			return errors.New("playground billing reservation contains a token charge")
		}
	} else if record.TokenReserved != record.ReservedQuota {
		return errors.New("billing reservation funding and token totals differ")
	}
	if record.FundingSource == BillingAdjustmentSubscription && record.ReservedQuota > 0 && record.SubscriptionID <= 0 {
		return errors.New("billing reservation contains an invalid subscription")
	}
	switch record.Status {
	case "", billingReservationStatusPending:
	case billingReservationStatusSettled, billingReservationStatusRefunded:
		return ErrBillingReservationTerminal
	default:
		return errors.New("billing reservation contains an invalid lifecycle status")
	}
	return nil
}

func billingReservationResult(record *BillingReservation, alreadyReserved bool) BillingReservationResult {
	return BillingReservationResult{
		FundingSource:               record.FundingSource,
		UserID:                      record.UserID,
		TokenID:                     record.TokenID,
		TokenKeyHash:                record.TokenKeyHash,
		SubscriptionID:              record.SubscriptionID,
		ReservedQuota:               record.ReservedQuota,
		TokenReserved:               record.TokenReserved,
		SubscriptionAmountTotal:     record.SubscriptionAmountTotal,
		SubscriptionAmountUsedAfter: record.SubscriptionAmountUsedAfter,
		AlreadyReserved:             alreadyReserved,
		TrustBypass:                 record.TrustBypass,
	}
}

// canFundLegacyTrustReservation identifies the only supported migration from
// an older zero-funded trust-bypass record. New requests always reserve the
// full estimate, but an idempotent retry may encounter a record created before
// the bypass was retired. Funding it in place preserves the request ID while
// closing the response-before-settlement crash window.
func canFundLegacyTrustReservation(record *BillingReservation, request BillingReservationRequest) bool {
	return record != nil &&
		record.TrustBypass &&
		!request.TrustBypass &&
		record.FundingSource == BillingAdjustmentWallet &&
		request.FundingSource == BillingAdjustmentWallet &&
		record.RequestID == request.RequestID &&
		record.UserID == request.UserID &&
		record.TokenID == request.TokenID &&
		record.ModelName == request.ModelName &&
		record.IsPlayground == request.IsPlayground &&
		record.RequestedQuota == request.RequestedQuota &&
		record.InitialQuota == 0 &&
		request.InitialQuota == request.RequestedQuota &&
		record.ReservedQuota == 0 &&
		record.TokenReserved == 0 &&
		record.SubscriptionID == 0 &&
		(record.Status == "" || record.Status == billingReservationStatusPending)
}

// FindBillingReservationByRequestID returns nil when the request has no
// durable reservation yet.
func FindBillingReservationByRequestID(requestID string) (*BillingReservation, error) {
	if strings.TrimSpace(requestID) == "" {
		return nil, errors.New("billing reservation request id is empty")
	}
	var reservation BillingReservation
	err := DB.Where("request_id = ?", requestID).First(&reservation).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &reservation, nil
}

// CreateBillingReservation atomically creates (or reads) the request's
// canonical reservation and applies both its funding and token deductions.
func CreateBillingReservation(request BillingReservationRequest) (*BillingReservationResult, error) {
	if err := validateBillingReservationRequest(request); err != nil {
		return nil, err
	}

	tokenKeyHash := ""
	if request.TokenID > 0 && !request.IsPlayground {
		tokenKeyHash = BillingTokenKeyHash(request.TokenKey)
	}
	candidate := BillingReservation{
		RequestID:      request.RequestID,
		ClaimToken:     common.GetUUID(),
		UserID:         request.UserID,
		TokenID:        request.TokenID,
		TokenKeyHash:   tokenKeyHash,
		FundingSource:  request.FundingSource,
		ModelName:      request.ModelName,
		IsPlayground:   request.IsPlayground,
		RequestedQuota: request.RequestedQuota,
		InitialQuota:   request.InitialQuota,
		TrustBypass:    request.TrustBypass,
	}
	var result BillingReservationResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "request_id"}},
			DoNothing: true,
		}).Create(&candidate).Error; err != nil {
			return err
		}

		var persisted BillingReservation
		if err := lockForUpdate(tx).Where("request_id = ?", request.RequestID).First(&persisted).Error; err != nil {
			return err
		}
		if persisted.ClaimToken != candidate.ClaimToken {
			if canFundLegacyTrustReservation(&persisted, request) {
				if err := reserveWalletQuotaTx(tx, request.UserID, request.InitialQuota); err != nil {
					return err
				}
				tokenDelta, tokenKey, err := reserveTokenQuotaTx(
					tx,
					request.UserID,
					request.TokenID,
					request.TokenKey,
					request.IsPlayground,
					request.InitialQuota,
				)
				if err != nil {
					return err
				}
				persisted.InitialQuota = request.InitialQuota
				persisted.TrustBypass = false
				persisted.ReservedQuota = request.InitialQuota
				persisted.TokenReserved = tokenDelta
				persisted.TokenKeyHash = BillingTokenKeyHash(tokenKey)
				persisted.Status = billingReservationStatusPending
				persisted.UpdatedAt = common.GetTimestamp()
				if err := tx.Model(&persisted).Updates(map[string]interface{}{
					"initial_quota":  persisted.InitialQuota,
					"trust_bypass":   false,
					"reserved_quota": persisted.ReservedQuota,
					"token_reserved": persisted.TokenReserved,
					"token_key_hash": persisted.TokenKeyHash,
					"status":         persisted.Status,
					"updated_at":     persisted.UpdatedAt,
				}).Error; err != nil {
					return err
				}
				result = billingReservationResult(&persisted, false)
				result.AppliedFundingDelta = request.InitialQuota
				result.AppliedTokenDelta = tokenDelta
				result.tokenKeyForCacheInvalidation = tokenKey
				return nil
			}
			if err := validateBillingReservationContext(&persisted, request); err != nil {
				return err
			}
			result = billingReservationResult(&persisted, true)
			result.tokenKeyForCacheInvalidation = request.TokenKey
			return nil
		}

		fundingDelta := request.InitialQuota
		switch request.FundingSource {
		case BillingAdjustmentWallet:
			if err := reserveWalletQuotaTx(tx, request.UserID, fundingDelta); err != nil {
				return err
			}
		case BillingAdjustmentSubscription:
			subscription, err := reserveInitialSubscriptionQuotaTx(tx, request, fundingDelta)
			if err != nil {
				return err
			}
			persisted.SubscriptionID = subscription.Id
			persisted.SubscriptionAmountTotal = subscription.AmountTotal
			persisted.SubscriptionAmountUsedAfter = subscription.AmountUsed
		}

		tokenDelta, tokenKey, err := reserveTokenQuotaTx(tx, request.UserID, request.TokenID, request.TokenKey, request.IsPlayground, fundingDelta)
		if err != nil {
			return err
		}
		persisted.ReservedQuota = fundingDelta
		persisted.TokenReserved = tokenDelta
		if tokenKey != "" {
			persisted.TokenKeyHash = BillingTokenKeyHash(tokenKey)
		}
		persisted.UpdatedAt = common.GetTimestamp()
		if err := tx.Model(&persisted).Updates(map[string]interface{}{
			"reserved_quota":                 persisted.ReservedQuota,
			"token_reserved":                 persisted.TokenReserved,
			"token_key_hash":                 persisted.TokenKeyHash,
			"subscription_id":                persisted.SubscriptionID,
			"subscription_amount_total":      persisted.SubscriptionAmountTotal,
			"subscription_amount_used_after": persisted.SubscriptionAmountUsedAfter,
			"updated_at":                     persisted.UpdatedAt,
		}).Error; err != nil {
			return err
		}

		result = billingReservationResult(&persisted, false)
		result.AppliedFundingDelta = fundingDelta
		result.AppliedTokenDelta = tokenDelta
		result.tokenKeyForCacheInvalidation = tokenKey
		return nil
	})
	if err != nil {
		return nil, err
	}
	invalidateBillingReservationCaches(&result)
	return &result, nil
}

// ExtendBillingReservation moves a request's reservation to a cumulative
// target. Repeating the same or a smaller target returns the persisted total
// without applying another balance change.
func ExtendBillingReservation(extension BillingReservationExtension) (*BillingReservationResult, error) {
	if err := validateBillingReservationExtension(extension); err != nil {
		return nil, err
	}
	var result BillingReservationResult
	err := DB.Transaction(func(tx *gorm.DB) error {
		var reservation BillingReservation
		if err := lockForUpdate(tx).Where("request_id = ?", extension.RequestID).First(&reservation).Error; err != nil {
			return err
		}
		request := BillingReservationRequest{
			RequestID:      extension.RequestID,
			UserID:         extension.UserID,
			TokenID:        extension.TokenID,
			TokenKey:       extension.TokenKey,
			FundingSource:  extension.FundingSource,
			ModelName:      extension.ModelName,
			IsPlayground:   extension.IsPlayground,
			RequestedQuota: reservation.RequestedQuota,
			InitialQuota:   reservation.InitialQuota,
			TrustBypass:    reservation.TrustBypass,
		}
		if err := validateBillingReservationContext(&reservation, request); err != nil {
			return err
		}
		if reservation.FundingSource == BillingAdjustmentSubscription &&
			extension.SubscriptionID != reservation.SubscriptionID {
			return ErrBillingReservationContextMismatch
		}
		if extension.TargetQuota <= reservation.ReservedQuota {
			result = billingReservationResult(&reservation, true)
			result.tokenKeyForCacheInvalidation = extension.TokenKey
			return nil
		}
		if reservation.TrustBypass {
			return ErrBillingReservationContextMismatch
		}

		delta := extension.TargetQuota - reservation.ReservedQuota
		switch reservation.FundingSource {
		case BillingAdjustmentWallet:
			if err := reserveWalletQuotaTx(tx, reservation.UserID, delta); err != nil {
				return err
			}
		case BillingAdjustmentSubscription:
			subscription, err := extendSubscriptionReservationTx(tx, &reservation, extension.TargetQuota, delta)
			if err != nil {
				return err
			}
			reservation.SubscriptionAmountTotal = subscription.AmountTotal
			reservation.SubscriptionAmountUsedAfter = subscription.AmountUsed
		}

		tokenDelta, tokenKey, err := reserveTokenQuotaTx(tx, reservation.UserID, reservation.TokenID, extension.TokenKey, reservation.IsPlayground, delta)
		if err != nil {
			return err
		}
		reservation.ReservedQuota = extension.TargetQuota
		reservation.TokenReserved += tokenDelta
		reservation.UpdatedAt = common.GetTimestamp()
		if err := tx.Model(&reservation).Updates(map[string]interface{}{
			"reserved_quota":                 reservation.ReservedQuota,
			"token_reserved":                 reservation.TokenReserved,
			"subscription_amount_total":      reservation.SubscriptionAmountTotal,
			"subscription_amount_used_after": reservation.SubscriptionAmountUsedAfter,
			"updated_at":                     reservation.UpdatedAt,
		}).Error; err != nil {
			return err
		}

		result = billingReservationResult(&reservation, false)
		result.AppliedFundingDelta = delta
		result.AppliedTokenDelta = tokenDelta
		result.tokenKeyForCacheInvalidation = tokenKey
		return nil
	})
	if err != nil {
		return nil, err
	}
	invalidateBillingReservationCaches(&result)
	return &result, nil
}

func reserveWalletQuotaTx(tx *gorm.DB, userID int, amount int) error {
	var user User
	if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("%w: user does not exist", ErrBillingReservationInsufficientWallet)
		}
		return err
	}
	if amount == 0 {
		return nil
	}
	if user.Quota < amount {
		return fmt.Errorf("%w: remaining=%d need=%d", ErrBillingReservationInsufficientWallet, user.Quota, amount)
	}
	quota, err := checkedQuotaBalanceDelta(user.Quota, -amount)
	if err != nil {
		return err
	}
	return tx.Model(&user).Update("quota", quota).Error
}

func reserveTokenQuotaTx(tx *gorm.DB, userID int, tokenID int, tokenKey string, playground bool, amount int) (int, string, error) {
	if playground || amount == 0 {
		return 0, "", nil
	}
	var token Token
	if err := lockForUpdate(tx).Where("id = ?", tokenID).First(&token).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, "", fmt.Errorf("%w: token does not exist", ErrBillingReservationInsufficientToken)
		}
		return 0, "", err
	}
	if token.UserId != userID || (tokenKey != "" && token.Key != tokenKey) {
		return 0, "", fmt.Errorf("%w: token context does not match user", ErrBillingReservationContextMismatch)
	}
	if !token.UnlimitedQuota && token.RemainQuota < amount {
		return 0, "", fmt.Errorf("%w: remaining=%d need=%d", ErrBillingReservationInsufficientToken, token.RemainQuota, amount)
	}
	remainQuota, err := checkedQuotaBalanceDelta(token.RemainQuota, -amount)
	if err != nil {
		return 0, "", err
	}
	usedQuota, err := checkedQuotaBalanceDelta(token.UsedQuota, amount)
	if err != nil {
		return 0, "", err
	}
	if err := tx.Model(&token).Updates(map[string]interface{}{
		"remain_quota":  remainQuota,
		"used_quota":    usedQuota,
		"accessed_time": common.GetTimestamp(),
	}).Error; err != nil {
		return 0, "", err
	}
	return amount, token.Key, nil
}

func reserveInitialSubscriptionQuotaTx(tx *gorm.DB, request BillingReservationRequest, amount int) (*UserSubscription, error) {
	var existing SubscriptionPreConsumeRecord
	probe := tx.Where("request_id = ?", request.RequestID).Limit(1).Find(&existing)
	if probe.Error != nil {
		return nil, probe.Error
	}
	if probe.RowsAffected != 0 {
		return nil, fmt.Errorf("%w: subscription reservation already exists without a billing reservation", ErrBillingReservationContextMismatch)
	}

	now := getDBTimestampTx(tx)
	// Account and entitlement lifecycle operations use user -> subscription.
	// Take the durable account lock before the active entitlement set so a
	// reservation cannot deadlock cancellation or provider renewal.
	if err := lockBillingUserTx(tx, request.UserID); err != nil {
		return nil, err
	}
	var subscriptions []UserSubscription
	if err := lockForUpdate(tx).
		Where("user_id = ? AND status = ? AND end_time > ?", request.UserID, "active", now).
		Order("id asc").
		Find(&subscriptions).Error; err != nil {
		return nil, err
	}
	if len(subscriptions) == 0 {
		return nil, ErrBillingReservationNoActiveSubscription
	}
	// Row locks are acquired in primary-key order across every multi-
	// subscription path. Restore business selection order only after the full
	// set is locked.
	sort.SliceStable(subscriptions, func(i, j int) bool {
		if subscriptions[i].EndTime != subscriptions[j].EndTime {
			return subscriptions[i].EndTime < subscriptions[j].EndTime
		}
		return subscriptions[i].Id < subscriptions[j].Id
	})

	amount64 := int64(amount)
	for i := range subscriptions {
		subscription := subscriptions[i]
		plan, err := getSubscriptionPlanByIdTx(tx, subscription.PlanId)
		if err != nil {
			return nil, err
		}
		if err := maybeResetUserSubscriptionWithPlanTx(tx, &subscription, plan, now); err != nil {
			return nil, err
		}
		if subscription.AmountUsed < 0 || subscription.AmountTotal < 0 ||
			subscription.AmountUsed > math.MaxInt64-amount64 {
			return nil, errors.New("subscription contains invalid quota values")
		}
		if subscription.AmountTotal > 0 && subscription.AmountTotal-subscription.AmountUsed < amount64 {
			continue
		}

		subscription.AmountUsed += amount64
		if err := tx.Model(&subscription).Updates(map[string]interface{}{
			"amount_used": subscription.AmountUsed,
			"updated_at":  common.GetTimestamp(),
		}).Error; err != nil {
			return nil, err
		}
		record := SubscriptionPreConsumeRecord{
			RequestId:          request.RequestID,
			ClaimToken:         common.GetUUID(),
			UserId:             request.UserID,
			UserSubscriptionId: subscription.Id,
			PreConsumed:        amount64,
			QuotaResetVersion:  subscription.QuotaResetVersion,
			Status:             "consumed",
		}
		if err := tx.Create(&record).Error; err != nil {
			return nil, err
		}
		return &subscription, nil
	}
	return nil, fmt.Errorf("%w: need=%d", ErrBillingReservationInsufficientSubscription, amount)
}

func extendSubscriptionReservationTx(tx *gorm.DB, reservation *BillingReservation, target int, delta int) (*UserSubscription, error) {
	var record SubscriptionPreConsumeRecord
	if err := lockForUpdate(tx).Where("request_id = ?", reservation.RequestID).First(&record).Error; err != nil {
		return nil, err
	}
	if record.UserId != reservation.UserID || record.UserSubscriptionId != reservation.SubscriptionID ||
		record.Status != "consumed" || record.PreConsumed != int64(reservation.ReservedQuota) {
		return nil, ErrBillingReservationContextMismatch
	}
	if err := lockBillingUserTx(tx, reservation.UserID); err != nil {
		return nil, err
	}

	var subscription UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", reservation.SubscriptionID).First(&subscription).Error; err != nil {
		return nil, err
	}
	if subscription.UserId != reservation.UserID || subscription.AmountUsed < 0 || subscription.AmountTotal < 0 {
		return nil, ErrBillingReservationContextMismatch
	}
	if subscription.QuotaResetVersion < record.QuotaResetVersion {
		return nil, errors.New("subscription quota reset version moved backwards")
	}
	if subscription.QuotaResetVersion > record.QuotaResetVersion {
		return nil, errors.New("subscription quota period changed during billing reservation")
	}
	delta64 := int64(delta)
	if subscription.AmountUsed > math.MaxInt64-delta64 {
		return nil, errors.New("subscription used quota overflow")
	}
	if subscription.AmountTotal > 0 && subscription.AmountTotal-subscription.AmountUsed < delta64 {
		return nil, fmt.Errorf("%w: remaining=%d need=%d", ErrBillingReservationInsufficientSubscription, subscription.AmountTotal-subscription.AmountUsed, delta)
	}
	subscription.AmountUsed += delta64
	if err := tx.Model(&subscription).Updates(map[string]interface{}{
		"amount_used": subscription.AmountUsed,
		"updated_at":  common.GetTimestamp(),
	}).Error; err != nil {
		return nil, err
	}
	if err := tx.Model(&record).Updates(map[string]interface{}{
		"pre_consumed": int64(target),
		"updated_at":   common.GetTimestamp(),
	}).Error; err != nil {
		return nil, err
	}
	return &subscription, nil
}

func invalidateBillingReservationCaches(result *BillingReservationResult) {
	if result == nil {
		return
	}
	if result.FundingSource == BillingAdjustmentWallet && result.ReservedQuota != 0 {
		if err := invalidateUserCache(result.UserID); err != nil {
			common.SysLog("failed to invalidate billing reservation user cache: " + err.Error())
		}
	}
	if common.RedisEnabled && result.TokenReserved != 0 && result.tokenKeyForCacheInvalidation != "" {
		if err := cacheDeleteToken(result.tokenKeyForCacheInvalidation); err != nil {
			common.SysLog("failed to invalidate billing reservation token cache: " + err.Error())
		}
	}
}

func billingReservationTerminalStatus(kind string) (string, error) {
	switch kind {
	case BillingAdjustmentSettle:
		return billingReservationStatusSettled, nil
	case BillingAdjustmentRefund:
		return billingReservationStatusRefunded, nil
	default:
		return "", fmt.Errorf("invalid billing reservation terminal kind: %s", kind)
	}
}

func markBillingReservationTerminalTx(tx *gorm.DB, requestID string, kind string) error {
	return markBillingReservationTerminalWithPolicyTx(tx, requestID, kind, false)
}

func markBillingAdjustmentReservationTerminalTx(tx *gorm.DB, adjustment BillingAdjustment) error {
	allowRefundedSubscriptionProof := adjustment.Kind == BillingAdjustmentSettle &&
		adjustment.FundingSource == BillingAdjustmentSubscription &&
		adjustment.FundingDelta == 0 &&
		adjustment.TokenDelta == 0 &&
		adjustment.SubscriptionRequestID == adjustment.RequestID
	return markBillingReservationTerminalWithPolicyTx(
		tx,
		adjustment.RequestID,
		adjustment.Kind,
		allowRefundedSubscriptionProof,
	)
}

func markBillingReservationTerminalWithPolicyTx(
	tx *gorm.DB,
	requestID string,
	kind string,
	allowRefundedSubscriptionProof bool,
) error {
	if tx == nil {
		return errors.New("billing reservation transaction is nil")
	}
	if strings.TrimSpace(requestID) == "" {
		return errors.New("billing reservation request id is empty")
	}
	targetStatus, err := billingReservationTerminalStatus(kind)
	if err != nil {
		return err
	}

	var reservation BillingReservation
	result := lockForUpdate(tx).Where("request_id = ?", requestID).Limit(1).Find(&reservation)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return nil
	}
	if reservation.Status == targetStatus {
		return nil
	}
	if reservation.Status != "" && reservation.Status != billingReservationStatusPending {
		if allowRefundedSubscriptionProof && reservation.Status == billingReservationStatusRefunded {
			return nil
		}
		return fmt.Errorf("%w: request %s has status %s", ErrBillingReservationTerminal, requestID, reservation.Status)
	}
	if kind == BillingAdjustmentSettle && reservation.FundingSource == BillingAdjustmentSubscription {
		var record SubscriptionPreConsumeRecord
		if err := lockForUpdate(tx).Where("request_id = ?", requestID).First(&record).Error; err != nil {
			return err
		}
		if record.UserId != reservation.UserID ||
			record.UserSubscriptionId != reservation.SubscriptionID ||
			record.PreConsumed < 0 ||
			record.PreConsumed > int64(reservation.ReservedQuota) {
			return ErrBillingReservationContextMismatch
		}
		switch record.Status {
		case "consumed":
			if record.PreConsumed != int64(reservation.ReservedQuota) {
				return ErrBillingReservationContextMismatch
			}
			if err := tx.Model(&record).Updates(map[string]interface{}{
				"status":     "settled",
				"updated_at": common.GetTimestamp(),
			}).Error; err != nil {
				return err
			}
		case "settled":
			// A nonzero adjustment closes the proof before closing the
			// reservation. Replaying that transaction is safe.
		case "refunded":
			if !allowRefundedSubscriptionProof {
				return fmt.Errorf("%w: subscription reservation %s was refunded", ErrBillingReservationTerminal, requestID)
			}
			// A terminal async-task refund can win before the accepted task's
			// initial zero-delta finalization. The provider job was still
			// accepted, so that finalization must complete its aggregates/log,
			// but the original request tombstone must remain refunded.
			targetStatus = billingReservationStatusRefunded
		default:
			return ErrBillingReservationContextMismatch
		}
	}
	update := tx.Model(&BillingReservation{}).
		Where("id = ? AND (status = ? OR status = ?)", reservation.ID, "", billingReservationStatusPending).
		Updates(map[string]interface{}{
			"status":     targetStatus,
			"updated_at": common.GetTimestamp(),
		})
	if update.Error != nil {
		return update.Error
	}
	if update.RowsAffected == 0 {
		var current BillingReservation
		if err := lockForUpdate(tx).Where("id = ?", reservation.ID).First(&current).Error; err != nil {
			return err
		}
		if current.Status == targetStatus {
			return nil
		}
		return fmt.Errorf("%w: request %s changed concurrently to %s", ErrBillingReservationTerminal, requestID, current.Status)
	}
	return nil
}

// MarkBillingReservationTerminal closes a reservation that needs no balance
// delta, such as a settlement whose estimate exactly matched final usage.
func MarkBillingReservationTerminal(requestID string, kind string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		return markBillingReservationTerminalTx(tx, requestID, kind)
	})
}

func CleanupBillingReservations(olderThanSeconds int64) (int64, error) {
	// Terminal reservations are permanent idempotency tombstones. Deleting one
	// lets a delayed replay reserve the same request again even though its
	// deterministic settlement task still reports AlreadyProcessed, leaving the
	// duplicate reservation charge with no matching settlement to reverse it.
	// A future compactor may replace rows with smaller tombstones, but it must
	// preserve the request ID and immutable billing context.
	return 0, nil
}
