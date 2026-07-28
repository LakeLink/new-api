package model

import (
	"errors"
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"

	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

type TopUp struct {
	Id     int   `json:"id"`
	UserId int   `json:"user_id" gorm:"index"`
	Amount int64 `json:"amount"`
	// Quota is the exact amount credited to the user. Older rows leave this at
	// zero and are interpreted using the legacy provider-specific fields.
	Quota                int     `json:"quota" gorm:"not null;default:0"`
	RefundedQuota        int     `json:"refunded_quota" gorm:"not null;default:0"`
	ProviderRefundEvents string  `json:"-" gorm:"type:text"`
	Money                float64 `json:"money"`
	TradeNo              string  `json:"trade_no" gorm:"type:varchar(255)"`
	TradeNoHash          *string `json:"-" gorm:"type:char(64);uniqueIndex:ux_topups_trade_no_hash"`
	ProviderPaymentId    string  `json:"provider_payment_id" gorm:"type:varchar(255)"`
	ProviderPaymentHash  *string `json:"-" gorm:"type:char(64);uniqueIndex:ux_topups_provider_payment_hash"`
	// ProviderPaidAmountMinor distinguishes the verified provider settlement
	// amount from Money, which remains the original checkout quote. Nil denotes
	// a legacy completed row whose exact paid amount was not persisted; a
	// non-nil zero records a valid 100%-promotion checkout.
	ProviderPaidAmountMinor *int64 `json:"provider_paid_amount_minor" gorm:"type:bigint"`
	ProviderProductId       string `json:"provider_product_id" gorm:"type:varchar(255)"`
	Currency                string `json:"currency" gorm:"type:varchar(8)"`
	PaymentMethod           string `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider         string `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	CreateTime              int64  `json:"create_time"`
	CompleteTime            int64  `json:"complete_time"`
	Status                  string `json:"status"`
}

func (topUp *TopUp) normalizeIdentities() error {
	tradeNo, tradeNoHash, err := normalizeTradeNumberIdentity(topUp.TradeNo)
	if err != nil {
		return err
	}
	topUp.TradeNo = tradeNo
	topUp.TradeNoHash = &tradeNoHash
	if topUp.ProviderPaymentId == "" {
		topUp.ProviderPaymentHash = nil
		return nil
	}
	paymentProvider, providerPaymentID, providerPaymentHash, err :=
		normalizeProviderPaymentIdentity(topUp.PaymentProvider, topUp.ProviderPaymentId)
	if err != nil {
		return err
	}
	topUp.PaymentProvider = paymentProvider
	topUp.ProviderPaymentId = providerPaymentID
	topUp.ProviderPaymentHash = &providerPaymentHash
	return nil
}

func (topUp *TopUp) BeforeCreate(_ *gorm.DB) error {
	return topUp.normalizeIdentities()
}

func (topUp *TopUp) BeforeUpdate(tx *gorm.DB) error {
	// Map-based status updates use a zero-value model and must not synthesize a
	// provider identity. Save calls carry the persisted ID and full identity.
	if topUp.Id <= 0 {
		return nil
	}
	if err := topUp.normalizeIdentities(); err != nil {
		return err
	}
	tx.Statement.SetColumn("trade_no", topUp.TradeNo)
	tx.Statement.SetColumn("trade_no_hash", topUp.TradeNoHash)
	tx.Statement.SetColumn("payment_provider", topUp.PaymentProvider)
	tx.Statement.SetColumn("provider_payment_id", topUp.ProviderPaymentId)
	tx.Statement.SetColumn("provider_payment_hash", topUp.ProviderPaymentHash)
	return nil
}

const (
	PaymentMethodStripe       = "stripe"
	PaymentMethodCreem        = "creem"
	PaymentMethodWaffo        = "waffo"
	PaymentMethodWaffoPancake = "waffo_pancake"
	PaymentMethodBalance      = "balance"
)

const (
	TopUpStatusPartiallyRefunded = "partially_refunded"
	TopUpStatusRefunded          = "refunded"
)

// TopUpQuotaForAmount converts a user-facing top-up quantity into the exact
// quota that may be persisted. It rejects saturation rather than granting a
// clamped amount after a customer has paid.
func TopUpQuotaForAmount(amount int64, amountIsTokens bool) (int, error) {
	if amount <= 0 {
		return 0, errors.New("充值数量必须大于0")
	}
	quotaValue := decimal.NewFromInt(amount)
	if !amountIsTokens {
		quotaPerUnit := common.CurrentQuotaPerUnit()
		if math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) ||
			quotaPerUnit <= 0 || quotaPerUnit > float64(common.MaxQuota) {
			return 0, errors.New("额度单位配置错误")
		}
		quotaValue = quotaValue.Mul(decimal.NewFromFloat(quotaPerUnit))
	}
	quota, clamp := common.QuotaFromDecimalChecked(quotaValue)
	if clamp != nil {
		return 0, clamp
	}
	if quota <= 0 {
		return 0, errors.New("无效的充值额度")
	}
	return quota, nil
}

func topUpCreditQuota(topUp *TopUp) (int, error) {
	if topUp == nil {
		return 0, errors.New("充值订单不存在")
	}
	if topUp.Quota > 0 {
		if topUp.Quota > common.MaxQuota {
			return 0, errors.New("无效的充值额度")
		}
		return topUp.Quota, nil
	}
	// Backward compatibility for orders created before the exact quota column
	// was introduced.
	value := decimal.NewFromInt(topUp.Amount)
	quotaPerUnit := 0.0
	if topUp.PaymentProvider != PaymentProviderCreem {
		quotaPerUnit = common.CurrentQuotaPerUnit()
		if math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) ||
			quotaPerUnit <= 0 || quotaPerUnit > float64(common.MaxQuota) {
			return 0, errors.New("额度单位配置错误")
		}
		value = value.Mul(decimal.NewFromFloat(quotaPerUnit))
	}
	if topUp.PaymentProvider == PaymentProviderStripe {
		if math.IsNaN(topUp.Money) || math.IsInf(topUp.Money, 0) || topUp.Money <= 0 {
			return 0, errors.New("无效的充值金额")
		}
		value = decimal.NewFromFloat(topUp.Money).Mul(decimal.NewFromFloat(quotaPerUnit))
	}
	quota, clamp := common.QuotaFromDecimalChecked(value)
	if clamp != nil {
		return 0, clamp
	}
	if quota <= 0 {
		return 0, errors.New("无效的充值额度")
	}
	return quota, nil
}

func topUpPaidAmountMinor(topUp *TopUp) (int64, error) {
	if topUp == nil {
		return 0, errors.New("充值订单不存在")
	}
	if topUp.ProviderPaidAmountMinor != nil {
		if *topUp.ProviderPaidAmountMinor < 0 {
			return 0, errors.New("persisted provider payment amount is invalid")
		}
		return *topUp.ProviderPaidAmountMinor, nil
	}
	if math.IsNaN(topUp.Money) || math.IsInf(topUp.Money, 0) || topUp.Money <= 0 {
		return 0, errors.New("persisted provider payment amount is invalid")
	}
	expectedMinorDecimal := decimal.NewFromFloat(topUp.Money).
		Mul(decimal.NewFromInt(100)).
		Round(0)
	if !expectedMinorDecimal.IsPositive() ||
		expectedMinorDecimal.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, errors.New("persisted provider payment amount is invalid")
	}
	return expectedMinorDecimal.IntPart(), nil
}

const (
	PaymentProviderEpay         = "epay"
	PaymentProviderStripe       = "stripe"
	PaymentProviderCreem        = "creem"
	PaymentProviderWaffo        = "waffo"
	PaymentProviderWaffoPancake = "waffo_pancake"
	PaymentProviderBalance      = "balance"
)

var (
	ErrPaymentMethodMismatch = errors.New("payment method mismatch")
	ErrTopUpNotFound         = errors.New("topup not found")
	ErrTopUpStatusInvalid    = errors.New("topup status invalid")
)

func creditTopUpQuotaTx(tx *gorm.DB, userId int, quota int, updates map[string]interface{}) error {
	if quota <= 0 || quota > common.MaxQuota {
		return errors.New("无效的充值额度")
	}
	if updates == nil {
		updates = make(map[string]interface{})
	}
	updates["quota"] = gorm.Expr("quota + ?", quota)
	result := tx.Model(&User{}).
		Where("id = ? AND quota <= ?", userId, common.MaxQuota-quota).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return errors.New("充值用户不存在或充值后额度超出允许范围")
	}
	return nil
}

func (topUp *TopUp) Insert() error {
	var err error
	err = DB.Create(topUp).Error
	return err
}

func (topUp *TopUp) Update() error {
	var err error
	err = DB.Save(topUp).Error
	return err
}

func GetTopUpById(id int) *TopUp {
	var topUp *TopUp
	var err error
	err = DB.Where("id = ?", id).First(&topUp).Error
	if err != nil {
		return nil
	}
	return topUp
}

func GetTopUpByTradeNo(tradeNo string) *TopUp {
	topUp, _ := FindTopUpByTradeNo(tradeNo)
	return topUp
}

// FindTopUpByTradeNo preserves database failures for payment/webhook callers
// that must distinguish a missing order from a transient storage outage.
func FindTopUpByTradeNo(tradeNo string) (*TopUp, error) {
	var topUp TopUp
	if err := findTopUpByTradeNo(DB, tradeNo, &topUp); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrTopUpNotFound
		}
		return nil, err
	}
	return &topUp, nil
}

func findTopUpByTradeNo(db *gorm.DB, tradeNo string, topUp *TopUp) error {
	if db == nil || topUp == nil {
		return errors.New("topup lookup is invalid")
	}
	tradeNo, tradeNoHash, err := normalizeTradeNumberIdentity(tradeNo)
	if err != nil {
		return err
	}
	if err := db.Where("trade_no_hash = ?", tradeNoHash).First(topUp).Error; err != nil {
		return err
	}
	if topUp.TradeNo != tradeNo {
		return errors.New("trade number identity hash collision")
	}
	return nil
}

func UpdatePendingTopUpStatus(tradeNo string, expectedPaymentProvider string, targetStatus string) error {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		if err := findTopUpByTradeNo(lockForUpdate(tx), tradeNo, topUp); err != nil {
			return ErrTopUpNotFound
		}
		if expectedPaymentProvider != "" && topUp.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status == targetStatus {
			return nil
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}

		topUp.Status = targetStatus
		return tx.Save(topUp).Error
	})
}

func Recharge(referenceId string, customerId string, providerPaymentId string, paidAmountMinor int64, callerIp string) (err error) {
	if referenceId == "" {
		return errors.New("未提供支付单号")
	}
	if paidAmountMinor < 0 {
		return errors.New("支付金额无效")
	}

	var quota int
	completed := false
	topUp := &TopUp{}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := findTopUpByTradeNo(lockForUpdate(tx), referenceId, topUp)
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderStripe {
			return ErrPaymentMethodMismatch
		}

		if math.IsNaN(topUp.Money) || math.IsInf(topUp.Money, 0) || topUp.Money <= 0 {
			return errors.New("支付金额与订单不匹配")
		}
		expectedAmountMinor := decimal.NewFromFloat(topUp.Money).
			Mul(decimal.NewFromInt(100)).
			Round(0)
		if !expectedAmountMinor.IsPositive() ||
			expectedAmountMinor.GreaterThan(decimal.NewFromInt(math.MaxInt64)) ||
			paidAmountMinor > expectedAmountMinor.IntPart() {
			return errors.New("支付金额与订单不匹配")
		}
		terminal := topUp.Status == common.TopUpStatusSuccess ||
			topUp.Status == TopUpStatusPartiallyRefunded ||
			topUp.Status == TopUpStatusRefunded
		if terminal {
			changed := false
			if topUp.ProviderPaidAmountMinor != nil {
				if providerPaymentId != topUp.ProviderPaymentId ||
					*topUp.ProviderPaidAmountMinor != paidAmountMinor {
					return ErrPaymentMethodMismatch
				}
			} else {
				// Legacy completed rows lack exact amount evidence. A verified
				// replay may fill it once, but it still cannot replace an
				// already-bound provider payment identity.
				if topUp.ProviderPaymentId != "" && providerPaymentId != topUp.ProviderPaymentId {
					return ErrPaymentMethodMismatch
				}
				if topUp.ProviderPaymentId == "" && providerPaymentId != "" {
					topUp.ProviderPaymentId = providerPaymentId
				}
				topUp.ProviderPaidAmountMinor = &paidAmountMinor
				changed = true
			}
			if changed {
				return tx.Save(topUp).Error
			}
			return nil
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}

		quota, err = topUpCreditQuota(topUp)
		if err != nil {
			return err
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		topUp.Quota = quota
		topUp.ProviderPaidAmountMinor = &paidAmountMinor
		if providerPaymentId != "" {
			topUp.ProviderPaymentId = providerPaymentId
		}
		err = tx.Save(topUp).Error
		if err != nil {
			return err
		}

		if err := creditTopUpQuotaTx(tx, topUp.UserId, quota, map[string]interface{}{"stripe_customer": customerId}); err != nil {
			return err
		}

		completed = true
		return nil
	})

	if err != nil {
		common.SysError("topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if !completed {
		return nil
	}
	if err := InvalidateUserCache(topUp.UserId); err != nil {
		common.SysLog("failed to invalidate user cache after Stripe topup: " + err.Error())
	}
	paidMoney := decimal.NewFromInt(paidAmountMinor).
		Div(decimal.NewFromInt(100)).
		InexactFloat64()
	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值额度: %v，支付金额：%.2f", logger.FormatQuota(quota), paidMoney), callerIp, topUp.PaymentMethod, PaymentMethodStripe)

	return nil
}

func RechargeEpay(referenceId string, paymentMethod string, providerPaymentId string, callerIp string) error {
	if referenceId == "" {
		return errors.New("未提供支付单号")
	}

	var quota int
	completed := false
	topUp := &TopUp{}

	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := findTopUpByTradeNo(lockForUpdate(tx), referenceId, topUp); err != nil {
			return ErrTopUpNotFound
		}
		if topUp.PaymentProvider != PaymentProviderEpay || (paymentMethod != "" && topUp.PaymentMethod != paymentMethod) {
			return ErrPaymentMethodMismatch
		}
		if topUp.ProviderPaymentId != "" && providerPaymentId != "" && topUp.ProviderPaymentId != providerPaymentId {
			return ErrPaymentMethodMismatch
		}
		if topUp.Status == common.TopUpStatusSuccess || topUp.Status == TopUpStatusPartiallyRefunded || topUp.Status == TopUpStatusRefunded {
			return nil
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}

		var err error
		quota, err = topUpCreditQuota(topUp)
		if err != nil {
			return err
		}
		topUp.Quota = quota
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		if providerPaymentId != "" {
			topUp.ProviderPaymentId = providerPaymentId
		}
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}
		if err := creditTopUpQuotaTx(tx, topUp.UserId, quota, nil); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err != nil {
		return err
	}
	if !completed {
		return nil
	}
	if err := InvalidateUserCache(topUp.UserId); err != nil {
		common.SysLog("failed to invalidate user cache after Epay topup: " + err.Error())
	}
	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用在线充值成功，充值额度: %v，支付金额：%.2f", logger.FormatQuota(quota), topUp.Money), callerIp, topUp.PaymentMethod, PaymentProviderEpay)
	return nil
}

// topUpQueryWindowSeconds 限制充值记录查询的时间窗口（秒）。
const topUpQueryWindowSeconds int64 = 30 * 24 * 60 * 60

// topUpQueryCutoff 返回允许查询的最早 create_time（秒级 Unix 时间戳）。
func topUpQueryCutoff() int64 {
	return common.GetTimestamp() - topUpQueryWindowSeconds
}

func GetUserTopUps(userId int, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	// Start transaction
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	cutoff := topUpQueryCutoff()

	// Get total count within transaction
	err = tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, cutoff).Count(&total).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Get paginated topups within same transaction
	err = tx.Where("user_id = ? AND create_time >= ?", userId, cutoff).Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error
	if err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	// Commit transaction
	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// GetAllTopUps 获取全平台的充值记录（管理员使用，不限制时间窗口）
func GetAllTopUps(pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	if err = tx.Model(&TopUp{}).Count(&total).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		return nil, 0, err
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}

	return topups, total, nil
}

// searchTopUpCountHardLimit 搜索充值记录时 COUNT 的安全上限，
// 防止对超大表执行无界 COUNT 触发 DoS。
const searchTopUpCountHardLimit = 10000

// SearchUserTopUps 按订单号搜索某用户的充值记录
func SearchUserTopUps(userId int, keyword string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{}).Where("user_id = ? AND create_time >= ?", userId, topUpQueryCutoff())
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to count search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// SearchAllTopUps 按订单号搜索全平台充值记录（管理员使用，不限制时间窗口）
func SearchAllTopUps(keyword string, pageInfo *common.PageInfo) (topups []*TopUp, total int64, err error) {
	tx := DB.Begin()
	if tx.Error != nil {
		return nil, 0, tx.Error
	}
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	query := tx.Model(&TopUp{})
	if keyword != "" {
		pattern, perr := sanitizeLikePattern(keyword)
		if perr != nil {
			tx.Rollback()
			return nil, 0, perr
		}
		query = query.Where("trade_no LIKE ? ESCAPE '!'", pattern)
	}

	if err = query.Limit(searchTopUpCountHardLimit).Count(&total).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to count search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = query.Order("id desc").Limit(pageInfo.GetPageSize()).Offset(pageInfo.GetStartIdx()).Find(&topups).Error; err != nil {
		tx.Rollback()
		common.SysError("failed to search topups: " + err.Error())
		return nil, 0, errors.New("搜索充值记录失败")
	}

	if err = tx.Commit().Error; err != nil {
		return nil, 0, err
	}
	return topups, total, nil
}

// ManualCompleteTopUp 管理员手动完成订单并给用户充值
func ManualCompleteTopUp(tradeNo string, callerIp string) error {
	if tradeNo == "" {
		return errors.New("未提供订单号")
	}

	var userId int
	var quotaToAdd int
	var payMoney float64
	var paymentMethod string
	completed := false

	err := DB.Transaction(func(tx *gorm.DB) error {
		topUp := &TopUp{}
		// 行级锁，避免并发补单
		if err := findTopUpByTradeNo(lockForUpdate(tx), tradeNo, topUp); err != nil {
			return errors.New("充值订单不存在")
		}

		// 幂等处理：已成功直接返回
		if topUp.Status == common.TopUpStatusSuccess {
			return nil
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("订单状态不是待支付，无法补单")
		}

		// 计算应充值额度：
		// - Stripe 订单：Money 代表经分组倍率换算后的美元数量，直接 * QuotaPerUnit
		// - 其他订单（如易支付）：Amount 为美元数量，* QuotaPerUnit
		calculatedQuota, err := topUpCreditQuota(topUp)
		if err != nil {
			return err
		}
		quotaToAdd = calculatedQuota

		// 标记完成
		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		topUp.Quota = quotaToAdd
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		// 增加用户额度（立即写库，保持一致性）
		if err := creditTopUpQuotaTx(tx, topUp.UserId, quotaToAdd, nil); err != nil {
			return err
		}

		userId = topUp.UserId
		payMoney = topUp.Money
		paymentMethod = topUp.PaymentMethod
		completed = true
		return nil
	})

	if err != nil {
		return err
	}
	if !completed {
		return nil
	}
	if err := InvalidateUserCache(userId); err != nil {
		common.SysLog("failed to invalidate user cache after manual topup: " + err.Error())
	}

	// 事务外记录日志，避免阻塞
	RecordTopupLog(userId, fmt.Sprintf("管理员补单成功，充值金额: %v，支付金额：%f", logger.FormatQuota(quotaToAdd), payMoney), callerIp, paymentMethod, "admin")
	return nil
}
func RechargeCreem(referenceId string, providerPaymentId string, paidAmountMinor int64, customerEmail string, customerName string, callerIp string) (err error) {
	if referenceId == "" {
		return errors.New("未提供支付单号")
	}
	if paidAmountMinor < 0 {
		return errors.New("支付金额无效")
	}

	var quota int
	completed := false
	topUp := &TopUp{}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := findTopUpByTradeNo(lockForUpdate(tx), referenceId, topUp)
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderCreem {
			return ErrPaymentMethodMismatch
		}

		terminal := topUp.Status == common.TopUpStatusSuccess ||
			topUp.Status == TopUpStatusPartiallyRefunded ||
			topUp.Status == TopUpStatusRefunded
		if terminal {
			changed := false
			if topUp.ProviderPaidAmountMinor != nil {
				if providerPaymentId != topUp.ProviderPaymentId ||
					*topUp.ProviderPaidAmountMinor != paidAmountMinor {
					return ErrPaymentMethodMismatch
				}
			} else {
				if topUp.ProviderPaymentId != "" && providerPaymentId != topUp.ProviderPaymentId {
					return ErrPaymentMethodMismatch
				}
				if topUp.ProviderPaymentId == "" && providerPaymentId != "" {
					topUp.ProviderPaymentId = providerPaymentId
				}
				topUp.ProviderPaidAmountMinor = &paidAmountMinor
				changed = true
			}
			if changed {
				return tx.Save(topUp).Error
			}
			return nil
		}
		if topUp.Status != common.TopUpStatusPending {
			return ErrTopUpStatusInvalid
		}
		quota, err = topUpCreditQuota(topUp)
		if err != nil {
			return err
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		topUp.Quota = quota
		topUp.ProviderPaidAmountMinor = &paidAmountMinor
		if providerPaymentId != "" {
			topUp.ProviderPaymentId = providerPaymentId
		}
		err = tx.Save(topUp).Error
		if err != nil {
			return err
		}

		// 构建更新字段，优先使用邮箱，如果邮箱为空则使用用户名
		updateFields := make(map[string]interface{})

		// 如果有客户邮箱，尝试更新用户邮箱（仅当用户邮箱为空时）
		if customerEmail != "" {
			// 先检查用户当前邮箱是否为空
			var user User
			err = tx.Where("id = ?", topUp.UserId).First(&user).Error
			if err != nil {
				return err
			}

			// 如果用户邮箱为空，则更新为支付时使用的邮箱
			if user.Email == "" {
				updateFields["email"] = customerEmail
			}
		}

		if err := creditTopUpQuotaTx(tx, topUp.UserId, quota, updateFields); err != nil {
			return err
		}

		completed = true
		return nil
	})

	if err != nil {
		common.SysError("creem topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if !completed {
		return nil
	}
	if err := InvalidateUserCache(topUp.UserId); err != nil {
		common.SysLog("failed to invalidate user cache after Creem topup: " + err.Error())
	}
	paidMoney := decimal.NewFromInt(paidAmountMinor).
		Div(decimal.NewFromInt(100)).
		InexactFloat64()
	RecordTopupLog(topUp.UserId, fmt.Sprintf("使用Creem充值成功，充值额度: %v，支付金额：%.2f", quota, paidMoney), callerIp, topUp.PaymentMethod, PaymentMethodCreem)

	return nil
}

func RechargeWaffo(tradeNo string, providerPaymentId string, callerIp string) (err error) {
	if tradeNo == "" || providerPaymentId == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	completed := false
	topUp := &TopUp{}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := findTopUpByTradeNo(lockForUpdate(tx), tradeNo, topUp)
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderWaffo {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess || topUp.Status == TopUpStatusPartiallyRefunded || topUp.Status == TopUpStatusRefunded {
			if topUp.ProviderPaymentId != "" && topUp.ProviderPaymentId != providerPaymentId {
				return ErrPaymentMethodMismatch
			}
			if topUp.ProviderPaymentId == "" {
				topUp.ProviderPaymentId = providerPaymentId
				return tx.Save(topUp).Error
			}
			return nil // 幂等：已成功直接返回
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quotaToAdd, err = topUpCreditQuota(topUp)
		if err != nil {
			return err
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		topUp.Quota = quotaToAdd
		topUp.ProviderPaymentId = providerPaymentId
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := creditTopUpQuotaTx(tx, topUp.UserId, quotaToAdd, nil); err != nil {
			return err
		}

		completed = true
		return nil
	})

	if err != nil {
		common.SysError("waffo topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if completed {
		if err := InvalidateUserCache(topUp.UserId); err != nil {
			common.SysLog("failed to invalidate user cache after Waffo topup: " + err.Error())
		}
		RecordTopupLog(topUp.UserId, fmt.Sprintf("Waffo充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money), callerIp, topUp.PaymentMethod, PaymentMethodWaffo)
	}

	return nil
}

func RechargeWaffoPancake(tradeNo string) (err error) {
	if tradeNo == "" {
		return errors.New("未提供支付单号")
	}

	var quotaToAdd int
	completed := false
	topUp := &TopUp{}

	err = DB.Transaction(func(tx *gorm.DB) error {
		err := findTopUpByTradeNo(lockForUpdate(tx), tradeNo, topUp)
		if err != nil {
			return errors.New("充值订单不存在")
		}

		if topUp.PaymentProvider != PaymentProviderWaffoPancake {
			return ErrPaymentMethodMismatch
		}

		if topUp.Status == common.TopUpStatusSuccess || topUp.Status == TopUpStatusPartiallyRefunded || topUp.Status == TopUpStatusRefunded {
			return nil
		}

		if topUp.Status != common.TopUpStatusPending {
			return errors.New("充值订单状态错误")
		}

		quotaToAdd, err = topUpCreditQuota(topUp)
		if err != nil {
			return err
		}

		topUp.CompleteTime = common.GetTimestamp()
		topUp.Status = common.TopUpStatusSuccess
		topUp.Quota = quotaToAdd
		if err := tx.Save(topUp).Error; err != nil {
			return err
		}

		if err := creditTopUpQuotaTx(tx, topUp.UserId, quotaToAdd, nil); err != nil {
			return err
		}

		completed = true
		return nil
	})

	if err != nil {
		common.SysError("waffo pancake topup failed: " + err.Error())
		return errors.New("充值失败，请稍后重试")
	}

	if completed {
		if err := InvalidateUserCache(topUp.UserId); err != nil {
			common.SysLog("failed to invalidate user cache after Waffo Pancake topup: " + err.Error())
		}
		RecordLog(topUp.UserId, LogTypeTopup, fmt.Sprintf("Waffo Pancake充值成功，充值额度: %v，支付金额: %.2f", logger.FormatQuota(quotaToAdd), topUp.Money))
	}

	return nil
}

// ReverseTopUpByProviderPayment revokes quota for a cumulative provider
// refund or dispute. refundMoney is the provider-reported cumulative refunded
// amount; full forces the entire credit to be revoked. The persisted
// RefundedQuota makes repeated webhook deliveries idempotent.
func ReverseTopUpByProviderPayment(paymentProvider string, providerPaymentId string, refundMoney float64, full bool) error {
	if paymentProvider == "" || providerPaymentId == "" {
		return errors.New("missing provider payment identifier")
	}
	return reverseTopUp(paymentProvider, "provider_payment_id", providerPaymentId, refundMoney, full)
}

// ReverseTopUpByTradeNo is the trade-number equivalent used by gateways whose
// refund notifications reference the original merchant order directly.
func ReverseTopUpByTradeNo(paymentProvider string, tradeNo string, refundMoney float64, full bool) error {
	if paymentProvider == "" || tradeNo == "" {
		return errors.New("missing topup identifier")
	}
	return reverseTopUp(paymentProvider, "trade_no", tradeNo, refundMoney, full)
}

// ReverseTopUpByTradeNoEvent applies a provider refund amount exactly once.
// Providers such as Waffo Pancake report each partial refund independently
// rather than reporting a cumulative refunded total, so the verified provider
// event ID and minor-unit amount are persisted on the order.
func ReverseTopUpByTradeNoEvent(paymentProvider string, tradeNo string, eventID string, refundMinor int64) error {
	if paymentProvider == "" || tradeNo == "" || eventID == "" || refundMinor <= 0 {
		return errors.New("invalid provider refund event")
	}

	var userId int
	var revoked int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var topUp TopUp
		if err := findTopUpByTradeNo(lockForUpdate(tx), tradeNo, &topUp); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTopUpNotFound
			}
			return err
		}
		if topUp.PaymentProvider != paymentProvider {
			return ErrPaymentMethodMismatch
		}
		if topUp.Quota == 0 && topUp.Amount == 0 {
			return ErrTopUpNotFound
		}
		if topUp.Status != common.TopUpStatusSuccess &&
			topUp.Status != TopUpStatusPartiallyRefunded &&
			topUp.Status != TopUpStatusRefunded {
			return ErrTopUpStatusInvalid
		}

		quota, err := topUpCreditQuota(&topUp)
		if err != nil {
			return err
		}
		expectedMinor, err := topUpPaidAmountMinor(&topUp)
		if err != nil {
			return err
		}
		if expectedMinor <= 0 || refundMinor > expectedMinor {
			return errors.New("provider refund amount is invalid")
		}

		refundEvents := map[string]int64{}
		if topUp.ProviderRefundEvents != "" {
			if err := common.UnmarshalJsonStr(topUp.ProviderRefundEvents, &refundEvents); err != nil {
				return fmt.Errorf("invalid persisted provider refund state: %w", err)
			}
		}
		if persistedMinor, exists := refundEvents[eventID]; exists {
			if persistedMinor != refundMinor {
				return errors.New("provider refund event amount changed")
			}
			return nil
		}
		const maxProviderRefundEvents = 1000
		if len(refundEvents) >= maxProviderRefundEvents {
			return errors.New("provider refund event limit exceeded")
		}
		refundEvents[eventID] = refundMinor

		cumulativeMinor := int64(0)
		for _, amountMinor := range refundEvents {
			if amountMinor <= 0 || amountMinor > expectedMinor {
				return errors.New("persisted provider refund amount is invalid")
			}
			if cumulativeMinor > expectedMinor-amountMinor {
				return errors.New("provider refund total exceeds payment amount")
			}
			cumulativeMinor += amountMinor
		}
		desired := decimal.NewFromInt(int64(quota)).
			Mul(decimal.NewFromInt(cumulativeMinor)).
			Div(decimal.NewFromInt(expectedMinor))
		desiredRefundedQuota, clamp := common.QuotaFromDecimalChecked(desired)
		if clamp != nil {
			return clamp
		}
		if desiredRefundedQuota < topUp.RefundedQuota {
			return errors.New("provider refund state would reduce refunded quota")
		}
		revoked = desiredRefundedQuota - topUp.RefundedQuota

		var user User
		if err := lockForUpdate(tx).Where("id = ?", topUp.UserId).First(&user).Error; err != nil {
			return err
		}
		newQuota, err := checkedQuotaBalanceDelta(user.Quota, -revoked)
		if err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", newQuota).Error; err != nil {
			return err
		}
		encodedEvents, err := common.Marshal(refundEvents)
		if err != nil {
			return err
		}
		topUp.Quota = quota
		topUp.RefundedQuota = desiredRefundedQuota
		topUp.ProviderRefundEvents = string(encodedEvents)
		if desiredRefundedQuota == quota {
			topUp.Status = TopUpStatusRefunded
		} else {
			topUp.Status = TopUpStatusPartiallyRefunded
		}
		if err := tx.Save(&topUp).Error; err != nil {
			return err
		}
		userId = topUp.UserId
		return nil
	})
	if err != nil {
		return err
	}
	if userId > 0 {
		if err := InvalidateUserCache(userId); err != nil {
			common.SysLog("failed to invalidate user cache after provider topup reversal: " + err.Error())
		}
		if revoked > 0 {
			RecordLog(userId, LogTypeTopup, fmt.Sprintf("支付退款撤销额度: %v", logger.FormatQuota(revoked)))
		}
	}
	return nil
}

func reverseTopUp(paymentProvider string, lookupColumn string, lookupValue string, refundMoney float64, full bool) error {
	if !full && (math.IsNaN(refundMoney) || math.IsInf(refundMoney, 0) || refundMoney <= 0) {
		return errors.New("invalid partial refund amount")
	}
	var userId int
	var revoked int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var topUp TopUp
		query := lockForUpdate(tx)
		expectedProviderPaymentID := ""
		expectedTradeNo := ""
		switch lookupColumn {
		case "provider_payment_id":
			normalizedProvider, normalizedPaymentID, paymentHash, err :=
				normalizeProviderPaymentIdentity(paymentProvider, lookupValue)
			if err != nil {
				return err
			}
			paymentProvider = normalizedProvider
			expectedProviderPaymentID = normalizedPaymentID
			query = query.Where("provider_payment_hash = ?", paymentHash)
		case "trade_no":
			normalizedTradeNo, tradeNoHash, err := normalizeTradeNumberIdentity(lookupValue)
			if err != nil {
				return err
			}
			expectedTradeNo = normalizedTradeNo
			query = query.Where("trade_no_hash = ?", tradeNoHash)
		default:
			return errors.New("invalid topup lookup")
		}
		if err := query.First(&topUp).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrTopUpNotFound
			}
			return err
		}
		if topUp.PaymentProvider != paymentProvider {
			return ErrPaymentMethodMismatch
		}
		if expectedProviderPaymentID != "" &&
			topUp.ProviderPaymentId != expectedProviderPaymentID {
			return errors.New("provider payment identity hash collision")
		}
		if expectedTradeNo != "" && topUp.TradeNo != expectedTradeNo {
			return errors.New("trade number identity hash collision")
		}
		// Subscription purchases are mirrored into the top-up history with no
		// wallet credit. Let the caller route those reversals to entitlement
		// cancellation instead of attempting a quota revocation.
		if topUp.Quota == 0 && topUp.Amount == 0 {
			return ErrTopUpNotFound
		}
		if topUp.Status != common.TopUpStatusSuccess && topUp.Status != TopUpStatusPartiallyRefunded && topUp.Status != TopUpStatusRefunded {
			return ErrTopUpStatusInvalid
		}
		quota, err := topUpCreditQuota(&topUp)
		if err != nil {
			return err
		}
		desiredRefundedQuota := quota
		if !full {
			expectedMinor, err := topUpPaidAmountMinor(&topUp)
			if err != nil || expectedMinor <= 0 {
				return errors.New("invalid partial refund amount")
			}
			refundMinor := decimal.NewFromFloat(refundMoney).
				Mul(decimal.NewFromInt(100))
			if !refundMinor.IsPositive() ||
				refundMinor.GreaterThan(decimal.NewFromInt(expectedMinor)) {
				return errors.New("invalid partial refund amount")
			}
			desired := decimal.NewFromInt(int64(quota)).
				Mul(refundMinor).
				Div(decimal.NewFromInt(expectedMinor))
			var clamp *common.QuotaClamp
			desiredRefundedQuota, clamp = common.QuotaFromDecimalChecked(desired)
			if clamp != nil {
				return clamp
			}
		}
		if desiredRefundedQuota <= topUp.RefundedQuota {
			return nil
		}
		revoked = desiredRefundedQuota - topUp.RefundedQuota

		var user User
		if err := lockForUpdate(tx).Where("id = ?", topUp.UserId).First(&user).Error; err != nil {
			return err
		}
		newQuota, err := checkedQuotaBalanceDelta(user.Quota, -revoked)
		if err != nil {
			return err
		}
		if err := tx.Model(&User{}).Where("id = ?", topUp.UserId).Update("quota", newQuota).Error; err != nil {
			return err
		}
		topUp.Quota = quota
		topUp.RefundedQuota = desiredRefundedQuota
		if desiredRefundedQuota == quota {
			topUp.Status = TopUpStatusRefunded
		} else {
			topUp.Status = TopUpStatusPartiallyRefunded
		}
		if err := tx.Save(&topUp).Error; err != nil {
			return err
		}
		userId = topUp.UserId
		return nil
	})
	if err != nil {
		return err
	}
	if userId > 0 {
		if err := InvalidateUserCache(userId); err != nil {
			common.SysLog("failed to invalidate user cache after topup reversal: " + err.Error())
		}
		if revoked > 0 {
			RecordLog(userId, LogTypeTopup, fmt.Sprintf("支付退款撤销额度: %v", logger.FormatQuota(revoked)))
		}
	}
	return nil
}
