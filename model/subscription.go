package model

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/pkg/cachex"
	"github.com/samber/hot"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Subscription duration units
const (
	SubscriptionDurationYear   = "year"
	SubscriptionDurationMonth  = "month"
	SubscriptionDurationDay    = "day"
	SubscriptionDurationHour   = "hour"
	SubscriptionDurationCustom = "custom"
)

// Subscription quota reset period
const (
	SubscriptionResetNever   = "never"
	SubscriptionResetDaily   = "daily"
	SubscriptionResetWeekly  = "weekly"
	SubscriptionResetMonthly = "monthly"
	SubscriptionResetCustom  = "custom"

	maxSubscriptionPlanYears   = 100
	maxSubscriptionResetSecond = int64(maxSubscriptionPlanYears * 366 * 24 * 60 * 60)
)

var (
	ErrSubscriptionOrderNotFound      = errors.New("subscription order not found")
	ErrSubscriptionOrderStatusInvalid = errors.New("subscription order status invalid")
)

// ProviderRefundAmountMode describes whether a webhook amount is a new refund
// delta or the provider's cumulative refunded total for the payment.
type ProviderRefundAmountMode int

const (
	ProviderRefundAmountIncremental ProviderRefundAmountMode = iota + 1
	ProviderRefundAmountCumulative
)

type subscriptionRefundEvent struct {
	AmountMinor int64                    `json:"amount_minor"`
	Mode        ProviderRefundAmountMode `json:"mode"`
	AmountScope string                   `json:"amount_scope,omitempty"`
	Full        bool                     `json:"full,omitempty"`
	EventTime   int64                    `json:"event_time,omitempty"`
}

// SubscriptionRefundInput is the verified provider data needed to apply one
// idempotent subscription-payment refund event.
type SubscriptionRefundInput struct {
	EventID         string
	AmountMinor     int64
	PaidAmountMinor int64
	AmountMode      ProviderRefundAmountMode
	// AmountScope identifies the provider payment/charge whose cumulative
	// refund total AmountMinor represents. Empty retains legacy order-wide
	// cumulative semantics.
	AmountScope       string
	Full              bool
	ProviderEventTime int64
	ProviderPayload   string
}

const (
	subscriptionPlanCacheNamespace     = "new-api:subscription_plan:v1"
	subscriptionPlanInfoCacheNamespace = "new-api:subscription_plan_info:v1"
	subscriptionPlanSnapshotVersion    = 1
)

var (
	subscriptionPlanCacheOnce     sync.Once
	subscriptionPlanInfoCacheOnce sync.Once

	subscriptionPlanCache     *cachex.HybridCache[SubscriptionPlan]
	subscriptionPlanInfoCache *cachex.HybridCache[SubscriptionPlanInfo]
)

func subscriptionPlanCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_TTL", 300)
	return common.SafeIntervalDuration(
		ttlSeconds,
		time.Second,
		300*time.Second,
		"subscription plan cache",
	)
}

func subscriptionPlanInfoCacheTTL() time.Duration {
	ttlSeconds := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_TTL", 120)
	return common.SafeIntervalDuration(
		ttlSeconds,
		time.Second,
		120*time.Second,
		"subscription plan info cache",
	)
}

func subscriptionPlanCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_CACHE_CAP", 5000)
	if capacity <= 0 {
		capacity = 5000
	}
	return capacity
}

func subscriptionPlanInfoCacheCapacity() int {
	capacity := common.GetEnvOrDefault("SUBSCRIPTION_PLAN_INFO_CACHE_CAP", 10000)
	if capacity <= 0 {
		capacity = 10000
	}
	return capacity
}

func getSubscriptionPlanCache() *cachex.HybridCache[SubscriptionPlan] {
	subscriptionPlanCacheOnce.Do(func() {
		ttl := subscriptionPlanCacheTTL()
		subscriptionPlanCache = cachex.NewHybridCache[SubscriptionPlan](cachex.HybridCacheConfig[SubscriptionPlan]{
			Namespace: cachex.Namespace(subscriptionPlanCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlan]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlan] {
				return hot.NewHotCache[string, SubscriptionPlan](hot.LRU, subscriptionPlanCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanCache
}

func getSubscriptionPlanInfoCache() *cachex.HybridCache[SubscriptionPlanInfo] {
	subscriptionPlanInfoCacheOnce.Do(func() {
		ttl := subscriptionPlanInfoCacheTTL()
		subscriptionPlanInfoCache = cachex.NewHybridCache[SubscriptionPlanInfo](cachex.HybridCacheConfig[SubscriptionPlanInfo]{
			Namespace: cachex.Namespace(subscriptionPlanInfoCacheNamespace),
			Redis:     common.RDB,
			RedisEnabled: func() bool {
				return common.RedisEnabled && common.RDB != nil
			},
			RedisCodec: cachex.JSONCodec[SubscriptionPlanInfo]{},
			Memory: func() *hot.HotCache[string, SubscriptionPlanInfo] {
				return hot.NewHotCache[string, SubscriptionPlanInfo](hot.LRU, subscriptionPlanInfoCacheCapacity()).
					WithTTL(ttl).
					WithJanitor().
					Build()
			},
		})
	})
	return subscriptionPlanInfoCache
}

func subscriptionPlanCacheKey(id int) string {
	if id <= 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func InvalidateSubscriptionPlanCache(planId int) {
	if planId <= 0 {
		return
	}
	cache := getSubscriptionPlanCache()
	_, _ = cache.DeleteMany([]string{subscriptionPlanCacheKey(planId)})
	infoCache := getSubscriptionPlanInfoCache()
	_ = infoCache.Purge()
}

// Subscription plan
type SubscriptionPlan struct {
	Id int `json:"id"`

	Title    string `json:"title" gorm:"type:varchar(128);not null"`
	Subtitle string `json:"subtitle" gorm:"type:varchar(255);default:''"`

	// Display money amount (follow existing code style: float64 for money)
	PriceAmount float64 `json:"price_amount" gorm:"type:decimal(10,6);not null;default:0"`
	Currency    string  `json:"currency" gorm:"type:varchar(8);not null;default:'USD'"`

	DurationUnit  string `json:"duration_unit" gorm:"type:varchar(16);not null;default:'month'"`
	DurationValue int    `json:"duration_value" gorm:"type:int;not null;default:1"`
	CustomSeconds int64  `json:"custom_seconds" gorm:"type:bigint;not null;default:0"`

	Enabled   bool `json:"enabled"`
	SortOrder int  `json:"sort_order" gorm:"type:int;default:0"`

	AllowBalancePay *bool `json:"allow_balance_pay"`

	// Allow falling back to wallet balance after subscription quota is exhausted (empty = true)
	AllowWalletOverflow *bool `json:"allow_wallet_overflow"`

	StripePriceId         string `json:"stripe_price_id" gorm:"type:varchar(128);default:''"`
	CreemProductId        string `json:"creem_product_id" gorm:"type:varchar(128);default:''"`
	WaffoPancakeProductId string `json:"waffo_pancake_product_id" gorm:"type:varchar(128);default:''"`

	// Max purchases per user (0 = unlimited)
	MaxPurchasePerUser int `json:"max_purchase_per_user" gorm:"type:int;default:0"`

	// Upgrade user group after purchase (empty = no change)
	UpgradeGroup string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`

	// Downgrade user group on expiry (empty = revert to the group held before purchase)
	DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`

	// Total quota (amount in quota units, 0 = unlimited)
	TotalAmount int64 `json:"total_amount" gorm:"type:bigint;not null;default:0"`

	// Quota reset period for plan
	QuotaResetPeriod        string `json:"quota_reset_period" gorm:"type:varchar(16);default:'never'"`
	QuotaResetCustomSeconds int64  `json:"quota_reset_custom_seconds" gorm:"type:bigint;default:0"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

// UnmarshalJSON applies the public API default without encoding it as a
// database schema default. Explicit false values remain false.
func (p *SubscriptionPlan) UnmarshalJSON(data []byte) error {
	type subscriptionPlanAlias SubscriptionPlan
	decoded := subscriptionPlanAlias{Enabled: true}
	if err := common.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*p = SubscriptionPlan(decoded)
	return nil
}

func (p *SubscriptionPlan) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	p.CreatedAt = now
	p.UpdatedAt = now
	return nil
}

func (p *SubscriptionPlan) BeforeUpdate(tx *gorm.DB) error {
	p.UpdatedAt = common.GetTimestamp()
	return nil
}

func (p *SubscriptionPlan) NormalizeDefaults() {
	if p.AllowBalancePay == nil {
		p.AllowBalancePay = common.GetPointer(true)
	}
	if p.AllowWalletOverflow == nil {
		p.AllowWalletOverflow = common.GetPointer(true)
	}
}

// subscriptionPlanSnapshot stores the paid entitlement terms independently
// from the mutable catalog plan. Provider product identifiers are retained so
// delayed webhooks can be verified against the checkout that was actually
// created rather than the current catalog entry.
type subscriptionPlanSnapshot struct {
	Version                 int     `json:"version"`
	Checksum                string  `json:"checksum"`
	PlanId                  int     `json:"plan_id"`
	Title                   string  `json:"title"`
	PriceAmount             float64 `json:"price_amount"`
	Currency                string  `json:"currency"`
	DurationUnit            string  `json:"duration_unit"`
	DurationValue           int     `json:"duration_value"`
	CustomSeconds           int64   `json:"custom_seconds"`
	AllowBalancePay         bool    `json:"allow_balance_pay"`
	AllowWalletOverflow     bool    `json:"allow_wallet_overflow"`
	StripePriceId           string  `json:"stripe_price_id"`
	CreemProductId          string  `json:"creem_product_id"`
	WaffoPancakeProductId   string  `json:"waffo_pancake_product_id"`
	MaxPurchasePerUser      int     `json:"max_purchase_per_user"`
	UpgradeGroup            string  `json:"upgrade_group"`
	DowngradeGroup          string  `json:"downgrade_group"`
	TotalAmount             int64   `json:"total_amount"`
	QuotaResetPeriod        string  `json:"quota_reset_period"`
	QuotaResetCustomSeconds int64   `json:"quota_reset_custom_seconds"`
}

func subscriptionPlanSnapshotChecksum(snapshot subscriptionPlanSnapshot) (string, error) {
	snapshot.Checksum = ""
	data, err := common.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", common.Sha256Raw(data)), nil
}

// Subscription order (payment -> webhook -> create UserSubscription)
type SubscriptionOrder struct {
	Id     int     `json:"id"`
	UserId int     `json:"user_id" gorm:"index"`
	PlanId int     `json:"plan_id" gorm:"index"`
	Money  float64 `json:"money"`
	// PlanSnapshot is private persisted checkout evidence. It is copied to
	// recurring invoice orders so renewals keep the original paid terms.
	PlanSnapshot string `json:"-" gorm:"type:text"`

	TradeNo         string  `json:"trade_no" gorm:"type:varchar(255)"`
	TradeNoHash     *string `json:"-" gorm:"type:char(64);uniqueIndex:ux_subscription_orders_trade_no_hash"`
	PaymentMethod   string  `json:"payment_method" gorm:"type:varchar(50)"`
	PaymentProvider string  `json:"payment_provider" gorm:"type:varchar(50);default:''"`
	Status          string  `json:"status"`
	CreateTime      int64   `json:"create_time"`
	CompleteTime    int64   `json:"complete_time"`

	ProviderPayload          string  `json:"provider_payload" gorm:"type:text"`
	ProviderCustomerId       string  `json:"provider_customer_id" gorm:"type:varchar(255)"`
	ProviderSubscriptionId   string  `json:"provider_subscription_id" gorm:"type:varchar(255)"`
	ProviderSubscriptionHash *string `json:"-" gorm:"type:char(64);index:idx_subscription_orders_provider_subscription_hash"`
	ProviderPaymentId        string  `json:"provider_payment_id" gorm:"type:varchar(255)"`
	ProviderStatus           string  `json:"provider_status" gorm:"type:varchar(64);index"`
	// ProviderEventTime orders lifecycle changes from providers whose webhook
	// deliveries may be delayed or reordered.
	ProviderEventTime int64 `json:"provider_event_time" gorm:"type:bigint;not null;default:0"`
	// These fields retain exact, idempotent refund progress for indivisible
	// subscription entitlements.
	ProviderPaidAmountMinor     int64  `json:"provider_paid_amount_minor" gorm:"type:bigint;not null;default:0"`
	ProviderRefundedAmountMinor int64  `json:"provider_refunded_amount_minor" gorm:"type:bigint;not null;default:0"`
	ProviderRefundEvents        string `json:"-" gorm:"type:text"`
}

func validateSubscriptionPlanSnapshot(plan *SubscriptionPlan) error {
	if plan == nil {
		return errors.New("plan is nil")
	}
	if plan.PriceAmount < 0 || plan.PriceAmount > 9999 ||
		math.IsNaN(plan.PriceAmount) || math.IsInf(plan.PriceAmount, 0) {
		return errors.New("snapshot plan price is invalid")
	}
	if err := validateSubscriptionPlanDuration(plan); err != nil {
		return err
	}
	if plan.Id <= 0 {
		return errors.New("snapshot plan id is invalid")
	}
	if strings.TrimSpace(plan.Title) == "" ||
		!utf8.ValidString(plan.Title) ||
		utf8.RuneCountInString(plan.Title) > 128 {
		return errors.New("snapshot plan title is invalid")
	}
	if strings.TrimSpace(plan.Currency) == "" || len(plan.Currency) > 8 {
		return errors.New("snapshot plan currency is invalid")
	}
	if len(plan.StripePriceId) > 128 ||
		len(plan.CreemProductId) > 128 ||
		len(plan.WaffoPancakeProductId) > 128 {
		return errors.New("snapshot provider product identifier is invalid")
	}
	if plan.MaxPurchasePerUser < 0 {
		return errors.New("snapshot purchase limit is invalid")
	}
	if plan.TotalAmount < 0 {
		return errors.New("snapshot total amount is invalid")
	}
	if !utf8.ValidString(plan.UpgradeGroup) ||
		!utf8.ValidString(plan.DowngradeGroup) ||
		utf8.RuneCountInString(strings.TrimSpace(plan.UpgradeGroup)) > 64 ||
		utf8.RuneCountInString(strings.TrimSpace(plan.DowngradeGroup)) > 64 {
		return errors.New("snapshot subscription group is invalid")
	}
	if err := validateSubscriptionPlanResetPeriod(plan); err != nil {
		return fmt.Errorf("invalid snapshot quota reset period: %w", err)
	}
	return nil
}

func marshalSubscriptionPlanSnapshot(plan *SubscriptionPlan) (string, error) {
	if plan == nil {
		return "", errors.New("plan is nil")
	}
	planCopy := *plan
	planCopy.NormalizeDefaults()
	if err := validateSubscriptionPlanSnapshot(&planCopy); err != nil {
		return "", err
	}
	snapshot := subscriptionPlanSnapshot{
		Version:                 subscriptionPlanSnapshotVersion,
		PlanId:                  planCopy.Id,
		Title:                   planCopy.Title,
		PriceAmount:             planCopy.PriceAmount,
		Currency:                planCopy.Currency,
		DurationUnit:            planCopy.DurationUnit,
		DurationValue:           planCopy.DurationValue,
		CustomSeconds:           planCopy.CustomSeconds,
		AllowBalancePay:         *planCopy.AllowBalancePay,
		AllowWalletOverflow:     *planCopy.AllowWalletOverflow,
		StripePriceId:           planCopy.StripePriceId,
		CreemProductId:          planCopy.CreemProductId,
		WaffoPancakeProductId:   planCopy.WaffoPancakeProductId,
		MaxPurchasePerUser:      planCopy.MaxPurchasePerUser,
		UpgradeGroup:            strings.TrimSpace(planCopy.UpgradeGroup),
		DowngradeGroup:          strings.TrimSpace(planCopy.DowngradeGroup),
		TotalAmount:             planCopy.TotalAmount,
		QuotaResetPeriod:        strings.TrimSpace(planCopy.QuotaResetPeriod),
		QuotaResetCustomSeconds: planCopy.QuotaResetCustomSeconds,
	}
	checksum, err := subscriptionPlanSnapshotChecksum(snapshot)
	if err != nil {
		return "", err
	}
	snapshot.Checksum = checksum
	data, err := common.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// SetPlanSnapshot records the exact plan used to construct a checkout.
func (o *SubscriptionOrder) SetPlanSnapshot(plan *SubscriptionPlan) error {
	if o == nil {
		return errors.New("subscription order is nil")
	}
	if plan == nil || o.PlanId <= 0 || plan.Id != o.PlanId {
		return errors.New("subscription order plan does not match snapshot")
	}
	snapshot, err := marshalSubscriptionPlanSnapshot(plan)
	if err != nil {
		return err
	}
	o.PlanSnapshot = snapshot
	return nil
}

func subscriptionPlanFromOrderTx(tx *gorm.DB, order *SubscriptionOrder) (*SubscriptionPlan, error) {
	if tx == nil || order == nil || order.PlanId <= 0 {
		return nil, errors.New("invalid subscription order")
	}
	if order.PlanSnapshot == "" {
		// Legacy orders predate durable plan snapshots. Read the database in
		// this transaction rather than consulting the process/Redis cache.
		return getSubscriptionPlanByIdTx(tx, order.PlanId)
	}
	var snapshot subscriptionPlanSnapshot
	if err := common.UnmarshalJsonStr(order.PlanSnapshot, &snapshot); err != nil {
		return nil, fmt.Errorf("decode subscription plan snapshot: %w", err)
	}
	if snapshot.Version != subscriptionPlanSnapshotVersion {
		return nil, errors.New("unsupported subscription plan snapshot version")
	}
	checksum, err := subscriptionPlanSnapshotChecksum(snapshot)
	if err != nil {
		return nil, err
	}
	if len(snapshot.Checksum) != 64 || snapshot.Checksum != checksum {
		return nil, errors.New("subscription plan snapshot checksum mismatch")
	}
	if snapshot.PlanId != order.PlanId {
		return nil, errors.New("subscription order plan snapshot mismatch")
	}
	allowBalancePay := snapshot.AllowBalancePay
	allowWalletOverflow := snapshot.AllowWalletOverflow
	plan := &SubscriptionPlan{
		Id:                      snapshot.PlanId,
		Title:                   snapshot.Title,
		PriceAmount:             snapshot.PriceAmount,
		Currency:                snapshot.Currency,
		DurationUnit:            snapshot.DurationUnit,
		DurationValue:           snapshot.DurationValue,
		CustomSeconds:           snapshot.CustomSeconds,
		Enabled:                 true,
		AllowBalancePay:         &allowBalancePay,
		AllowWalletOverflow:     &allowWalletOverflow,
		StripePriceId:           snapshot.StripePriceId,
		CreemProductId:          snapshot.CreemProductId,
		WaffoPancakeProductId:   snapshot.WaffoPancakeProductId,
		MaxPurchasePerUser:      snapshot.MaxPurchasePerUser,
		UpgradeGroup:            snapshot.UpgradeGroup,
		DowngradeGroup:          snapshot.DowngradeGroup,
		TotalAmount:             snapshot.TotalAmount,
		QuotaResetPeriod:        snapshot.QuotaResetPeriod,
		QuotaResetCustomSeconds: snapshot.QuotaResetCustomSeconds,
	}
	if err := validateSubscriptionPlanSnapshot(plan); err != nil {
		return nil, fmt.Errorf("invalid subscription plan snapshot: %w", err)
	}
	return plan, nil
}

// GetSubscriptionOrderPlan resolves the immutable checkout snapshot. Legacy
// orders without a snapshot fall back to a fresh database read.
func GetSubscriptionOrderPlan(order *SubscriptionOrder) (*SubscriptionPlan, error) {
	return subscriptionPlanFromOrderTx(DB, order)
}

// SubscriptionProviderPayment maps every provider payment object attached to
// an invoice back to its local subscription order. Modern Stripe invoices can
// contain multiple InvoicePayments, so SubscriptionOrder.ProviderPaymentId is
// retained only as the primary/legacy identifier.
type SubscriptionProviderPayment struct {
	Id                  int     `json:"id"`
	PaymentProvider     string  `json:"payment_provider" gorm:"type:varchar(50);not null"`
	ProviderPaymentId   string  `json:"provider_payment_id" gorm:"type:varchar(255);not null"`
	PaymentIdentityHash *string `json:"-" gorm:"type:char(64);uniqueIndex:ux_subscription_provider_payment_hash"`
	SubscriptionOrderId int     `json:"subscription_order_id" gorm:"not null;index"`
	CreatedAt           int64   `json:"created_at" gorm:"type:bigint;not null"`
}

func (p *SubscriptionProviderPayment) BeforeCreate(tx *gorm.DB) error {
	paymentProvider, providerPaymentID, paymentIdentityHash, err :=
		normalizeProviderPaymentIdentity(p.PaymentProvider, p.ProviderPaymentId)
	if err != nil {
		return err
	}
	p.PaymentProvider = paymentProvider
	p.ProviderPaymentId = providerPaymentID
	p.PaymentIdentityHash = &paymentIdentityHash
	if p.CreatedAt == 0 {
		p.CreatedAt = common.GetTimestamp()
	}
	return nil
}

func (o *SubscriptionOrder) normalizeIdentities() error {
	tradeNo, tradeNoHash, err := normalizeTradeNumberIdentity(o.TradeNo)
	if err != nil {
		return err
	}
	o.TradeNo = tradeNo
	o.TradeNoHash = &tradeNoHash
	if o.ProviderSubscriptionId == "" {
		o.ProviderSubscriptionHash = nil
		return nil
	}
	paymentProvider, providerSubscriptionID, providerSubscriptionHash, err :=
		normalizeProviderSubscriptionIdentity(
			o.PaymentProvider,
			o.ProviderSubscriptionId,
		)
	if err != nil {
		return err
	}
	o.PaymentProvider = paymentProvider
	o.ProviderSubscriptionId = providerSubscriptionID
	o.ProviderSubscriptionHash = &providerSubscriptionHash
	return nil
}

func (o *SubscriptionOrder) BeforeCreate(_ *gorm.DB) error {
	return o.normalizeIdentities()
}

func (o *SubscriptionOrder) BeforeUpdate(tx *gorm.DB) error {
	if o.Id <= 0 {
		return nil
	}
	if err := o.normalizeIdentities(); err != nil {
		return err
	}
	tx.Statement.SetColumn("trade_no", o.TradeNo)
	tx.Statement.SetColumn("trade_no_hash", o.TradeNoHash)
	tx.Statement.SetColumn("payment_provider", o.PaymentProvider)
	tx.Statement.SetColumn("provider_subscription_id", o.ProviderSubscriptionId)
	tx.Statement.SetColumn("provider_subscription_hash", o.ProviderSubscriptionHash)
	return nil
}

func (o *SubscriptionOrder) Insert() error {
	if o.CreateTime == 0 {
		o.CreateTime = common.GetTimestamp()
	}
	return DB.Create(o).Error
}

func (o *SubscriptionOrder) Update() error {
	return DB.Save(o).Error
}

func GetSubscriptionOrderByTradeNo(tradeNo string) *SubscriptionOrder {
	order, _ := FindSubscriptionOrderByTradeNo(tradeNo)
	return order
}

// FindSubscriptionOrderByTradeNo preserves database failures for webhook
// callers that need to request a provider retry on transient storage errors.
func FindSubscriptionOrderByTradeNo(tradeNo string) (*SubscriptionOrder, error) {
	var order SubscriptionOrder
	if err := findSubscriptionOrderByTradeNo(DB, tradeNo, &order); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrSubscriptionOrderNotFound
		}
		return nil, err
	}
	return &order, nil
}

func findSubscriptionOrderByTradeNo(
	db *gorm.DB,
	tradeNo string,
	order *SubscriptionOrder,
) error {
	if db == nil || order == nil {
		return errors.New("subscription order lookup is invalid")
	}
	tradeNo, tradeNoHash, err := normalizeTradeNumberIdentity(tradeNo)
	if err != nil {
		return err
	}
	if err := db.Where("trade_no_hash = ?", tradeNoHash).First(order).Error; err != nil {
		return err
	}
	if order.TradeNo != tradeNo {
		return errors.New("trade number identity hash collision")
	}
	return nil
}

func GetSubscriptionOrderByProviderSubscription(paymentProvider string, providerSubscriptionID string) *SubscriptionOrder {
	order, _ := FindSubscriptionOrderByProviderSubscription(paymentProvider, providerSubscriptionID)
	return order
}

// FindSubscriptionOrderByProviderSubscription is the error-preserving
// counterpart used while processing recurring provider events.
func FindSubscriptionOrderByProviderSubscription(
	paymentProvider string,
	providerSubscriptionID string,
) (*SubscriptionOrder, error) {
	orders, err := findProviderSubscriptionOrders(
		DB,
		paymentProvider,
		providerSubscriptionID,
	)
	if err != nil {
		return nil, err
	}
	return &orders[len(orders)-1], nil
}

func findProviderSubscriptionOrders(
	db *gorm.DB,
	paymentProvider string,
	providerSubscriptionID string,
) ([]SubscriptionOrder, error) {
	if db == nil {
		return nil, errors.New("subscription order lookup database is nil")
	}
	paymentProvider, providerSubscriptionID, providerSubscriptionHash, err :=
		normalizeProviderSubscriptionIdentity(paymentProvider, providerSubscriptionID)
	if err != nil {
		return nil, err
	}
	var orders []SubscriptionOrder
	if err := db.Where("provider_subscription_hash = ?", providerSubscriptionHash).
		Order("id asc").
		Find(&orders).Error; err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, ErrSubscriptionOrderNotFound
	}
	for i := range orders {
		if orders[i].PaymentProvider != paymentProvider ||
			orders[i].ProviderSubscriptionId != providerSubscriptionID {
			return nil, errors.New("provider subscription identity hash collision")
		}
	}
	return orders, nil
}

func BindSubscriptionProviderPayments(paymentProvider string, providerSubscriptionID string, providerInvoiceID string, providerPaymentIDs []string) error {
	if paymentProvider == "" || providerSubscriptionID == "" || len(providerPaymentIDs) == 0 {
		return errors.New("missing subscription provider payment identifiers")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		orders, err := lockProviderSubscriptionOrdersTx(tx, paymentProvider, providerSubscriptionID)
		if err != nil {
			return err
		}
		targetOrderID := orders[0].Id
		if providerInvoiceID != "" {
			tradeNo := paymentProvider + "_invoice_" + providerInvoiceID
			targetOrderID = 0
			for i := range orders {
				if orders[i].TradeNo == tradeNo {
					targetOrderID = orders[i].Id
					break
				}
			}
			if targetOrderID == 0 {
				return ErrSubscriptionOrderNotFound
			}
		}
		return bindSubscriptionProviderPaymentsTx(
			tx,
			paymentProvider,
			targetOrderID,
			providerPaymentIDs,
		)
	})
}

func bindSubscriptionProviderPaymentsTx(tx *gorm.DB, paymentProvider string, subscriptionOrderID int, providerPaymentIDs []string) error {
	if tx == nil || paymentProvider == "" || subscriptionOrderID <= 0 || len(providerPaymentIDs) == 0 {
		return errors.New("missing subscription provider payment identifiers")
	}
	if len(providerPaymentIDs) > 100 {
		return errors.New("too many subscription provider payment identifiers")
	}
	seen := make(map[string]struct{}, len(providerPaymentIDs))
	for _, providerPaymentID := range providerPaymentIDs {
		normalizedProvider, normalizedPaymentID, paymentIdentityHash, err :=
			normalizeProviderPaymentIdentity(paymentProvider, providerPaymentID)
		if err != nil {
			return err
		}
		if _, exists := seen[paymentIdentityHash]; exists {
			continue
		}
		seen[paymentIdentityHash] = struct{}{}
		reference := &SubscriptionProviderPayment{
			PaymentProvider:     normalizedProvider,
			ProviderPaymentId:   normalizedPaymentID,
			SubscriptionOrderId: subscriptionOrderID,
		}
		if err := tx.Clauses(clause.OnConflict{
			Columns: []clause.Column{
				{Name: "payment_identity_hash"},
			},
			DoNothing: true,
		}).Create(reference).Error; err != nil {
			return err
		}
		var persisted SubscriptionProviderPayment
		if err := tx.Where("payment_identity_hash = ?", paymentIdentityHash).
			First(&persisted).Error; err != nil {
			return err
		}
		if persisted.PaymentProvider != normalizedProvider ||
			persisted.ProviderPaymentId != normalizedPaymentID {
			return errors.New("provider payment identity hash collision")
		}
		if persisted.SubscriptionOrderId != subscriptionOrderID {
			return errors.New("provider payment is already bound to another subscription order")
		}
	}
	return nil
}

func findSubscriptionOrderByProviderPaymentTx(
	tx *gorm.DB,
	paymentProvider string,
	providerPaymentID string,
) (*SubscriptionOrder, error) {
	if tx == nil {
		return nil, errors.New("subscription provider payment database is nil")
	}
	paymentProvider, providerPaymentID, paymentIdentityHash, err :=
		normalizeProviderPaymentIdentity(paymentProvider, providerPaymentID)
	if err != nil {
		return nil, err
	}
	var reference SubscriptionProviderPayment
	if err := tx.Where("payment_identity_hash = ?", paymentIdentityHash).
		First(&reference).Error; err != nil {
		return nil, err
	}
	if reference.PaymentProvider != paymentProvider ||
		reference.ProviderPaymentId != providerPaymentID {
		return nil, errors.New("provider payment identity hash collision")
	}
	var order SubscriptionOrder
	if err := tx.Where("id = ?", reference.SubscriptionOrderId).First(&order).Error; err != nil {
		return nil, err
	}
	if order.PaymentProvider != paymentProvider {
		return nil, ErrPaymentMethodMismatch
	}
	return &order, nil
}

// User subscription instance
type UserSubscription struct {
	Id     int `json:"id"`
	UserId int `json:"user_id" gorm:"index;index:idx_user_sub_active,priority:1"`
	PlanId int `json:"plan_id" gorm:"index"`

	AmountTotal int64 `json:"amount_total" gorm:"type:bigint;not null;default:0"`
	AmountUsed  int64 `json:"amount_used" gorm:"type:bigint;not null;default:0"`

	StartTime int64  `json:"start_time" gorm:"bigint"`
	EndTime   int64  `json:"end_time" gorm:"bigint;index;index:idx_user_sub_active,priority:3"`
	Status    string `json:"status" gorm:"type:varchar(32);index;index:idx_user_sub_active,priority:2"` // active/expired/cancelled

	Source                   string  `json:"source" gorm:"type:varchar(32);default:'order'"` // order/admin
	SubscriptionOrderId      int     `json:"subscription_order_id" gorm:"index"`
	PaymentProvider          string  `json:"payment_provider" gorm:"type:varchar(50);index"`
	ProviderSubscriptionId   string  `json:"provider_subscription_id" gorm:"type:varchar(255)"`
	ProviderSubscriptionHash *string `json:"-" gorm:"type:char(64);index:idx_user_subscriptions_provider_subscription_hash"`
	ProviderEventTime        int64   `json:"provider_event_time" gorm:"type:bigint;not null;default:0"`

	LastResetTime int64 `json:"last_reset_time" gorm:"type:bigint;default:0"`
	NextResetTime int64 `json:"next_reset_time" gorm:"type:bigint;default:0;index"`
	// QuotaResetVersion is an internal monotonic generation used to keep late
	// asynchronous billing reversals from decrementing usage created after any
	// scheduled, renewal, or manual reset. It is separate from LastResetTime
	// because a manual reset may intentionally preserve the plan's schedule.
	QuotaResetVersion int64 `json:"-" gorm:"type:bigint"`

	UpgradeGroup  string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`
	PrevUserGroup string `json:"prev_user_group" gorm:"type:varchar(64);default:''"`

	// Downgrade target group on expiry (snapshot from plan; empty = revert to PrevUserGroup)
	DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`

	// Whether wallet fallback is allowed after this subscription's quota is exhausted (snapshot from plan)
	AllowWalletOverflow bool `json:"allow_wallet_overflow"`

	CreatedAt int64 `json:"created_at" gorm:"bigint"`
	UpdatedAt int64 `json:"updated_at" gorm:"bigint"`
}

func (s *UserSubscription) BeforeCreate(tx *gorm.DB) error {
	if err := s.normalizeProviderSubscriptionIdentity(); err != nil {
		return err
	}
	now := common.GetTimestamp()
	s.CreatedAt = now
	s.UpdatedAt = now
	return nil
}

func (s *UserSubscription) BeforeUpdate(tx *gorm.DB) error {
	if s.Id > 0 {
		if err := s.normalizeProviderSubscriptionIdentity(); err != nil {
			return err
		}
		tx.Statement.SetColumn("payment_provider", s.PaymentProvider)
		tx.Statement.SetColumn("provider_subscription_id", s.ProviderSubscriptionId)
		tx.Statement.SetColumn("provider_subscription_hash", s.ProviderSubscriptionHash)
	}
	s.UpdatedAt = common.GetTimestamp()
	return nil
}

func (s *UserSubscription) normalizeProviderSubscriptionIdentity() error {
	if s.ProviderSubscriptionId == "" {
		s.ProviderSubscriptionHash = nil
		return nil
	}
	paymentProvider, providerSubscriptionID, providerSubscriptionHash, err :=
		normalizeProviderSubscriptionIdentity(
			s.PaymentProvider,
			s.ProviderSubscriptionId,
		)
	if err != nil {
		return err
	}
	s.PaymentProvider = paymentProvider
	s.ProviderSubscriptionId = providerSubscriptionID
	s.ProviderSubscriptionHash = &providerSubscriptionHash
	return nil
}

type SubscriptionSummary struct {
	Subscription *UserSubscription `json:"subscription"`
}

type SubscriptionResetResult struct {
	PlanId           int    `json:"plan_id"`
	MatchedCount     int    `json:"matched_count"`
	ResetCount       int    `json:"reset_count"`
	UserCount        int    `json:"user_count"`
	AdvanceResetTime bool   `json:"advance_reset_time"`
	PlanTitle        string `json:"-"`
	AffectedUserIds  []int  `json:"-"`
}

func calcPlanEndTime(start time.Time, plan *SubscriptionPlan) (int64, error) {
	if plan == nil {
		return 0, errors.New("plan is nil")
	}
	if err := validateSubscriptionPlanDuration(plan); err != nil {
		return 0, err
	}
	switch plan.DurationUnit {
	case SubscriptionDurationYear:
		return start.AddDate(plan.DurationValue, 0, 0).Unix(), nil
	case SubscriptionDurationMonth:
		return start.AddDate(0, plan.DurationValue, 0).Unix(), nil
	case SubscriptionDurationDay:
		return start.Add(time.Duration(plan.DurationValue) * 24 * time.Hour).Unix(), nil
	case SubscriptionDurationHour:
		return start.Add(time.Duration(plan.DurationValue) * time.Hour).Unix(), nil
	case SubscriptionDurationCustom:
		if plan.CustomSeconds <= 0 {
			return 0, errors.New("custom_seconds must be > 0")
		}
		return start.Add(time.Duration(plan.CustomSeconds) * time.Second).Unix(), nil
	default:
		return 0, fmt.Errorf("invalid duration_unit: %s", plan.DurationUnit)
	}
}

func validateSubscriptionPlanDuration(plan *SubscriptionPlan) error {
	if plan == nil {
		return errors.New("plan is nil")
	}
	switch plan.DurationUnit {
	case SubscriptionDurationYear:
		if plan.DurationValue <= 0 || plan.DurationValue > maxSubscriptionPlanYears {
			return errors.New("duration_value is out of range for years")
		}
	case SubscriptionDurationMonth:
		if plan.DurationValue <= 0 || plan.DurationValue > maxSubscriptionPlanYears*12 {
			return errors.New("duration_value is out of range for months")
		}
	case SubscriptionDurationDay:
		if plan.DurationValue <= 0 || plan.DurationValue > maxSubscriptionPlanYears*366 {
			return errors.New("duration_value is out of range for days")
		}
	case SubscriptionDurationHour:
		if plan.DurationValue <= 0 || plan.DurationValue > maxSubscriptionPlanYears*366*24 {
			return errors.New("duration_value is out of range for hours")
		}
	case SubscriptionDurationCustom:
		if plan.CustomSeconds <= 0 || plan.CustomSeconds > maxSubscriptionResetSecond {
			return errors.New("custom_seconds is out of range")
		}
	default:
		return fmt.Errorf("invalid duration_unit: %s", plan.DurationUnit)
	}
	return nil
}

// ValidateSubscriptionPlanForPurchase rejects plans that cannot produce a
// bounded entitlement before an external checkout is created.
func ValidateSubscriptionPlanForPurchase(plan *SubscriptionPlan) error {
	if plan == nil {
		return errors.New("plan is nil")
	}
	if plan.PriceAmount < 0 || math.IsNaN(plan.PriceAmount) || math.IsInf(plan.PriceAmount, 0) {
		return errors.New("price_amount is invalid")
	}
	if plan.AllowBalancePay == nil || *plan.AllowBalancePay {
		if _, err := calcSubscriptionBalanceQuota(plan.PriceAmount); err != nil {
			return fmt.Errorf("balance price is not representable: %w", err)
		}
	}
	if err := validateSubscriptionPlanDuration(plan); err != nil {
		return err
	}
	return validateSubscriptionPlanResetPeriod(plan)
}

func validateSubscriptionPlanResetPeriod(plan *SubscriptionPlan) error {
	if plan == nil {
		return errors.New("plan is nil")
	}
	switch strings.TrimSpace(plan.QuotaResetPeriod) {
	case "", SubscriptionResetNever, SubscriptionResetDaily, SubscriptionResetWeekly, SubscriptionResetMonthly:
		if plan.QuotaResetCustomSeconds < 0 || plan.QuotaResetCustomSeconds > maxSubscriptionResetSecond {
			return errors.New("quota_reset_custom_seconds is out of range")
		}
	case SubscriptionResetCustom:
		if plan.QuotaResetCustomSeconds <= 0 || plan.QuotaResetCustomSeconds > maxSubscriptionResetSecond {
			return errors.New("quota_reset_custom_seconds is out of range")
		}
	default:
		return errors.New("quota_reset_period is invalid")
	}
	return nil
}

func NormalizeResetPeriod(period string) string {
	switch strings.TrimSpace(period) {
	case SubscriptionResetDaily, SubscriptionResetWeekly, SubscriptionResetMonthly, SubscriptionResetCustom:
		return strings.TrimSpace(period)
	default:
		return SubscriptionResetNever
	}
}

func calcNextResetTime(base time.Time, plan *SubscriptionPlan, endUnix int64) int64 {
	if plan == nil {
		return 0
	}
	period := NormalizeResetPeriod(plan.QuotaResetPeriod)
	if period == SubscriptionResetNever {
		return 0
	}
	var next time.Time
	switch period {
	case SubscriptionResetDaily:
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location()).
			AddDate(0, 0, 1)
	case SubscriptionResetWeekly:
		// Align to next Monday 00:00
		weekday := int(base.Weekday()) // Sunday=0
		// Convert to Monday=1..Sunday=7
		if weekday == 0 {
			weekday = 7
		}
		daysUntil := 8 - weekday
		next = time.Date(base.Year(), base.Month(), base.Day(), 0, 0, 0, 0, base.Location()).
			AddDate(0, 0, daysUntil)
	case SubscriptionResetMonthly:
		// Align to first day of next month 00:00
		next = time.Date(base.Year(), base.Month(), 1, 0, 0, 0, 0, base.Location()).
			AddDate(0, 1, 0)
	case SubscriptionResetCustom:
		if plan.QuotaResetCustomSeconds <= 0 ||
			plan.QuotaResetCustomSeconds > maxSubscriptionResetSecond {
			return 0
		}
		duration, ok := common.SafeOptionalDuration64(
			plan.QuotaResetCustomSeconds,
			time.Second,
			"subscription quota reset",
		)
		if !ok {
			return 0
		}
		next = base.Add(duration)
	default:
		return 0
	}
	if endUnix > 0 && next.Unix() > endUnix {
		return 0
	}
	return next.Unix()
}

func GetSubscriptionPlanById(id int) (*SubscriptionPlan, error) {
	// Callers of the exported lookup use the plan to create a paid checkout or
	// grant an entitlement. Those decisions must observe the authoritative row:
	// cache invalidation can fail, and an in-memory cache on another node cannot
	// be invalidated when Redis is unavailable. Read through DB here while the
	// internal display-only plan-info lookup may still use the bounded cache.
	return getSubscriptionPlanByIdTx(DB, id)
}

func getSubscriptionPlanByIdTx(tx *gorm.DB, id int) (*SubscriptionPlan, error) {
	if id <= 0 {
		return nil, errors.New("invalid plan id")
	}
	if tx == nil {
		key := subscriptionPlanCacheKey(id)
		if key != "" {
			if cached, found, err := getSubscriptionPlanCache().Get(key); err == nil && found {
				cached.NormalizeDefaults()
				return &cached, nil
			}
		}
		var plan SubscriptionPlan
		if err := DB.Where("id = ?", id).First(&plan).Error; err != nil {
			return nil, err
		}
		plan.NormalizeDefaults()
		_ = getSubscriptionPlanCache().SetWithTTL(key, plan, subscriptionPlanCacheTTL())
		return &plan, nil
	}
	var plan SubscriptionPlan
	if err := tx.Where("id = ?", id).First(&plan).Error; err != nil {
		return nil, err
	}
	plan.NormalizeDefaults()
	return &plan, nil
}

func CountUserSubscriptionsByPlan(userId int, planId int) (int64, error) {
	if userId <= 0 || planId <= 0 {
		return 0, errors.New("invalid userId or planId")
	}
	var count int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND plan_id = ?", userId, planId).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func getUserGroupByIdTx(tx *gorm.DB, userId int) (string, error) {
	if userId <= 0 {
		return "", errors.New("invalid userId")
	}
	if tx == nil {
		tx = DB
	}
	var user User
	if err := tx.Select(commonGroupCol).Where("id = ?", userId).First(&user).Error; err != nil {
		return "", err
	}
	return user.Group, nil
}

func updateUserGroupTx(tx *gorm.DB, userId int, group string) error {
	if tx == nil || userId <= 0 {
		return errors.New("invalid user group update")
	}
	result := tx.Model(&User{}).Where("id = ?", userId).Update("group", group)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("user %d does not exist for subscription group update", userId)
	}
	return nil
}

func downgradeUserGroupForSubscriptionTx(tx *gorm.DB, sub *UserSubscription, now int64) (string, error) {
	if tx == nil || sub == nil {
		return "", errors.New("invalid downgrade args")
	}
	downgradeGroup := strings.TrimSpace(sub.DowngradeGroup)
	upgradeGroup := strings.TrimSpace(sub.UpgradeGroup)
	// Nothing to do if neither an explicit downgrade target nor an upgrade snapshot exists.
	if downgradeGroup == "" && upgradeGroup == "" {
		return "", nil
	}
	currentGroup, err := getUserGroupByIdTx(tx, sub.UserId)
	if err != nil {
		return "", err
	}
	// If another active upgraded subscription exists, restore the most recently
	// purchased surviving upgrade when the expiring subscription currently owns
	// the user's group. This matters when overlapping plans use different paid
	// groups (for example base -> vip -> pro).
	var activeSub UserSubscription
	activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND id <> ? AND upgrade_group <> ''",
		sub.UserId, "active", now, sub.Id).
		Order("start_time desc, id desc").
		Limit(1).
		Find(&activeSub)
	if activeQuery.Error == nil && activeQuery.RowsAffected > 0 {
		target := strings.TrimSpace(activeSub.UpgradeGroup)
		// A different baseline means a manual group override occurred between
		// the two purchases. Restore that manual group instead of reviving an
		// older paid upgrade which the administrator had already overridden.
		prevGroup := strings.TrimSpace(sub.PrevUserGroup)
		if prevGroup != "" && prevGroup != strings.TrimSpace(activeSub.PrevUserGroup) {
			target = prevGroup
		}
		if currentGroup != upgradeGroup || target == "" || target == currentGroup {
			return "", nil
		}
		if err := updateUserGroupTx(tx, sub.UserId, target); err != nil {
			return "", err
		}
		return target, nil
	}
	if activeQuery.Error != nil {
		return "", activeQuery.Error
	}
	// Determine the downgrade target: an explicit downgrade group takes precedence,
	// otherwise revert to the group held before purchase (legacy behavior).
	target := downgradeGroup
	if target == "" {
		// Legacy behavior: only revert when the subscription actually elevated the user.
		if currentGroup != upgradeGroup {
			return "", nil
		}
		target = strings.TrimSpace(sub.PrevUserGroup)
	}
	if target == "" || target == currentGroup {
		return "", nil
	}
	if err := updateUserGroupTx(tx, sub.UserId, target); err != nil {
		return "", err
	}
	return target, nil
}

func CreateUserSubscriptionFromPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, source string) (*UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("tx is nil")
	}
	if plan == nil || plan.Id == 0 {
		return nil, errors.New("invalid plan")
	}
	if userId <= 0 {
		return nil, errors.New("invalid user id")
	}
	// Serialize entitlement creation per user. This makes purchase limits and
	// the group transition snapshot deterministic even when separate provider
	// callbacks for the same user arrive concurrently.
	var user User
	if err := lockForUpdate(tx).
		Select("id", commonGroupCol).
		Where("id = ?", userId).
		First(&user).Error; err != nil {
		return nil, err
	}
	if plan.MaxPurchasePerUser > 0 {
		var count int64
		if err := tx.Model(&UserSubscription{}).
			Where("user_id = ? AND plan_id = ?", userId, plan.Id).
			Count(&count).Error; err != nil {
			return nil, err
		}
		if count >= int64(plan.MaxPurchasePerUser) {
			return nil, errors.New("已达到该套餐购买上限")
		}
	}
	nowUnix := getDBTimestampTx(tx)
	now := time.Unix(nowUnix, 0)
	endUnix, err := calcPlanEndTime(now, plan)
	if err != nil {
		return nil, err
	}
	resetBase := now
	nextReset := calcNextResetTime(resetBase, plan, endUnix)
	lastReset := int64(0)
	if nextReset > 0 {
		lastReset = now.Unix()
	}
	upgradeGroup := strings.TrimSpace(plan.UpgradeGroup)
	prevGroup := ""
	if upgradeGroup != "" {
		currentGroup := user.Group
		// Propagate the original baseline only when the current group is still
		// owned by an active paid upgrade. If an administrator changed the group
		// between purchases, that manual group becomes the restoration point.
		var existing UserSubscription
		result := tx.Where("user_id = ? AND status = ? AND end_time > ? AND upgrade_group = ? AND prev_user_group <> ''",
			userId, "active", nowUnix, currentGroup).
			Order("start_time desc, id desc").Limit(1).Find(&existing)
		if result.Error != nil {
			return nil, result.Error
		}
		if result.RowsAffected > 0 {
			prevGroup = existing.PrevUserGroup
		} else {
			prevGroup = currentGroup
		}
		if currentGroup != upgradeGroup {
			if err := updateUserGroupTx(tx, userId, upgradeGroup); err != nil {
				return nil, err
			}
		}
	}
	allowWalletOverflow := true
	if plan.AllowWalletOverflow != nil {
		allowWalletOverflow = *plan.AllowWalletOverflow
	}
	sub := &UserSubscription{
		UserId:              userId,
		PlanId:              plan.Id,
		AmountTotal:         plan.TotalAmount,
		AmountUsed:          0,
		StartTime:           now.Unix(),
		EndTime:             endUnix,
		Status:              "active",
		Source:              source,
		LastResetTime:       lastReset,
		NextResetTime:       nextReset,
		UpgradeGroup:        upgradeGroup,
		PrevUserGroup:       prevGroup,
		DowngradeGroup:      strings.TrimSpace(plan.DowngradeGroup),
		AllowWalletOverflow: allowWalletOverflow,
		CreatedAt:           common.GetTimestamp(),
		UpdatedAt:           common.GetTimestamp(),
	}
	if err := tx.Create(sub).Error; err != nil {
		return nil, err
	}
	return sub, nil
}

// Complete a subscription order (idempotent). Creates a UserSubscription snapshot from the plan.
// expectedPaymentProvider guards against cross-gateway callback attacks (empty skips the check).
// actualPaymentMethod updates the order's PaymentMethod to reflect the real payment type used (empty skips update).
func CompleteSubscriptionOrder(tradeNo string, providerPayload string, expectedPaymentProvider string, actualPaymentMethod string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	if expectedPaymentProvider != "" {
		var err error
		expectedPaymentProvider, err = normalizePaymentProvider(expectedPaymentProvider)
		if err != nil {
			return err
		}
	}
	var logUserId int
	var logPlanTitle string
	var logMoney float64
	var logPaymentMethod string
	var upgradeGroup string
	err := DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := findSubscriptionOrderByTradeNo(lockForUpdate(tx), tradeNo, &order); err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status == common.TopUpStatusSuccess {
			return nil
		}
		if order.Status != common.TopUpStatusPending {
			return ErrSubscriptionOrderStatusInvalid
		}
		plan, err := subscriptionPlanFromOrderTx(tx, &order)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			// still allow completion for already purchased orders
		}
		upgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
		sub, err := CreateUserSubscriptionFromPlanTx(tx, order.UserId, plan, "order")
		if err != nil {
			return err
		}
		sub.SubscriptionOrderId = order.Id
		sub.PaymentProvider = order.PaymentProvider
		sub.ProviderSubscriptionId = order.ProviderSubscriptionId
		sub.ProviderEventTime = order.ProviderEventTime
		if err := tx.Save(sub).Error; err != nil {
			return err
		}
		if err := upsertSubscriptionTopUpTx(tx, &order); err != nil {
			return err
		}
		order.Status = common.TopUpStatusSuccess
		order.CompleteTime = common.GetTimestamp()
		if providerPayload != "" {
			order.ProviderPayload = providerPayload
		}
		if actualPaymentMethod != "" && order.PaymentMethod != actualPaymentMethod {
			order.PaymentMethod = actualPaymentMethod
		}
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		logUserId = order.UserId
		logPlanTitle = plan.Title
		logMoney = order.Money
		logPaymentMethod = order.PaymentMethod
		return nil
	})
	if err != nil {
		return err
	}
	if upgradeGroup != "" && logUserId > 0 {
		_ = UpdateUserGroupCache(logUserId, upgradeGroup)
	}
	if logUserId > 0 {
		msg := fmt.Sprintf("订阅购买成功，套餐: %s，支付金额: %.2f，支付方式: %s", logPlanTitle, logMoney, logPaymentMethod)
		RecordLog(logUserId, LogTypeTopup, msg)
	}
	return nil
}

// SetSubscriptionOrderProviderIdentifiers binds verified provider identifiers
// before fulfillment. They are subsequently copied onto the entitlement and
// used to reconcile renewals, cancellation, payment failure and reversals.
func SetSubscriptionOrderProviderIdentifiers(tradeNo string, expectedPaymentProvider string, customerId string, subscriptionId string, paymentId string, providerStatus string, providerPayload string) error {
	return SetSubscriptionOrderProviderIdentifiersAt(
		tradeNo,
		expectedPaymentProvider,
		customerId,
		subscriptionId,
		paymentId,
		providerStatus,
		0,
		providerPayload,
	)
}

func SetSubscriptionOrderProviderIdentifiersAt(tradeNo string, expectedPaymentProvider string, customerId string, subscriptionId string, paymentId string, providerStatus string, providerEventTime int64, providerPayload string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	if providerEventTime < 0 {
		return errors.New("provider event time must not be negative")
	}
	if expectedPaymentProvider != "" {
		var err error
		expectedPaymentProvider, err = normalizePaymentProvider(expectedPaymentProvider)
		if err != nil {
			return err
		}
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := findSubscriptionOrderByTradeNo(lockForUpdate(tx), tradeNo, &order); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSubscriptionOrderNotFound
			}
			return err
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		updates := map[string]interface{}{}
		if customerId != "" {
			if order.ProviderCustomerId != "" && order.ProviderCustomerId != customerId {
				return ErrPaymentMethodMismatch
			}
			updates["provider_customer_id"] = customerId
		}
		if subscriptionId != "" {
			paymentProvider := order.PaymentProvider
			if expectedPaymentProvider != "" {
				paymentProvider = expectedPaymentProvider
			}
			normalizedProvider, normalizedSubscriptionID, subscriptionHash, err :=
				normalizeProviderSubscriptionIdentity(paymentProvider, subscriptionId)
			if err != nil {
				return err
			}
			if order.ProviderSubscriptionId != "" &&
				(order.PaymentProvider != normalizedProvider ||
					order.ProviderSubscriptionId != normalizedSubscriptionID) {
				return ErrPaymentMethodMismatch
			}
			updates["payment_provider"] = normalizedProvider
			updates["provider_subscription_id"] = normalizedSubscriptionID
			updates["provider_subscription_hash"] = subscriptionHash
		}
		if paymentId != "" {
			if order.ProviderPaymentId != "" && order.ProviderPaymentId != paymentId {
				return ErrPaymentMethodMismatch
			}
			updates["provider_payment_id"] = paymentId
		}
		lifecycleIsCurrent := providerSubscriptionLifecycleUpdateIsCurrent(
			order.ProviderEventTime,
			order.ProviderStatus,
			providerEventTime,
			providerStatus,
		)
		if lifecycleIsCurrent {
			if providerStatus != "" {
				updates["provider_status"] = providerStatus
			}
			if providerEventTime > 0 {
				updates["provider_event_time"] = providerEventTime
			}
			if providerPayload != "" {
				updates["provider_payload"] = providerPayload
			}
		}
		if len(updates) > 0 {
			// Identity updates are normalized above. Use a zero-value model so
			// the loaded order's pre-update hook cannot overwrite a newly bound
			// provider-subscription hash with its previous nil value.
			if err := tx.Model(&SubscriptionOrder{}).
				Where("id = ?", order.Id).
				Updates(updates).Error; err != nil {
				return err
			}
		}
		if paymentId != "" {
			paymentProvider := order.PaymentProvider
			if expectedPaymentProvider != "" {
				paymentProvider = expectedPaymentProvider
			}
			if err := bindSubscriptionProviderPaymentsTx(
				tx,
				paymentProvider,
				order.Id,
				[]string{paymentId},
			); err != nil {
				return err
			}
		}
		if expectedPaymentProvider == PaymentProviderStripe && customerId != "" {
			var user User
			if err := lockForUpdate(tx).Select("id").Where("id = ?", order.UserId).First(&user).Error; err != nil {
				return err
			}
			return tx.Model(&User{}).Where("id = ?", order.UserId).Update("stripe_customer", customerId).Error
		}
		return nil
	})
}

func lockProviderSubscriptionOrdersTx(tx *gorm.DB, paymentProvider string, providerSubscriptionId string) ([]SubscriptionOrder, error) {
	if tx == nil {
		return nil, errors.New("subscription order database is nil")
	}
	return findProviderSubscriptionOrders(
		lockForUpdate(tx),
		paymentProvider,
		providerSubscriptionId,
	)
}

func lockProviderUserSubscriptionsTx(
	tx *gorm.DB,
	paymentProvider string,
	providerSubscriptionID string,
) ([]UserSubscription, error) {
	if tx == nil {
		return nil, errors.New("user subscription database is nil")
	}
	paymentProvider, providerSubscriptionID, providerSubscriptionHash, err :=
		normalizeProviderSubscriptionIdentity(paymentProvider, providerSubscriptionID)
	if err != nil {
		return nil, err
	}
	var subscriptions []UserSubscription
	if err := lockForUpdate(tx).
		Where("provider_subscription_hash = ?", providerSubscriptionHash).
		Order("id asc").
		Find(&subscriptions).Error; err != nil {
		return nil, err
	}
	for i := range subscriptions {
		if subscriptions[i].PaymentProvider != paymentProvider ||
			subscriptions[i].ProviderSubscriptionId != providerSubscriptionID {
			return nil, errors.New("provider subscription identity hash collision")
		}
	}
	// Acquire every entitlement row in primary-key order, then restore the
	// newest-first business order expected by renewal/cancellation callers.
	sort.SliceStable(subscriptions, func(i, j int) bool {
		if subscriptions[i].EndTime != subscriptions[j].EndTime {
			return subscriptions[i].EndTime > subscriptions[j].EndTime
		}
		return subscriptions[i].Id > subscriptions[j].Id
	})
	return subscriptions, nil
}

func updateLockedProviderSubscriptionOrdersTx(
	tx *gorm.DB,
	orders []SubscriptionOrder,
	updates map[string]interface{},
) error {
	if tx == nil || len(orders) == 0 {
		return ErrSubscriptionOrderNotFound
	}
	orderIDs := make([]int, len(orders))
	for i := range orders {
		orderIDs[i] = orders[i].Id
	}
	return tx.Model(&SubscriptionOrder{}).
		Where("id IN ?", orderIDs).
		Updates(updates).Error
}

func subscriptionOrderUserID(orders []SubscriptionOrder) (int, error) {
	if len(orders) == 0 || orders[0].UserId <= 0 {
		return 0, ErrSubscriptionOrderNotFound
	}
	userID := orders[0].UserId
	for i := 1; i < len(orders); i++ {
		if orders[i].UserId != userID {
			return 0, errors.New("provider subscription spans multiple users")
		}
	}
	return userID, nil
}

func providerSubscriptionStatusPrecedence(status string) int {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "disputed":
		return 4
	case "refunded":
		return 3
	case "canceled", "cancelled", "unpaid", "incomplete_expired", "paused":
		return 2
	default:
		return 1
	}
}

func providerSubscriptionLifecycleUpdateIsCurrent(currentEventTime int64, currentStatus string, providerEventTime int64, providerStatus string) bool {
	if providerEventTime <= 0 || providerEventTime > currentEventTime {
		return true
	}
	if providerEventTime < currentEventTime {
		return false
	}
	return providerSubscriptionStatusPrecedence(providerStatus) >=
		providerSubscriptionStatusPrecedence(currentStatus)
}

// RenewProviderSubscription applies a paid recurring invoice exactly once.
// The invoice id is persisted as a unique order trade number and the existing
// entitlement is extended from its current end rather than creating an
// overlapping subscription.
func RenewProviderSubscription(paymentProvider string, providerSubscriptionId string, providerInvoiceId string, providerPaymentId string, money float64, providerPayload string) error {
	return RenewProviderSubscriptionAt(
		paymentProvider,
		providerSubscriptionId,
		providerInvoiceId,
		providerPaymentId,
		money,
		0,
		providerPayload,
	)
}

func RenewProviderSubscriptionAt(paymentProvider string, providerSubscriptionId string, providerInvoiceId string, providerPaymentId string, money float64, providerEventTime int64, providerPayload string) error {
	return renewProviderSubscriptionWithPaymentsAt(
		paymentProvider,
		providerSubscriptionId,
		providerInvoiceId,
		providerPaymentId,
		nil,
		money,
		providerEventTime,
		providerPayload,
	)
}

// RenewProviderSubscriptionWithPaymentsAt atomically extends an entitlement
// and binds every provider payment allocated to that invoice. Atomicity keeps a
// successful renewal from becoming untraceable if a reference collision or
// database failure is discovered while recording a multi-payment invoice.
func RenewProviderSubscriptionWithPaymentsAt(paymentProvider string, providerSubscriptionId string, providerInvoiceId string, providerPaymentId string, providerPaymentIDs []string, money float64, providerEventTime int64, providerPayload string) error {
	if len(providerPaymentIDs) == 0 {
		return errors.New("missing subscription provider payment identifiers")
	}
	return renewProviderSubscriptionWithPaymentsAt(
		paymentProvider,
		providerSubscriptionId,
		providerInvoiceId,
		providerPaymentId,
		providerPaymentIDs,
		money,
		providerEventTime,
		providerPayload,
	)
}

func renewProviderSubscriptionWithPaymentsAt(paymentProvider string, providerSubscriptionId string, providerInvoiceId string, providerPaymentId string, providerPaymentIDs []string, money float64, providerEventTime int64, providerPayload string) error {
	if paymentProvider == "" || providerSubscriptionId == "" || providerInvoiceId == "" {
		return errors.New("missing provider renewal identifier")
	}
	var err error
	paymentProvider, providerSubscriptionId, _, err =
		normalizeProviderSubscriptionIdentity(paymentProvider, providerSubscriptionId)
	if err != nil {
		return err
	}
	if providerEventTime < 0 {
		return errors.New("provider event time must not be negative")
	}
	if math.IsNaN(money) || math.IsInf(money, 0) || money <= 0 {
		return errors.New("provider renewal amount is invalid")
	}
	moneyMinor := decimal.NewFromFloat(money).Mul(decimal.NewFromInt(100)).Round(0)
	if !moneyMinor.IsPositive() || moneyMinor.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return errors.New("provider renewal amount is invalid")
	}
	allProviderPaymentIDs := append([]string(nil), providerPaymentIDs...)
	if providerPaymentId != "" {
		allProviderPaymentIDs = append(allProviderPaymentIDs, providerPaymentId)
	}
	tradeNo := paymentProvider + "_invoice_" + providerInvoiceId
	var cacheGroup string
	var userId int
	err = DB.Transaction(func(tx *gorm.DB) error {
		orders, err := lockProviderSubscriptionOrdersTx(tx, paymentProvider, providerSubscriptionId)
		if err != nil {
			return err
		}
		var sourceOrder *SubscriptionOrder
		for i := range orders {
			order := &orders[i]
			if order.TradeNo == tradeNo {
				if order.Status == common.TopUpStatusSuccess {
					if len(allProviderPaymentIDs) > 0 {
						return bindSubscriptionProviderPaymentsTx(
							tx,
							paymentProvider,
							order.Id,
							allProviderPaymentIDs,
						)
					}
					return nil
				}
				return ErrSubscriptionOrderStatusInvalid
			}
			if providerPaymentId != "" &&
				order.ProviderPaymentId == providerPaymentId &&
				order.Status == common.TopUpStatusSuccess {
				return nil
			}
			if order.Status == common.TopUpStatusSuccess {
				if sourceOrder == nil ||
					(sourceOrder.PlanSnapshot == "" && order.PlanSnapshot != "") {
					sourceOrder = order
				}
			}
		}
		if sourceOrder == nil {
			return ErrSubscriptionOrderNotFound
		}
		for i := range orders {
			if orders[i].PlanId != sourceOrder.PlanId {
				return errors.New("provider subscription spans multiple plans")
			}
		}
		userId, err = subscriptionOrderUserID(orders)
		if err != nil {
			return err
		}
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		plan, err := subscriptionPlanFromOrderTx(tx, sourceOrder)
		if err != nil {
			return err
		}
		planSnapshot := sourceOrder.PlanSnapshot
		if planSnapshot == "" {
			planSnapshot, err = marshalSubscriptionPlanSnapshot(plan)
			if err != nil {
				return err
			}
		}

		subscriptions, err := lockProviderUserSubscriptionsTx(
			tx,
			paymentProvider,
			providerSubscriptionId,
		)
		if err != nil {
			return err
		}
		if len(subscriptions) == 0 {
			return gorm.ErrRecordNotFound
		}
		sub := subscriptions[0]
		if sub.UserId != userId || sub.PlanId != plan.Id {
			return errors.New("provider subscription entitlement does not match paid plan")
		}
		reactivating := sub.Status != "active"
		if sub.Status != "active" {
			// A terminal entitlement may only be reactivated by a provider event
			// that is provably newer than the event which terminated it. Legacy
			// or administrative cancellations have no provider timestamp and are
			// therefore fail-closed.
			if sub.Status != "expired" &&
				(sub.ProviderEventTime <= 0 || providerEventTime <= sub.ProviderEventTime) {
				return nil
			}
		}
		now := getDBTimestampTx(tx)
		base := now
		if sub.EndTime > base {
			base = sub.EndTime
		}
		endTime, err := calcPlanEndTime(time.Unix(base, 0), plan)
		if err != nil {
			return err
		}
		sub.EndTime = endTime
		sub.Status = "active"
		sub.AmountTotal = plan.TotalAmount
		sub.AmountUsed = 0
		upgradeGroup := strings.TrimSpace(plan.UpgradeGroup)
		sub.UpgradeGroup = upgradeGroup
		sub.DowngradeGroup = strings.TrimSpace(plan.DowngradeGroup)
		sub.AllowWalletOverflow = plan.AllowWalletOverflow == nil || *plan.AllowWalletOverflow
		if reactivating && upgradeGroup != "" {
			prevGroup := user.Group
			var activeOwner UserSubscription
			result := tx.Where(
				"user_id = ? AND status = ? AND end_time > ? AND id <> ? AND upgrade_group = ? AND prev_user_group <> ''",
				userId,
				"active",
				now,
				sub.Id,
				user.Group,
			).Order("start_time desc, id desc").Limit(1).Find(&activeOwner)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected > 0 {
				prevGroup = activeOwner.PrevUserGroup
			}
			sub.PrevUserGroup = prevGroup
		}
		if providerEventTime > sub.ProviderEventTime {
			sub.ProviderEventTime = providerEventTime
		}
		if err := advanceSubscriptionQuotaResetVersion(&sub); err != nil {
			return err
		}
		sub.LastResetTime = now
		sub.NextResetTime = calcNextResetTime(time.Unix(now, 0), plan, endTime)
		if sub.NextResetTime == 0 {
			sub.LastResetTime = 0
		}
		if err := tx.Save(&sub).Error; err != nil {
			return err
		}

		order := &SubscriptionOrder{
			UserId:                 sourceOrder.UserId,
			PlanId:                 sourceOrder.PlanId,
			Money:                  money,
			PlanSnapshot:           planSnapshot,
			TradeNo:                tradeNo,
			PaymentMethod:          sourceOrder.PaymentMethod,
			PaymentProvider:        paymentProvider,
			Status:                 common.TopUpStatusSuccess,
			CreateTime:             now,
			CompleteTime:           now,
			ProviderPayload:        providerPayload,
			ProviderCustomerId:     sourceOrder.ProviderCustomerId,
			ProviderSubscriptionId: providerSubscriptionId,
			ProviderPaymentId:      providerPaymentId,
			ProviderStatus:         "active",
			ProviderEventTime:      providerEventTime,
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		if len(allProviderPaymentIDs) > 0 {
			if err := bindSubscriptionProviderPaymentsTx(
				tx,
				paymentProvider,
				order.Id,
				allProviderPaymentIDs,
			); err != nil {
				return err
			}
		}
		if err := upsertSubscriptionTopUpTx(tx, order); err != nil {
			return err
		}
		if upgradeGroup != "" {
			current := user.Group
			if current != upgradeGroup {
				if err := updateUserGroupTx(tx, userId, upgradeGroup); err != nil {
					return err
				}
				cacheGroup = upgradeGroup
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	return nil
}

// UpdateProviderSubscriptionStatus records non-terminal provider lifecycle
// states without revoking a still-valid local entitlement.
func UpdateProviderSubscriptionStatus(paymentProvider string, providerSubscriptionId string, status string, providerPayload string) error {
	return UpdateProviderSubscriptionStatusAt(paymentProvider, providerSubscriptionId, status, 0, providerPayload)
}

func UpdateProviderSubscriptionStatusAt(paymentProvider string, providerSubscriptionId string, status string, providerEventTime int64, providerPayload string) error {
	if paymentProvider == "" || providerSubscriptionId == "" {
		return errors.New("missing provider subscription identifier")
	}
	var err error
	paymentProvider, providerSubscriptionId, _, err =
		normalizeProviderSubscriptionIdentity(paymentProvider, providerSubscriptionId)
	if err != nil {
		return err
	}
	if providerEventTime < 0 {
		return errors.New("provider event time must not be negative")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		orders, err := lockProviderSubscriptionOrdersTx(tx, paymentProvider, providerSubscriptionId)
		if err != nil {
			return err
		}
		if providerEventTime > 0 {
			for i := range orders {
				if !providerSubscriptionLifecycleUpdateIsCurrent(
					orders[i].ProviderEventTime,
					orders[i].ProviderStatus,
					providerEventTime,
					status,
				) {
					return nil
				}
			}
		}
		updates := map[string]interface{}{"provider_status": status}
		if providerEventTime > 0 {
			updates["provider_event_time"] = providerEventTime
		}
		if providerPayload != "" {
			updates["provider_payload"] = providerPayload
		}
		return updateLockedProviderSubscriptionOrdersTx(tx, orders, updates)
	})
}

func SetProviderSubscriptionPayment(paymentProvider string, providerSubscriptionId string, providerPaymentId string, providerStatus string, providerPayload string) error {
	return SetProviderSubscriptionPaymentAt(
		paymentProvider,
		providerSubscriptionId,
		providerPaymentId,
		providerStatus,
		0,
		providerPayload,
	)
}

func SetProviderSubscriptionPaymentAt(paymentProvider string, providerSubscriptionId string, providerPaymentId string, providerStatus string, providerEventTime int64, providerPayload string) error {
	if paymentProvider == "" || providerSubscriptionId == "" {
		return errors.New("missing provider subscription identifier")
	}
	var err error
	paymentProvider, providerSubscriptionId, _, err =
		normalizeProviderSubscriptionIdentity(paymentProvider, providerSubscriptionId)
	if err != nil {
		return err
	}
	if providerEventTime < 0 {
		return errors.New("provider event time must not be negative")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		orders, err := lockProviderSubscriptionOrdersTx(tx, paymentProvider, providerSubscriptionId)
		if err != nil {
			return err
		}
		lifecycleIsCurrent := true
		if providerEventTime > 0 {
			for i := range orders {
				if !providerSubscriptionLifecycleUpdateIsCurrent(
					orders[i].ProviderEventTime,
					orders[i].ProviderStatus,
					providerEventTime,
					providerStatus,
				) {
					lifecycleIsCurrent = false
					break
				}
			}
		}
		if lifecycleIsCurrent {
			updates := map[string]interface{}{"provider_status": providerStatus}
			if providerEventTime > 0 {
				updates["provider_event_time"] = providerEventTime
			}
			if providerPayload != "" {
				updates["provider_payload"] = providerPayload
			}
			if err := updateLockedProviderSubscriptionOrdersTx(tx, orders, updates); err != nil {
				return err
			}
		}
		if providerPaymentId == "" {
			return nil
		}
		if err := tx.Model(&SubscriptionOrder{}).
			Where("id = ?", orders[0].Id).
			Update("provider_payment_id", providerPaymentId).Error; err != nil {
			return err
		}
		return bindSubscriptionProviderPaymentsTx(
			tx,
			paymentProvider,
			orders[0].Id,
			[]string{providerPaymentId},
		)
	})
}

func cancelProviderSubscriptionTx(tx *gorm.DB, orders []SubscriptionOrder, status string, providerEventTime int64, providerPayload string) (int, string, bool, error) {
	userID, err := subscriptionOrderUserID(orders)
	if err != nil {
		return 0, "", false, err
	}
	if providerEventTime > 0 {
		for i := range orders {
			if !providerSubscriptionLifecycleUpdateIsCurrent(
				orders[i].ProviderEventTime,
				orders[i].ProviderStatus,
				providerEventTime,
				status,
			) {
				return userID, "", true, nil
			}
		}
	}
	var user User
	if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
		return 0, "", false, err
	}

	subs, err := lockProviderUserSubscriptionsTx(
		tx,
		orders[0].PaymentProvider,
		orders[0].ProviderSubscriptionId,
	)
	if err != nil {
		return 0, "", false, err
	}
	if providerEventTime > 0 {
		for i := range subs {
			if subs[i].ProviderEventTime > providerEventTime {
				return userID, "", true, nil
			}
		}
	}

	now := getDBTimestampTx(tx)
	var transition *UserSubscription
	for i := range subs {
		sub := &subs[i]
		updates := map[string]interface{}{}
		if sub.Status == "active" {
			if transition == nil {
				transition = sub
			}
			updates["status"] = "cancelled"
			updates["end_time"] = now
			updates["updated_at"] = now
		}
		if providerEventTime > sub.ProviderEventTime {
			updates["provider_event_time"] = providerEventTime
		}
		if len(updates) > 0 {
			if err := tx.Model(sub).Updates(updates).Error; err != nil {
				return 0, "", false, err
			}
		}
	}

	cacheGroup := ""
	if transition != nil {
		target, err := downgradeUserGroupForSubscriptionTx(tx, transition, now)
		if err != nil {
			return 0, "", false, err
		}
		cacheGroup = target
	}
	updates := map[string]interface{}{"provider_status": status}
	if providerEventTime > 0 {
		updates["provider_event_time"] = providerEventTime
	}
	if providerPayload != "" {
		updates["provider_payload"] = providerPayload
	}
	if err := updateLockedProviderSubscriptionOrdersTx(tx, orders, updates); err != nil {
		return 0, "", false, err
	}
	return userID, cacheGroup, false, nil
}

// CancelProviderSubscription revokes active local entitlements once the
// provider reports a terminal cancellation/unpaid state.
func CancelProviderSubscription(paymentProvider string, providerSubscriptionId string, status string, providerPayload string) error {
	return CancelProviderSubscriptionAt(paymentProvider, providerSubscriptionId, status, 0, providerPayload)
}

func CancelProviderSubscriptionAt(paymentProvider string, providerSubscriptionId string, status string, providerEventTime int64, providerPayload string) error {
	if paymentProvider == "" || providerSubscriptionId == "" {
		return errors.New("missing provider subscription identifier")
	}
	var err error
	paymentProvider, providerSubscriptionId, _, err =
		normalizeProviderSubscriptionIdentity(paymentProvider, providerSubscriptionId)
	if err != nil {
		return err
	}
	if providerEventTime < 0 {
		return errors.New("provider event time must not be negative")
	}
	var userId int
	var cacheGroup string
	err = DB.Transaction(func(tx *gorm.DB) error {
		orders, err := lockProviderSubscriptionOrdersTx(tx, paymentProvider, providerSubscriptionId)
		if err != nil {
			return err
		}
		var stale bool
		userId, cacheGroup, stale, err = cancelProviderSubscriptionTx(
			tx, orders, status, providerEventTime, providerPayload,
		)
		if stale {
			cacheGroup = ""
		}
		return err
	})
	if err != nil {
		return err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	return nil
}

func CancelProviderSubscriptionByPayment(paymentProvider string, providerPaymentId string, status string, providerPayload string) error {
	return CancelProviderSubscriptionByPaymentAt(paymentProvider, providerPaymentId, status, 0, providerPayload)
}

func CancelProviderSubscriptionByPaymentAt(paymentProvider string, providerPaymentId string, status string, providerEventTime int64, providerPayload string) error {
	normalizedProvider, normalizedPaymentID, _, err :=
		normalizeProviderPaymentIdentity(paymentProvider, providerPaymentId)
	if err != nil {
		return err
	}
	paymentProvider = normalizedProvider
	providerPaymentId = normalizedPaymentID
	if providerEventTime < 0 {
		return errors.New("provider event time must not be negative")
	}
	var userID int
	var cacheGroup string
	err = DB.Transaction(func(tx *gorm.DB) error {
		candidate, err := findSubscriptionOrderByProviderPaymentTx(
			tx,
			paymentProvider,
			providerPaymentId,
		)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSubscriptionOrderNotFound
			}
			return err
		}
		if candidate.ProviderSubscriptionId == "" {
			return ErrSubscriptionOrderNotFound
		}
		// Lock the complete subscription order set in canonical ID order. Locking
		// the initially discovered payment row first would invert the order used
		// by renewal/refund paths and can deadlock on MySQL/PostgreSQL.
		orders, err := lockProviderSubscriptionOrdersTx(tx, paymentProvider, candidate.ProviderSubscriptionId)
		if err != nil {
			return err
		}
		candidateStillMatches := false
		for i := range orders {
			if orders[i].Id == candidate.Id {
				candidateStillMatches = true
				break
			}
		}
		if !candidateStillMatches {
			return ErrSubscriptionOrderNotFound
		}
		var stale bool
		userID, cacheGroup, stale, err = cancelProviderSubscriptionTx(
			tx,
			orders,
			status,
			providerEventTime,
			providerPayload,
		)
		if stale {
			cacheGroup = ""
		}
		return err
	})
	if err != nil {
		return err
	}
	if cacheGroup != "" && userID > 0 {
		_ = UpdateUserGroupCache(userID, cacheGroup)
	}
	return nil
}

type subscriptionRefundLookup int

const (
	subscriptionRefundByTradeNo subscriptionRefundLookup = iota + 1
	subscriptionRefundByProviderPayment
	subscriptionRefundByProviderSubscription
	subscriptionRefundByProviderInvoice
)

// ApplySubscriptionRefundByTradeNo applies a refund to a one-time subscription
// payment identified by its local merchant reference.
func ApplySubscriptionRefundByTradeNo(paymentProvider string, tradeNo string, refund SubscriptionRefundInput) error {
	return applySubscriptionRefund(paymentProvider, subscriptionRefundByTradeNo, tradeNo, refund)
}

// ApplySubscriptionRefundByProviderPayment applies a refund to the exact
// provider payment that funded a subscription period.
func ApplySubscriptionRefundByProviderPayment(paymentProvider string, providerPaymentID string, refund SubscriptionRefundInput) error {
	return applySubscriptionRefund(paymentProvider, subscriptionRefundByProviderPayment, providerPaymentID, refund)
}

// ApplySubscriptionRefundByProviderSubscription is the fallback for providers
// that omit the original payment identifier from a verified refund event.
func ApplySubscriptionRefundByProviderSubscription(paymentProvider string, providerSubscriptionID string, refund SubscriptionRefundInput) error {
	return applySubscriptionRefund(paymentProvider, subscriptionRefundByProviderSubscription, providerSubscriptionID, refund)
}

// ApplySubscriptionRefundByProviderInvoice applies a refund using the
// provider invoice identifier persisted by RenewProviderSubscriptionAt. This
// remains reliable when a modern invoice is funded by multiple payment
// objects and only one payment identifier fits on the local order.
func ApplySubscriptionRefundByProviderInvoice(paymentProvider string, providerInvoiceID string, refund SubscriptionRefundInput) error {
	return applySubscriptionRefund(paymentProvider, subscriptionRefundByProviderInvoice, providerInvoiceID, refund)
}

func applySubscriptionRefund(paymentProvider string, lookup subscriptionRefundLookup, lookupValue string, refund SubscriptionRefundInput) error {
	if paymentProvider == "" || lookupValue == "" || refund.EventID == "" {
		return errors.New("missing subscription refund identifier")
	}
	var err error
	paymentProvider, err = normalizePaymentProvider(paymentProvider)
	if err != nil {
		return err
	}
	if len(refund.EventID) > 128 {
		return errors.New("subscription refund event identifier is too long")
	}
	if len(refund.AmountScope) > 255 {
		return errors.New("subscription refund amount scope is too long")
	}
	if refund.AmountMode != ProviderRefundAmountIncremental &&
		refund.AmountMode != ProviderRefundAmountCumulative {
		return errors.New("invalid subscription refund amount mode")
	}
	if refund.AmountMinor < 0 || (!refund.Full && refund.AmountMinor <= 0) {
		return errors.New("invalid subscription refund amount")
	}
	if refund.ProviderEventTime < 0 {
		return errors.New("provider event time must not be negative")
	}
	if lookup == subscriptionRefundByProviderPayment {
		normalizedProvider, normalizedPaymentID, _, err :=
			normalizeProviderPaymentIdentity(paymentProvider, lookupValue)
		if err != nil {
			return err
		}
		paymentProvider = normalizedProvider
		lookupValue = normalizedPaymentID
	}

	var cacheGroup string
	var userID int
	err = DB.Transaction(func(tx *gorm.DB) error {
		var candidate SubscriptionOrder
		var queryErr error
		switch lookup {
		case subscriptionRefundByTradeNo:
			queryErr = findSubscriptionOrderByTradeNo(tx, lookupValue, &candidate)
		case subscriptionRefundByProviderPayment:
			resolved, err := findSubscriptionOrderByProviderPaymentTx(
				tx,
				paymentProvider,
				lookupValue,
			)
			if err == nil {
				candidate = *resolved
			}
			queryErr = err
		case subscriptionRefundByProviderSubscription:
			orders, err := findProviderSubscriptionOrders(
				tx,
				paymentProvider,
				lookupValue,
			)
			if err == nil {
				candidate = orders[len(orders)-1]
			}
			queryErr = err
		case subscriptionRefundByProviderInvoice:
			queryErr = findSubscriptionOrderByTradeNo(
				tx,
				paymentProvider+"_invoice_"+lookupValue,
				&candidate,
			)
		default:
			return errors.New("invalid subscription refund lookup")
		}
		if errors.Is(queryErr, gorm.ErrRecordNotFound) {
			return ErrSubscriptionOrderNotFound
		}
		if queryErr != nil {
			return queryErr
		}
		if candidate.PaymentProvider != paymentProvider {
			return ErrPaymentMethodMismatch
		}

		var orders []SubscriptionOrder
		if candidate.ProviderSubscriptionId != "" {
			var err error
			orders, err = lockProviderSubscriptionOrdersTx(tx, paymentProvider, candidate.ProviderSubscriptionId)
			if err != nil {
				return err
			}
		} else {
			if err := lockForUpdate(tx).Where("id = ?", candidate.Id).First(&candidate).Error; err != nil {
				return err
			}
			orders = []SubscriptionOrder{candidate}
		}
		var order *SubscriptionOrder
		for i := range orders {
			if orders[i].Id == candidate.Id {
				order = &orders[i]
				break
			}
		}
		if order == nil {
			return ErrSubscriptionOrderNotFound
		}
		paymentEventTime := order.ProviderEventTime
		if math.IsNaN(order.Money) || math.IsInf(order.Money, 0) || order.Money <= 0 {
			return errors.New("persisted subscription payment amount is invalid")
		}
		expectedMinorDecimal := decimal.NewFromFloat(order.Money).
			Mul(decimal.NewFromInt(100)).
			Round(0)
		if expectedMinorDecimal.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
			return errors.New("persisted subscription payment amount is invalid")
		}
		expectedMinor := expectedMinorDecimal.IntPart()
		if order.ProviderPaidAmountMinor < 0 || refund.PaidAmountMinor < 0 {
			return errors.New("persisted subscription payment amount is invalid")
		}
		if order.ProviderPaidAmountMinor > 0 {
			expectedMinor = order.ProviderPaidAmountMinor
		}
		if refund.PaidAmountMinor > 0 {
			if order.ProviderPaidAmountMinor > 0 &&
				order.ProviderPaidAmountMinor != refund.PaidAmountMinor {
				return errors.New("provider subscription payment amount changed")
			}
			expectedMinor = refund.PaidAmountMinor
		}
		if expectedMinor <= 0 || (!refund.Full && refund.AmountMinor > expectedMinor) {
			return errors.New("provider subscription refund amount is invalid")
		}
		if order.ProviderRefundedAmountMinor < 0 ||
			order.ProviderRefundedAmountMinor > expectedMinor {
			return errors.New("persisted subscription refund amount is invalid")
		}

		events := map[string]subscriptionRefundEvent{}
		if order.ProviderRefundEvents != "" {
			if err := common.UnmarshalJsonStr(order.ProviderRefundEvents, &events); err != nil {
				return fmt.Errorf("invalid persisted subscription refund events: %w", err)
			}
		}
		currentEvent := subscriptionRefundEvent{
			AmountMinor: refund.AmountMinor,
			Mode:        refund.AmountMode,
			AmountScope: refund.AmountScope,
			Full:        refund.Full,
			EventTime:   refund.ProviderEventTime,
		}
		if persisted, exists := events[refund.EventID]; exists {
			if persisted != currentEvent {
				return errors.New("provider subscription refund event changed")
			}
			return nil
		}

		desiredMinor := order.ProviderRefundedAmountMinor
		if desiredMinor == expectedMinor {
			// Once the payment is fully reversed, later partial/out-of-order
			// deliveries cannot change entitlement or accounting state. Accept
			// them without growing the replay ledger indefinitely.
			return nil
		}
		switch {
		case refund.Full:
			desiredMinor = expectedMinor
		case refund.AmountMode == ProviderRefundAmountCumulative:
			if refund.AmountScope == "" {
				if refund.AmountMinor > desiredMinor {
					desiredMinor = refund.AmountMinor
				}
				break
			}
			var previousScopeMinor int64
			hasLegacyOrderWideCumulative := false
			for _, persistedEvent := range events {
				if persistedEvent.Mode == ProviderRefundAmountCumulative &&
					persistedEvent.AmountScope == "" {
					hasLegacyOrderWideCumulative = true
				}
				if persistedEvent.Mode == ProviderRefundAmountCumulative &&
					persistedEvent.AmountScope == refund.AmountScope &&
					persistedEvent.AmountMinor > previousScopeMinor {
					previousScopeMinor = persistedEvent.AmountMinor
				}
			}
			if previousScopeMinor == 0 && hasLegacyOrderWideCumulative &&
				desiredMinor < refund.AmountMinor {
				previousScopeMinor = desiredMinor
			} else if previousScopeMinor == 0 && hasLegacyOrderWideCumulative {
				previousScopeMinor = refund.AmountMinor
			}
			if refund.AmountMinor > previousScopeMinor {
				increment := refund.AmountMinor - previousScopeMinor
				if desiredMinor > expectedMinor-increment {
					return errors.New("provider subscription refund total exceeds payment amount")
				}
				desiredMinor += increment
			}
		default:
			if desiredMinor > expectedMinor-refund.AmountMinor {
				return errors.New("provider subscription refund total exceeds payment amount")
			}
			desiredMinor += refund.AmountMinor
		}
		const maxProviderSubscriptionRefundEvents = 64
		if len(events) >= maxProviderSubscriptionRefundEvents {
			switch {
			case desiredMinor == order.ProviderRefundedAmountMinor:
				// A stale cumulative snapshot is a safe no-op. It need not consume
				// another bounded replay slot.
				return nil
			case desiredMinor == expectedMinor:
				// Preserve the terminal event replay proof and compact obsolete
				// partial-event proofs. State is monotonic after a full refund.
				events = map[string]subscriptionRefundEvent{}
			default:
				return errors.New("provider subscription refund event limit exceeded")
			}
		}
		events[refund.EventID] = currentEvent
		encodedEvents, err := common.Marshal(events)
		if err != nil {
			return err
		}
		order.ProviderRefundedAmountMinor = desiredMinor
		if refund.PaidAmountMinor > 0 {
			order.ProviderPaidAmountMinor = refund.PaidAmountMinor
		}
		order.ProviderRefundEvents = string(encodedEvents)
		if desiredMinor == expectedMinor {
			order.ProviderStatus = TopUpStatusRefunded
			if order.ProviderSubscriptionId == "" {
				order.Status = TopUpStatusRefunded
			}
		} else {
			order.ProviderStatus = TopUpStatusPartiallyRefunded
		}
		if refund.ProviderPayload != "" {
			order.ProviderPayload = refund.ProviderPayload
		}
		if err := tx.Save(order).Error; err != nil {
			return err
		}

		refundStatus := TopUpStatusPartiallyRefunded
		if desiredMinor == expectedMinor {
			refundStatus = TopUpStatusRefunded
		}
		var topUp TopUp
		if err := findTopUpByTradeNo(tx, order.TradeNo, &topUp); err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		} else if topUp.PaymentProvider == paymentProvider {
			if err := tx.Model(&topUp).Update("status", refundStatus).Error; err != nil {
				return err
			}
		}
		if desiredMinor != expectedMinor {
			return nil
		}

		if order.ProviderSubscriptionId != "" {
			for i := range orders {
				laterOrder := &orders[i]
				if laterOrder.Id == order.Id ||
					laterOrder.Status != common.TopUpStatusSuccess ||
					laterOrder.ProviderStatus == TopUpStatusRefunded {
					continue
				}
				fundedLater := laterOrder.ProviderEventTime > paymentEventTime ||
					laterOrder.Id > order.Id
				if fundedLater {
					// Refunding an older invoice does not cancel a provider
					// subscription or invalidate a newer paid period. A separate,
					// newer terminal lifecycle event can still revoke it.
					return nil
				}
			}
			var stale bool
			userID, cacheGroup, stale, err = cancelProviderSubscriptionTx(
				tx,
				orders,
				TopUpStatusRefunded,
				refund.ProviderEventTime,
				refund.ProviderPayload,
			)
			if stale {
				cacheGroup = ""
			}
			return err
		}

		userID = order.UserId
		var user User
		if err := lockForUpdate(tx).Where("id = ?", userID).First(&user).Error; err != nil {
			return err
		}
		var sub UserSubscription
		result := lockForUpdate(tx).
			Where("subscription_order_id = ? AND status = ?", order.Id, "active").
			First(&sub)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}
		if result.Error != nil {
			return nil
		}
		now := getDBTimestampTx(tx)
		updates := map[string]interface{}{
			"status":     "cancelled",
			"end_time":   now,
			"updated_at": now,
		}
		if refund.ProviderEventTime > sub.ProviderEventTime {
			updates["provider_event_time"] = refund.ProviderEventTime
		}
		if err := tx.Model(&sub).Updates(updates).Error; err != nil {
			return err
		}
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		cacheGroup = target
		return nil
	})
	if err != nil {
		return err
	}
	if cacheGroup != "" && userID > 0 {
		_ = UpdateUserGroupCache(userID, cacheGroup)
	}
	return nil
}

// CancelSubscriptionOrderByTradeNo revokes an entitlement purchased through a
// one-time provider product. Such orders have no recurring subscription ID, so
// refund webhooks must resolve the entitlement through the local trade number.
func CancelSubscriptionOrderByTradeNo(paymentProvider string, tradeNo string, status string, providerPayload string) error {
	if paymentProvider == "" || tradeNo == "" {
		return errors.New("missing subscription order identifier")
	}
	var err error
	paymentProvider, err = normalizePaymentProvider(paymentProvider)
	if err != nil {
		return err
	}
	var userId int
	var cacheGroup string
	err = DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := findSubscriptionOrderByTradeNo(lockForUpdate(tx), tradeNo, &order); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrSubscriptionOrderNotFound
			}
			return err
		}
		if order.PaymentProvider != paymentProvider {
			return ErrPaymentMethodMismatch
		}

		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", order.UserId).First(&user).Error; err != nil {
			return err
		}
		var sub UserSubscription
		result := lockForUpdate(tx).
			Where("subscription_order_id = ? AND status = ?", order.Id, "active").
			First(&sub)
		if result.Error != nil && !errors.Is(result.Error, gorm.ErrRecordNotFound) {
			return result.Error
		}
		now := getDBTimestampTx(tx)
		if result.Error == nil {
			userId = sub.UserId
			if err := tx.Model(&sub).Updates(map[string]interface{}{
				"status":     "cancelled",
				"end_time":   now,
				"updated_at": now,
			}).Error; err != nil {
				return err
			}
			target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
			if err != nil {
				return err
			}
			cacheGroup = target
		}

		order.Status = TopUpStatusRefunded
		order.ProviderStatus = status
		if providerPayload != "" {
			order.ProviderPayload = providerPayload
		}
		if err := tx.Save(&order).Error; err != nil {
			return err
		}
		var topUp TopUp
		if err := findTopUpByTradeNo(tx, tradeNo, &topUp); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		if topUp.PaymentProvider != paymentProvider {
			return nil
		}
		return tx.Model(&topUp).
			Updates(map[string]interface{}{"status": TopUpStatusRefunded}).Error
	})
	if err != nil {
		return err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	return nil
}

func upsertSubscriptionTopUpTx(tx *gorm.DB, order *SubscriptionOrder) error {
	if tx == nil || order == nil {
		return errors.New("invalid subscription order")
	}
	now := common.GetTimestamp()
	historyStatus := common.TopUpStatusSuccess
	if order.ProviderRefundedAmountMinor > 0 {
		historyStatus = TopUpStatusPartiallyRefunded
	}
	var topup TopUp
	if err := findTopUpByTradeNo(tx, order.TradeNo, &topup); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			topup = TopUp{
				UserId:            order.UserId,
				Amount:            0,
				Money:             order.Money,
				TradeNo:           order.TradeNo,
				PaymentMethod:     order.PaymentMethod,
				PaymentProvider:   order.PaymentProvider,
				ProviderPaymentId: order.ProviderPaymentId,
				CreateTime:        order.CreateTime,
				CompleteTime:      now,
				Status:            historyStatus,
			}
			return tx.Create(&topup).Error
		}
		return err
	}
	topup.Money = order.Money
	topup.PaymentProvider = order.PaymentProvider
	topup.ProviderPaymentId = order.ProviderPaymentId
	if topup.PaymentMethod == "" {
		topup.PaymentMethod = order.PaymentMethod
	} else if topup.PaymentMethod != order.PaymentMethod {
		return ErrPaymentMethodMismatch
	}
	if topup.CreateTime == 0 {
		topup.CreateTime = order.CreateTime
	}
	topup.CompleteTime = now
	topup.Status = historyStatus
	return tx.Save(&topup).Error
}

func ExpireSubscriptionOrder(tradeNo string, expectedPaymentProvider string) error {
	if tradeNo == "" {
		return errors.New("tradeNo is empty")
	}
	if expectedPaymentProvider != "" {
		var err error
		expectedPaymentProvider, err = normalizePaymentProvider(expectedPaymentProvider)
		if err != nil {
			return err
		}
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var order SubscriptionOrder
		if err := findSubscriptionOrderByTradeNo(lockForUpdate(tx), tradeNo, &order); err != nil {
			return ErrSubscriptionOrderNotFound
		}
		if expectedPaymentProvider != "" && order.PaymentProvider != expectedPaymentProvider {
			return ErrPaymentMethodMismatch
		}
		if order.Status != common.TopUpStatusPending {
			return nil
		}
		order.Status = common.TopUpStatusExpired
		order.CompleteTime = common.GetTimestamp()
		return tx.Save(&order).Error
	})
}

// Admin bind (no payment). Creates a UserSubscription from a plan.
func AdminBindSubscription(userId int, planId int, sourceNote string) (string, error) {
	if userId <= 0 || planId <= 0 {
		return "", errors.New("invalid userId or planId")
	}
	var upgradeGroup string
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(lockForUpdate(tx), planId)
		if err != nil {
			return err
		}
		upgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
		_, err = CreateUserSubscriptionFromPlanTx(tx, userId, plan, "admin")
		return err
	})
	if err != nil {
		return "", err
	}
	if upgradeGroup != "" {
		_ = UpdateUserGroupCache(userId, upgradeGroup)
		return fmt.Sprintf("用户分组将升级到 %s", upgradeGroup), nil
	}
	return "", nil
}

func calcSubscriptionBalanceQuota(priceAmount float64) (int, error) {
	if priceAmount < 0 || math.IsNaN(priceAmount) || math.IsInf(priceAmount, 0) {
		return 0, errors.New("套餐价格配置错误")
	}
	if priceAmount == 0 {
		return 0, nil
	}
	quotaPerUnit := common.CurrentQuotaPerUnit()
	if quotaPerUnit <= 0 || math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) {
		return 0, errors.New("额度单位配置错误")
	}
	quotaValue := decimal.NewFromFloat(priceAmount).
		Mul(decimal.NewFromFloat(quotaPerUnit)).
		Ceil()
	quota, clamp := common.QuotaFromDecimalChecked(quotaValue)
	if clamp != nil {
		return 0, clamp
	}
	return quota, nil
}

// PurchaseSubscriptionWithBalance creates a subscription by deducting the user's wallet quota.
func PurchaseSubscriptionWithBalance(userId int, planId int) error {
	if userId <= 0 || planId <= 0 {
		return errors.New("invalid userId or planId")
	}

	var logPlanTitle string
	var logMoney float64
	var chargedQuota int
	var upgradeGroup string
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		if !plan.Enabled {
			return errors.New("套餐未启用")
		}
		if err := ValidateSubscriptionPlanForPurchase(plan); err != nil {
			return fmt.Errorf("套餐配置无效: %w", err)
		}
		if plan.AllowBalancePay != nil && !*plan.AllowBalancePay {
			return errors.New("该套餐不允许使用余额兑换")
		}

		requiredQuota, err := calcSubscriptionBalanceQuota(plan.PriceAmount)
		if err != nil {
			return err
		}

		var user User
		if err := lockForUpdate(tx).Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		if requiredQuota > 0 && user.Quota < requiredQuota {
			return errors.New("余额不足")
		}
		if requiredQuota > 0 {
			if err := tx.Model(&User{}).Where("id = ?", userId).
				Update("quota", gorm.Expr("quota - ?", requiredQuota)).Error; err != nil {
				return err
			}
		}

		if _, err := CreateUserSubscriptionFromPlanTx(tx, userId, plan, PaymentMethodBalance); err != nil {
			return err
		}

		now := common.GetTimestamp()
		tradeNo := fmt.Sprintf("SUBBALUSR%dNO%s%d", userId, common.GetRandomString(6), time.Now().UnixNano())
		order := &SubscriptionOrder{
			UserId:          userId,
			PlanId:          plan.Id,
			Money:           plan.PriceAmount,
			TradeNo:         tradeNo,
			PaymentMethod:   PaymentMethodBalance,
			PaymentProvider: PaymentProviderBalance,
			Status:          common.TopUpStatusSuccess,
			CreateTime:      now,
			CompleteTime:    now,
			ProviderPayload: fmt.Sprintf("charged_quota=%d", requiredQuota),
		}
		if err := order.SetPlanSnapshot(plan); err != nil {
			return err
		}
		if err := tx.Create(order).Error; err != nil {
			return err
		}

		logPlanTitle = plan.Title
		logMoney = plan.PriceAmount
		chargedQuota = requiredQuota
		upgradeGroup = strings.TrimSpace(plan.UpgradeGroup)
		return nil
	})
	if err != nil {
		return err
	}

	if chargedQuota > 0 {
		if err := cacheDecrUserQuota(userId, int64(chargedQuota)); err != nil {
			common.SysLog("failed to decrease user quota cache after subscription balance purchase: " + err.Error())
		}
	}
	if upgradeGroup != "" {
		_ = UpdateUserGroupCache(userId, upgradeGroup)
	}
	msg := fmt.Sprintf("使用余额购买订阅成功，套餐: %s，支付金额: %.2f，扣除额度: %d", logPlanTitle, logMoney, chargedQuota)
	RecordLog(userId, LogTypeTopup, msg)
	return nil
}

// GetAllActiveUserSubscriptions returns all active subscriptions for a user.
func GetAllActiveUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var subs []UserSubscription
	err := DB.Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

// HasActiveUserSubscription returns whether the user has any active subscription.
// This is a lightweight existence check to avoid heavy pre-consume transactions.
func HasActiveUserSubscription(userId int) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var count int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// UserActiveSubscriptionsAllowWalletOverflow returns whether wallet balance may be used
// after the user's subscription quota is exhausted. A single active subscription that
// disallows wallet overflow (allow_wallet_overflow = false) blocks the fallback.
func UserActiveSubscriptionsAllowWalletOverflow(userId int) (bool, error) {
	if userId <= 0 {
		return false, errors.New("invalid userId")
	}
	now := common.GetTimestamp()
	var strictCount int64
	if err := DB.Model(&UserSubscription{}).
		Where("user_id = ? AND status = ? AND end_time > ? AND allow_wallet_overflow = ?",
			userId, "active", now, false).
		Count(&strictCount).Error; err != nil {
		return false, err
	}
	return strictCount == 0, nil
}

// GetAllUserSubscriptions returns all subscriptions (active and expired) for a user.
func GetAllUserSubscriptions(userId int) ([]SubscriptionSummary, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	var subs []UserSubscription
	err := DB.Where("user_id = ?", userId).
		Order("end_time desc, id desc").
		Find(&subs).Error
	if err != nil {
		return nil, err
	}
	return buildSubscriptionSummaries(subs), nil
}

func buildSubscriptionSummaries(subs []UserSubscription) []SubscriptionSummary {
	if len(subs) == 0 {
		return []SubscriptionSummary{}
	}
	result := make([]SubscriptionSummary, 0, len(subs))
	for _, sub := range subs {
		subCopy := sub
		result = append(result, SubscriptionSummary{
			Subscription: &subCopy,
		})
	}
	return result
}

// AdminInvalidateUserSubscription marks a user subscription as cancelled and ends it immediately.
func AdminInvalidateUserSubscription(userSubscriptionId int) (string, error) {
	if userSubscriptionId <= 0 {
		return "", errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var identity UserSubscription
		if err := tx.Select("id", "user_id").
			Where("id = ?", userSubscriptionId).
			First(&identity).Error; err != nil {
			return err
		}
		userId = identity.UserId
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
		if sub.UserId != userId {
			return errors.New("subscription owner changed during invalidation")
		}
		if err := tx.Model(&sub).Updates(map[string]interface{}{
			"status":              "cancelled",
			"end_time":            now,
			"updated_at":          now,
			"provider_event_time": 0,
		}).Error; err != nil {
			return err
		}
		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	if downgradeGroup != "" {
		return fmt.Sprintf("用户分组将回退到 %s", downgradeGroup), nil
	}
	return "", nil
}

// AdminDeleteUserSubscription hard-deletes a user subscription.
func AdminDeleteUserSubscription(userSubscriptionId int) (string, error) {
	if userSubscriptionId <= 0 {
		return "", errors.New("invalid userSubscriptionId")
	}
	now := common.GetTimestamp()
	cacheGroup := ""
	downgradeGroup := ""
	var userId int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var identity UserSubscription
		if err := tx.Select("id", "user_id").
			Where("id = ?", userSubscriptionId).
			First(&identity).Error; err != nil {
			return err
		}
		userId = identity.UserId
		var user User
		if err := lockForUpdate(tx).Select("id").Where("id = ?", userId).First(&user).Error; err != nil {
			return err
		}
		var sub UserSubscription
		if err := lockForUpdate(tx).
			Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
			return err
		}
		if sub.UserId != userId {
			return errors.New("subscription owner changed during deletion")
		}

		// Pre-consume writes lock the subscription before creating their durable
		// request record. Holding the same row lock here closes the race between
		// this guard and a new charge on MySQL/PostgreSQL; SQLite's single-writer
		// transaction semantics provide the equivalent exclusion.
		var livePreConsumeCount int64
		if err := tx.Model(&SubscriptionPreConsumeRecord{}).
			Where("user_subscription_id = ? AND (status IS NULL OR status NOT IN ?)", userSubscriptionId, []string{"refunded", "settled"}).
			Count(&livePreConsumeCount).Error; err != nil {
			return err
		}
		if livePreConsumeCount != 0 {
			return errors.New("subscription has live billing reservations; cancel it and retry deletion after reservations expire")
		}

		// A completed per-call charge can still carry a refund obligation while
		// its asynchronous Midjourney job is in flight. The pre-consume record is
		// not a sufficient guard: legacy per-call charges may not have one, and old
		// records are eventually cleaned up. Keep the subscription until every
		// linked job either completed successfully or its exact reversal succeeded.
		// These reads intentionally remain non-locking. Reversal processing locks
		// task -> subscription, so waiting on task rows while holding subscription
		// would invert that order. Seeing the prior pending state simply fails the
		// deletion closed and lets the processor continue after rollback.
		var billedMidjourneyTasks []Midjourney
		if err := tx.Select("id", "user_id", "status", "progress", "quota", "billing_request_id", "billing_purpose", "billing_source",
			"billing_subscription_id", "billing_task_id", "billing_token_id", "billing_finalized").
			Where("billing_source = ? AND billing_subscription_id = ? AND (billing_purpose <> ? OR billing_task_id <> ?)",
				BillingAdjustmentSubscription, userSubscriptionId, "", "").
			Find(&billedMidjourneyTasks).Error; err != nil {
			return err
		}
		for _, task := range billedMidjourneyTasks {
			if task.BillingTaskId == "" || !task.BillingFinalized {
				return fmt.Errorf("subscription has unresolved Midjourney billing obligation for task %d", task.Id)
			}
			if _, _, err := loadSucceededMidjourneySettlementTx(tx, &task); err != nil {
				return fmt.Errorf("subscription has invalid Midjourney billing settlement for task %d: %w", task.Id, err)
			}
			if task.Status == "SUCCESS" && task.Progress == "100%" {
				continue
			}
			if _, _, err := loadSucceededMidjourneyReversalTx(tx, &task); err != nil {
				if errors.Is(err, errMidjourneyBillingReversalNotSucceeded) {
					return fmt.Errorf("subscription has unresolved Midjourney billing obligation for task %d", task.Id)
				}
				return fmt.Errorf("subscription has invalid Midjourney billing reversal for task %d: %w", task.Id, err)
			}
		}

		// Generic async task billing context is stored in a portable JSON value.
		// Inspect it through the model scanner instead of dialect-specific JSON
		// operators. Keep the subscription while any task history still points at
		// it: terminal status is written before completion/refund accounting, so
		// status alone cannot safely prove there is no late obligation.
		var billedTasks []Task
		if err := tx.Select("id", "task_id", "status", "private_data").
			Where("user_id = ?", sub.UserId).
			Find(&billedTasks).Error; err != nil {
			return err
		}
		for _, task := range billedTasks {
			if task.PrivateData.BillingSource == BillingAdjustmentSubscription &&
				task.PrivateData.SubscriptionId == userSubscriptionId {
				return fmt.Errorf("subscription is referenced by async task %s; retain or cancel it until task billing history is removed", task.TaskID)
			}
		}

		// Billing adjustment payloads live in a portable TEXT column, so inspect
		// unresolved tasks in Go instead of relying on dialect-specific JSON operators.
		// A failed/unknown adjustment is not a safe terminal state: no supported
		// worker path can prove that its financial obligation was completed.
		// Do not row-lock the tasks: processors lock task -> subscription, while
		// this transaction already holds subscription; avoiding the inverse lock
		// order prevents an unnecessary deadlock.
		var unresolvedBillingTasks []SystemTask
		if err := tx.Select("task_id", "payload").
			Where(
				"type = ? AND (status IS NULL OR status <> ?)",
				SystemTaskTypeBillingAdjustment,
				SystemTaskStatusSucceeded,
			).
			Find(&unresolvedBillingTasks).Error; err != nil {
			return err
		}
		for _, task := range unresolvedBillingTasks {
			var adjustment BillingAdjustment
			if err := common.UnmarshalJsonStr(task.Payload, &adjustment); err != nil {
				return fmt.Errorf("inspect active billing adjustment %s: %w", task.TaskID, err)
			}
			if adjustment.FundingSource == BillingAdjustmentSubscription && adjustment.SubscriptionID == userSubscriptionId {
				return errors.New("subscription has an unresolved billing adjustment; retry deletion after billing reconciliation")
			}
		}

		target, err := downgradeUserGroupForSubscriptionTx(tx, &sub, now)
		if err != nil {
			return err
		}
		if target != "" {
			cacheGroup = target
			downgradeGroup = target
		}
		if err := tx.Where("id = ?", userSubscriptionId).Delete(&UserSubscription{}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if cacheGroup != "" && userId > 0 {
		_ = UpdateUserGroupCache(userId, cacheGroup)
	}
	if downgradeGroup != "" {
		return fmt.Sprintf("用户分组将回退到 %s", downgradeGroup), nil
	}
	return "", nil
}

func resetUserSubscriptionTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, now int64, advanceResetTime bool) error {
	if tx == nil || sub == nil || plan == nil {
		return errors.New("invalid reset args")
	}
	sub.AmountUsed = 0
	if err := advanceSubscriptionQuotaResetVersion(sub); err != nil {
		return err
	}
	if advanceResetTime {
		nextReset := calcNextResetTime(time.Unix(now, 0), plan, sub.EndTime)
		sub.NextResetTime = nextReset
		if nextReset > 0 {
			sub.LastResetTime = now
		} else {
			sub.LastResetTime = 0
		}
	}
	return tx.Save(sub).Error
}

func advanceSubscriptionQuotaResetVersion(sub *UserSubscription) error {
	if sub == nil {
		return errors.New("subscription is nil")
	}
	if sub.QuotaResetVersion < 0 || sub.QuotaResetVersion == math.MaxInt64 {
		return errors.New("subscription quota reset version exceeds storage range")
	}
	sub.QuotaResetVersion++
	return nil
}

func buildSubscriptionResetResult(plan *SubscriptionPlan, subs []UserSubscription, advanceResetTime bool) *SubscriptionResetResult {
	userIds := make([]int, 0, len(subs))
	seenUsers := make(map[int]struct{}, len(subs))
	for _, sub := range subs {
		if _, ok := seenUsers[sub.UserId]; ok {
			continue
		}
		seenUsers[sub.UserId] = struct{}{}
		userIds = append(userIds, sub.UserId)
	}
	return &SubscriptionResetResult{
		PlanId:           plan.Id,
		MatchedCount:     len(subs),
		ResetCount:       len(subs),
		UserCount:        len(userIds),
		AdvanceResetTime: advanceResetTime,
		PlanTitle:        plan.Title,
		AffectedUserIds:  userIds,
	}
}

func adminResetUserSubscriptionsByPlanTx(tx *gorm.DB, userId int, plan *SubscriptionPlan, now int64, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if tx == nil || plan == nil {
		return nil, errors.New("invalid reset args")
	}
	var subs []UserSubscription
	if err := lockForUpdate(tx).
		Where("user_id = ? AND plan_id = ? AND status = ? AND end_time > ?", userId, plan.Id, "active", now).
		Order("id asc").
		Find(&subs).Error; err != nil {
		return nil, err
	}
	if len(subs) == 0 {
		return nil, errors.New("该用户没有有效的此套餐订阅")
	}
	for i := range subs {
		if err := resetUserSubscriptionTx(tx, &subs[i], plan, now, advanceResetTime); err != nil {
			return nil, err
		}
	}
	return buildSubscriptionResetResult(plan, subs, advanceResetTime), nil
}

func adminResetPlanSubscriptionsTx(tx *gorm.DB, plan *SubscriptionPlan, now int64, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if tx == nil || plan == nil {
		return nil, errors.New("invalid reset args")
	}
	var subs []UserSubscription
	if err := lockForUpdate(tx).
		Where("plan_id = ? AND status = ? AND end_time > ?", plan.Id, "active", now).
		Order("id asc").
		Find(&subs).Error; err != nil {
		return nil, err
	}
	for i := range subs {
		if err := resetUserSubscriptionTx(tx, &subs[i], plan, now, advanceResetTime); err != nil {
			return nil, err
		}
	}
	return buildSubscriptionResetResult(plan, subs, advanceResetTime), nil
}

func AdminResetUserSubscriptionsByPlan(userId int, planId int, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if userId <= 0 || planId <= 0 {
		return nil, errors.New("invalid userId or planId")
	}
	var result *SubscriptionResetResult
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		result, err = adminResetUserSubscriptionsByPlanTx(tx, userId, plan, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func AdminResetPlanSubscriptions(planId int, advanceResetTime bool) (*SubscriptionResetResult, error) {
	if planId <= 0 {
		return nil, errors.New("invalid planId")
	}
	var result *SubscriptionResetResult
	now := GetDBTimestamp()
	err := DB.Transaction(func(tx *gorm.DB) error {
		plan, err := getSubscriptionPlanByIdTx(tx, planId)
		if err != nil {
			return err
		}
		result, err = adminResetPlanSubscriptionsTx(tx, plan, now, advanceResetTime)
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

type SubscriptionPreConsumeResult struct {
	UserSubscriptionId int
	PreConsumed        int64
	AmountTotal        int64
	AmountUsedBefore   int64
	AmountUsedAfter    int64
}

func loadSubscriptionPreConsumeResultTx(tx *gorm.DB, record *SubscriptionPreConsumeRecord, userId int, result *SubscriptionPreConsumeResult) error {
	if record == nil || result == nil {
		return errors.New("invalid subscription pre-consume result args")
	}
	if record.UserId != userId {
		return errors.New("subscription pre-consume request belongs to another user")
	}
	if record.Status == "refunded" {
		return errors.New("subscription pre-consume already refunded")
	}
	if record.Status != "consumed" && record.Status != "settled" {
		return fmt.Errorf("subscription pre-consume has invalid status %s", record.Status)
	}
	if record.UserSubscriptionId <= 0 || record.PreConsumed <= 0 || record.PreConsumed > int64(common.MaxQuota) {
		return errors.New("subscription pre-consume record contains invalid quota context")
	}
	var sub UserSubscription
	if err := tx.Where("id = ?", record.UserSubscriptionId).First(&sub).Error; err != nil {
		return err
	}
	if sub.AmountUsed < 0 || sub.AmountTotal < 0 {
		return errors.New("subscription contains invalid quota values")
	}
	result.UserSubscriptionId = record.UserSubscriptionId
	result.PreConsumed = record.PreConsumed
	result.AmountTotal = sub.AmountTotal
	result.AmountUsedBefore = sub.AmountUsed
	result.AmountUsedAfter = sub.AmountUsed
	return nil
}

// ExpireDueSubscriptions marks expired subscriptions and handles group downgrade.
func ExpireDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := DB.Where("status = ? AND end_time > 0 AND end_time <= ?", "active", now).
		Order("end_time asc, id asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	expiredCount := 0
	subscriptionsByUser := make(map[int][]int, len(subs))
	for _, sub := range subs {
		if sub.UserId > 0 {
			subscriptionsByUser[sub.UserId] = append(subscriptionsByUser[sub.UserId], sub.Id)
		}
	}
	for userId, selectedIDs := range subscriptionsByUser {
		cacheGroup := ""
		err := DB.Transaction(func(tx *gorm.DB) error {
			var user User
			if err := lockForUpdate(tx).
				Select("id", commonGroupCol).
				Where("id = ?", userId).
				First(&user).Error; err != nil {
				return err
			}
			var due []UserSubscription
			if err := lockForUpdate(tx).
				Where("id IN ? AND user_id = ? AND status = ? AND end_time > 0 AND end_time <= ?",
					selectedIDs, userId, "active", now).
				Order("id asc").
				Find(&due).Error; err != nil {
				return err
			}
			if len(due) == 0 {
				return nil
			}
			dueIDs := make([]int, 0, len(due))
			for i := range due {
				dueIDs = append(dueIDs, due[i].Id)
			}
			res := tx.Model(&UserSubscription{}).
				Where("id IN ? AND status = ?", dueIDs, "active").
				Updates(map[string]interface{}{
					"status":     "expired",
					"updated_at": common.GetTimestamp(),
				})
			if res.Error != nil {
				return res.Error
			}
			expiredCount += int(res.RowsAffected)

			currentGroup := user.Group
			// Identify the just-expired entitlement that actually owns the
			// current paid group. Ordering by purchase time handles multiple
			// overdue plans whose end-time order differs from purchase order.
			var expiredOwner *UserSubscription
			var explicitDowngrade *UserSubscription
			for i := range due {
				candidate := &due[i]
				if strings.TrimSpace(candidate.UpgradeGroup) == currentGroup &&
					(expiredOwner == nil ||
						candidate.StartTime > expiredOwner.StartTime ||
						(candidate.StartTime == expiredOwner.StartTime && candidate.Id > expiredOwner.Id)) {
					expiredOwner = candidate
				}
				if strings.TrimSpace(candidate.DowngradeGroup) != "" &&
					(explicitDowngrade == nil ||
						candidate.StartTime > explicitDowngrade.StartTime ||
						(candidate.StartTime == explicitDowngrade.StartTime && candidate.Id > explicitDowngrade.Id)) {
					explicitDowngrade = candidate
				}
			}

			// If a different paid upgrade remains active, transition back to the
			// most recently purchased surviving group when the just-expired
			// entitlement currently owns the user's group. A manual group change
			// is left untouched.
			var activeSub UserSubscription
			activeQuery := tx.Where("user_id = ? AND status = ? AND end_time > ? AND upgrade_group <> ''",
				userId, "active", now).
				Order("start_time desc, id desc").
				Limit(1).
				Find(&activeSub)
			if activeQuery.Error != nil {
				return activeQuery.Error
			}
			if activeQuery.RowsAffected > 0 {
				target := strings.TrimSpace(activeSub.UpgradeGroup)
				if expiredOwner != nil {
					prevGroup := strings.TrimSpace(expiredOwner.PrevUserGroup)
					if prevGroup != "" && prevGroup != strings.TrimSpace(activeSub.PrevUserGroup) {
						target = prevGroup
					}
				}
				if expiredOwner == nil || target == "" || target == currentGroup {
					return nil
				}
				if err := updateUserGroupTx(tx, userId, target); err != nil {
					return err
				}
				cacheGroup = target
				return nil
			}

			// An explicit downgrade group takes precedence; otherwise revert to the
			// group held before purchase (legacy behavior, only when the subscription
			// actually elevated the user).
			transition := expiredOwner
			if transition == nil {
				transition = explicitDowngrade
			}
			if transition == nil {
				return nil
			}
			target := strings.TrimSpace(transition.DowngradeGroup)
			if target == "" {
				prevGroup := strings.TrimSpace(transition.PrevUserGroup)
				if prevGroup == "" {
					return nil
				}
				target = prevGroup
			}
			if target == "" || target == currentGroup {
				return nil
			}
			if err := updateUserGroupTx(tx, userId, target); err != nil {
				return err
			}
			cacheGroup = target
			return nil
		})
		if err != nil {
			return expiredCount, err
		}
		if cacheGroup != "" {
			_ = UpdateUserGroupCache(userId, cacheGroup)
		}
	}
	return expiredCount, nil
}

// SubscriptionPreConsumeRecord stores idempotent pre-consume operations per request.
type SubscriptionPreConsumeRecord struct {
	Id                 int    `json:"id"`
	RequestId          string `json:"request_id" gorm:"type:varchar(64);uniqueIndex"`
	ClaimToken         string `json:"-" gorm:"type:varchar(32)"`
	UserId             int    `json:"user_id" gorm:"index"`
	UserSubscriptionId int    `json:"user_subscription_id" gorm:"index"`
	PreConsumed        int64  `json:"pre_consumed" gorm:"type:bigint;not null;default:0"`
	QuotaResetVersion  int64  `json:"-" gorm:"type:bigint"`
	Status             string `json:"status" gorm:"type:varchar(32);index"` // consumed/settled/refunded
	CreatedAt          int64  `json:"created_at" gorm:"bigint"`
	UpdatedAt          int64  `json:"updated_at" gorm:"bigint;index"`
}

func (r *SubscriptionPreConsumeRecord) BeforeCreate(tx *gorm.DB) error {
	now := common.GetTimestamp()
	r.CreatedAt = now
	r.UpdatedAt = now
	return nil
}

func (r *SubscriptionPreConsumeRecord) BeforeUpdate(tx *gorm.DB) error {
	r.UpdatedAt = common.GetTimestamp()
	return nil
}

func maybeResetUserSubscriptionWithPlanTx(tx *gorm.DB, sub *UserSubscription, plan *SubscriptionPlan, now int64) error {
	if tx == nil || sub == nil || plan == nil {
		return errors.New("invalid reset args")
	}
	if sub.NextResetTime > 0 && sub.NextResetTime > now {
		return nil
	}
	if NormalizeResetPeriod(plan.QuotaResetPeriod) == SubscriptionResetNever {
		return nil
	}
	baseUnix := sub.LastResetTime
	if baseUnix <= 0 {
		baseUnix = sub.StartTime
	}
	base := time.Unix(baseUnix, 0)
	next := calcNextResetTime(base, plan, sub.EndTime)
	advanced := false
	for next > 0 && next <= now {
		advanced = true
		base = time.Unix(next, 0)
		next = calcNextResetTime(base, plan, sub.EndTime)
	}
	if !advanced {
		if sub.NextResetTime == 0 && next > 0 {
			sub.NextResetTime = next
			sub.LastResetTime = base.Unix()
			return tx.Save(sub).Error
		}
		return nil
	}
	sub.AmountUsed = 0
	if err := advanceSubscriptionQuotaResetVersion(sub); err != nil {
		return err
	}
	sub.LastResetTime = base.Unix()
	sub.NextResetTime = next
	return tx.Save(sub).Error
}

// PreConsumeUserSubscription pre-consumes from any active subscription total quota.
func PreConsumeUserSubscription(requestId string, userId int, modelName string, quotaType int, amount int64) (*SubscriptionPreConsumeResult, error) {
	if userId <= 0 {
		return nil, errors.New("invalid userId")
	}
	if strings.TrimSpace(requestId) == "" {
		return nil, errors.New("requestId is empty")
	}
	if amount <= 0 {
		return nil, errors.New("amount must be > 0")
	}
	if amount > int64(common.MaxQuota) {
		return nil, fmt.Errorf("amount exceeds supported quota range: %d", amount)
	}
	now := GetDBTimestamp()

	returnValue := &SubscriptionPreConsumeResult{}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var existing SubscriptionPreConsumeRecord
		query := tx.Where("request_id = ?", requestId).Limit(1).Find(&existing)
		if query.Error != nil {
			return query.Error
		}
		if query.RowsAffected > 0 {
			// Re-read under a row lock so a concurrent refund cannot change the
			// reservation between the status check and this idempotent return. The
			// initial non-locking probe avoids taking a MySQL gap lock when the key
			// does not exist, which would turn concurrent first-use inserts into a
			// lock-upgrade deadlock.
			if err := lockForUpdate(tx).
				Where("request_id = ?", requestId).
				First(&existing).Error; err != nil {
				return err
			}
			return loadSubscriptionPreConsumeResultTx(tx, &existing, userId, returnValue)
		}

		var subs []UserSubscription
		if err := lockForUpdate(tx).
			Where("user_id = ? AND status = ? AND end_time > ?", userId, "active", now).
			Order("id asc").
			Find(&subs).Error; err != nil {
			return fmt.Errorf("query active subscriptions: %w", err)
		}
		if len(subs) == 0 {
			return errors.New("no active subscription")
		}
		sort.SliceStable(subs, func(i, j int) bool {
			if subs[i].EndTime != subs[j].EndTime {
				return subs[i].EndTime < subs[j].EndTime
			}
			return subs[i].Id < subs[j].Id
		})
		for _, candidate := range subs {
			sub := candidate
			plan, err := getSubscriptionPlanByIdTx(tx, sub.PlanId)
			if err != nil {
				return err
			}
			if err := maybeResetUserSubscriptionWithPlanTx(tx, &sub, plan, now); err != nil {
				return err
			}
			if sub.AmountUsed < 0 || sub.AmountTotal < 0 {
				return errors.New("subscription contains invalid quota values")
			}
			usedBefore := sub.AmountUsed
			if sub.AmountTotal > 0 {
				remain := sub.AmountTotal - usedBefore
				if remain < amount {
					continue
				}
			}
			record := &SubscriptionPreConsumeRecord{
				RequestId:          requestId,
				ClaimToken:         common.GetUUID(),
				UserId:             userId,
				UserSubscriptionId: sub.Id,
				PreConsumed:        amount,
				QuotaResetVersion:  sub.QuotaResetVersion,
				Status:             "consumed",
			}
			createResult := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "request_id"}},
				DoNothing: true,
			}).Create(record)
			if createResult.Error != nil {
				return createResult.Error
			}
			// MySQL implements DoNothing as a no-op ON DUPLICATE KEY UPDATE and can
			// report one affected row for a duplicate when CLIENT_FOUND_ROWS is
			// enabled. Always read the canonical row and compare its unpredictable
			// token instead of using RowsAffected to decide who owns the charge.
			var persisted SubscriptionPreConsumeRecord
			if err := lockForUpdate(tx).
				Where("request_id = ?", requestId).
				First(&persisted).Error; err != nil {
				return err
			}
			if persisted.ClaimToken != record.ClaimToken {
				return loadSubscriptionPreConsumeResultTx(tx, &persisted, userId, returnValue)
			}
			if sub.AmountUsed > math.MaxInt64-amount {
				return errors.New("subscription used amount overflow")
			}
			sub.AmountUsed += amount
			if err := tx.Save(&sub).Error; err != nil {
				return err
			}
			returnValue.UserSubscriptionId = sub.Id
			returnValue.PreConsumed = amount
			returnValue.AmountTotal = sub.AmountTotal
			returnValue.AmountUsedBefore = usedBefore
			returnValue.AmountUsedAfter = sub.AmountUsed
			return nil
		}
		return fmt.Errorf("subscription quota insufficient, need=%d", amount)
	})
	if err != nil {
		return nil, err
	}
	return returnValue, nil
}

// RefundSubscriptionPreConsume is idempotent and refunds pre-consumed subscription quota by requestId.
func RefundSubscriptionPreConsume(requestId string) error {
	if strings.TrimSpace(requestId) == "" {
		return errors.New("requestId is empty")
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		var record SubscriptionPreConsumeRecord
		if err := lockForUpdate(tx).
			Where("request_id = ?", requestId).First(&record).Error; err != nil {
			return err
		}
		if record.Status == "refunded" {
			return nil
		}
		if record.PreConsumed <= 0 {
			record.Status = "refunded"
			return tx.Save(&record).Error
		}
		if record.PreConsumed > int64(common.MaxQuota) {
			return errors.New("subscription pre-consume record contains invalid quota")
		}
		if _, err := refundSubscriptionPreConsumeUsageTx(tx, &record, record.PreConsumed); err != nil {
			return err
		}
		record.Status = "refunded"
		return tx.Save(&record).Error
	})
}

// refundSubscriptionPreConsumeUsageTx reverses a reservation only within the
// quota generation that originally carried it. A reset already cleared the
// old charge, so a late refund must not subtract usage created in the new
// generation.
func refundSubscriptionPreConsumeUsageTx(tx *gorm.DB, record *SubscriptionPreConsumeRecord, refundAmount int64) (int64, error) {
	if tx == nil || record == nil || refundAmount < 0 || refundAmount > int64(common.MaxQuota) {
		return 0, errors.New("invalid subscription pre-consume refund context")
	}
	if refundAmount == 0 {
		return 0, nil
	}
	var subscription UserSubscription
	if err := lockForUpdate(tx).Where("id = ?", record.UserSubscriptionId).First(&subscription).Error; err != nil {
		return 0, err
	}
	if subscription.UserId != record.UserId || subscription.AmountUsed < 0 || subscription.AmountTotal < 0 {
		return 0, errors.New("subscription pre-consume refund context does not match subscription")
	}
	if subscription.QuotaResetVersion < record.QuotaResetVersion {
		return 0, errors.New("subscription quota reset version moved backwards")
	}
	periodAdvanced := subscription.QuotaResetVersion > record.QuotaResetVersion
	if record.CreatedAt > 0 {
		periodAdvanced = periodAdvanced || subscription.LastResetTime > record.CreatedAt
	}
	if periodAdvanced {
		return 0, nil
	}
	applied, err := updateSubscriptionUsedTx(tx, &subscription, -refundAmount, false)
	if err != nil {
		return 0, err
	}
	if applied != -refundAmount {
		return 0, errors.New("subscription pre-consume refund exceeds current-period usage")
	}
	return applied, nil
}

// ResetDueSubscriptions resets subscriptions whose next_reset_time has passed.
func ResetDueSubscriptions(limit int) (int, error) {
	if limit <= 0 {
		limit = 200
	}
	now := GetDBTimestamp()
	var subs []UserSubscription
	if err := DB.Where("next_reset_time > 0 AND next_reset_time <= ? AND status = ?", now, "active").
		Order("next_reset_time asc").
		Limit(limit).
		Find(&subs).Error; err != nil {
		return 0, err
	}
	if len(subs) == 0 {
		return 0, nil
	}
	resetCount := 0
	for _, sub := range subs {
		subCopy := sub
		plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				continue
			}
			return resetCount, fmt.Errorf("load subscription plan %d for quota reset: %w", sub.PlanId, err)
		}
		if plan == nil {
			continue
		}
		err = DB.Transaction(func(tx *gorm.DB) error {
			var locked UserSubscription
			if err := lockForUpdate(tx).
				Where("id = ? AND next_reset_time > 0 AND next_reset_time <= ?", subCopy.Id, now).
				First(&locked).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			if err := maybeResetUserSubscriptionWithPlanTx(tx, &locked, plan, now); err != nil {
				return err
			}
			resetCount++
			return nil
		})
		if err != nil {
			return resetCount, err
		}
	}
	return resetCount, nil
}

const subscriptionPreConsumeCleanupBatchSize = 500

// CleanupSubscriptionPreConsumeRecords removes a bounded batch of terminal
// reservation proofs. Consumed rows are never eligible. A settled proof is
// retained while an asynchronous task or durable billing outbox can still
// issue a reset-safe refund against it.
func CleanupSubscriptionPreConsumeRecords(olderThanSeconds int64) (int64, error) {
	if olderThanSeconds <= 0 {
		olderThanSeconds = 7 * 24 * 3600
	}
	cutoff := GetDBTimestamp() - olderThanSeconds
	var deleted int64
	err := DB.Transaction(func(tx *gorm.DB) error {
		var candidates []SubscriptionPreConsumeRecord
		if err := lockForUpdate(tx).
			Select("id", "request_id", "user_id", "user_subscription_id", "status").
			Where("status IN ? AND updated_at < ?", []string{"settled", "refunded"}, cutoff).
			Order("id asc").
			Limit(subscriptionPreConsumeCleanupBatchSize).
			Find(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) == 0 {
			return nil
		}

		terminalRequests := make(map[string]struct{}, len(candidates))
		candidateUsers := make(map[int]struct{}, len(candidates))
		type subscriptionOwner struct {
			userID         int
			subscriptionID int
		}
		requestsByOwner := make(map[subscriptionOwner][]string, len(candidates))
		requestsByUser := make(map[int][]string, len(candidates))
		deleteIDs := make([]int, 0, len(candidates))
		for _, candidate := range candidates {
			terminalRequests[candidate.RequestId] = struct{}{}
			candidateUsers[candidate.UserId] = struct{}{}
			owner := subscriptionOwner{
				userID:         candidate.UserId,
				subscriptionID: candidate.UserSubscriptionId,
			}
			requestsByOwner[owner] = append(requestsByOwner[owner], candidate.RequestId)
			requestsByUser[candidate.UserId] = append(requestsByUser[candidate.UserId], candidate.RequestId)
		}

		protected := make(map[string]struct{}, len(terminalRequests))
		if len(terminalRequests) != 0 {
			requestIDs := make([]string, 0, len(terminalRequests))
			for requestID := range terminalRequests {
				requestIDs = append(requestIDs, requestID)
			}
			// A terminal task refund can mark the proof refunded before the
			// accepted submission's original zero-delta settlement outbox runs.
			// That settlement still needs the proof to close the pending durable
			// reservation, so refunded rows are not unconditionally disposable.
			var pendingReservations []BillingReservation
			if err := tx.Select("request_id").
				Where(
					"request_id IN ? AND (status IS NULL OR status IN ?)",
					requestIDs,
					[]string{"", billingReservationStatusPending},
				).
				Find(&pendingReservations).Error; err != nil {
				return err
			}
			for _, reservation := range pendingReservations {
				protected[reservation.RequestID] = struct{}{}
			}

			userIDs := make([]int, 0, len(candidateUsers))
			for userID := range candidateUsers {
				userIDs = append(userIDs, userID)
			}

			var lastTaskID int64
			for {
				var tasks []Task
				if err := tx.Select("id", "user_id", "private_data").
					Where(
						"id > ? AND user_id IN ? AND (status IS NULL OR status NOT IN ?)",
						lastTaskID,
						userIDs,
						[]TaskStatus{TaskStatusSuccess, TaskStatusFailure},
					).
					Order("id asc").
					Limit(subscriptionPreConsumeCleanupBatchSize).
					Find(&tasks).Error; err != nil {
					return err
				}
				for _, task := range tasks {
					if _, ok := terminalRequests[task.PrivateData.BillingRequestId]; ok {
						protected[task.PrivateData.BillingRequestId] = struct{}{}
						continue
					}
					if task.PrivateData.BillingRequestId != "" ||
						task.PrivateData.BillingSource == BillingAdjustmentWallet {
						continue
					}
					if task.PrivateData.SubscriptionId > 0 {
						owner := subscriptionOwner{
							userID:         task.UserId,
							subscriptionID: task.PrivateData.SubscriptionId,
						}
						for _, requestID := range requestsByOwner[owner] {
							protected[requestID] = struct{}{}
						}
						continue
					}
					// Legacy task rows can predate the persisted funding source
					// and request ID. With no narrower identity, retain every
					// candidate for that user rather than guess which quota
					// period may still need a refund.
					for _, requestID := range requestsByUser[task.UserId] {
						protected[requestID] = struct{}{}
					}
				}
				if len(tasks) < subscriptionPreConsumeCleanupBatchSize {
					break
				}
				lastTaskID = tasks[len(tasks)-1].ID
			}

			var lastMidjourneyID int
			for {
				var tasks []Midjourney
				if err := tx.Select(
					"id",
					"user_id",
					"status",
					"progress",
					"billing_request_id",
					"billing_purpose",
					"billing_source",
					"billing_subscription_id",
					"billing_finalized",
					"billing_refunded",
				).
					Where(
						`id > ? AND user_id IN ? AND (
							progress IS NULL OR progress <> ? OR
							(billing_purpose <> ? AND (
								billing_finalized IS NULL OR billing_finalized = ? OR
								((status IS NULL OR status <> ?) AND
									(billing_refunded IS NULL OR billing_refunded = ?))
							))
						)`,
						lastMidjourneyID,
						userIDs,
						"100%",
						"",
						false,
						"SUCCESS",
						false,
					).
					Order("id asc").
					Limit(subscriptionPreConsumeCleanupBatchSize).
					Find(&tasks).Error; err != nil {
					return err
				}
				for _, task := range tasks {
					if _, ok := terminalRequests[task.BillingRequestId]; ok {
						protected[task.BillingRequestId] = struct{}{}
						continue
					}
					if task.BillingRequestId != "" || task.BillingSource == BillingAdjustmentWallet {
						continue
					}
					if task.BillingSubscriptionId > 0 {
						owner := subscriptionOwner{
							userID:         task.UserId,
							subscriptionID: task.BillingSubscriptionId,
						}
						for _, requestID := range requestsByOwner[owner] {
							protected[requestID] = struct{}{}
						}
						continue
					}
					for _, requestID := range requestsByUser[task.UserId] {
						protected[requestID] = struct{}{}
					}
				}
				if len(tasks) < subscriptionPreConsumeCleanupBatchSize {
					break
				}
				lastMidjourneyID = tasks[len(tasks)-1].Id
			}

			var lastSystemTaskID int64
			for {
				var billingTasks []SystemTask
				if err := tx.Select("id", "task_id", "payload").
					Where(
						"id > ? AND type = ? AND (status IS NULL OR status <> ?)",
						lastSystemTaskID,
						SystemTaskTypeBillingAdjustment,
						SystemTaskStatusSucceeded,
					).
					Order("id asc").
					Limit(subscriptionPreConsumeCleanupBatchSize).
					Find(&billingTasks).Error; err != nil {
					return err
				}
				for _, billingTask := range billingTasks {
					var adjustment BillingAdjustment
					if err := common.UnmarshalJsonStr(billingTask.Payload, &adjustment); err != nil {
						return fmt.Errorf("inspect unresolved billing adjustment %s: %w", billingTask.TaskID, err)
					}
					if _, ok := terminalRequests[adjustment.SubscriptionRequestID]; ok {
						protected[adjustment.SubscriptionRequestID] = struct{}{}
					}
				}
				if len(billingTasks) < subscriptionPreConsumeCleanupBatchSize {
					break
				}
				lastSystemTaskID = billingTasks[len(billingTasks)-1].ID
			}

			var lastFinalizationID int64
			for {
				var finalizations []TaskBillingFinalization
				if err := tx.Select("id", "finalization_id", "payload").
					Where(
						"id > ? AND (main_status IS NULL OR main_status <> ? OR log_status IS NULL OR log_status <> ?)",
						lastFinalizationID,
						taskBillingFinalizationSucceeded,
						taskBillingFinalizationSucceeded,
					).
					Order("id asc").
					Limit(subscriptionPreConsumeCleanupBatchSize).
					Find(&finalizations).Error; err != nil {
					return err
				}
				for _, finalization := range finalizations {
					var payload TaskBillingFinalizationPayload
					if err := common.UnmarshalJsonStr(finalization.Payload, &payload); err != nil {
						return fmt.Errorf("inspect unresolved billing finalization %s: %w", finalization.FinalizationID, err)
					}
					if _, ok := terminalRequests[payload.Adjustment.SubscriptionRequestID]; ok {
						protected[payload.Adjustment.SubscriptionRequestID] = struct{}{}
					}
				}
				if len(finalizations) < subscriptionPreConsumeCleanupBatchSize {
					break
				}
				lastFinalizationID = finalizations[len(finalizations)-1].ID
			}
		}

		for _, candidate := range candidates {
			if _, keep := protected[candidate.RequestId]; !keep {
				deleteIDs = append(deleteIDs, candidate.Id)
			}
		}
		if len(deleteIDs) == 0 {
			return nil
		}
		result := tx.Where(
			"id IN ? AND status IN ? AND updated_at < ?",
			deleteIDs,
			[]string{"settled", "refunded"},
			cutoff,
		).Delete(&SubscriptionPreConsumeRecord{})
		deleted = result.RowsAffected
		return result.Error
	})
	return deleted, err
}

type SubscriptionPlanInfo struct {
	PlanId    int
	PlanTitle string
}

func GetSubscriptionPlanInfoByUserSubscriptionId(userSubscriptionId int) (*SubscriptionPlanInfo, error) {
	if userSubscriptionId <= 0 {
		return nil, errors.New("invalid userSubscriptionId")
	}
	cacheKey := fmt.Sprintf("sub:%d", userSubscriptionId)
	if cached, found, err := getSubscriptionPlanInfoCache().Get(cacheKey); err == nil && found {
		return &cached, nil
	}
	var sub UserSubscription
	if err := DB.Where("id = ?", userSubscriptionId).First(&sub).Error; err != nil {
		return nil, err
	}
	plan, err := getSubscriptionPlanByIdTx(nil, sub.PlanId)
	if err != nil {
		return nil, err
	}
	info := &SubscriptionPlanInfo{
		PlanId:    sub.PlanId,
		PlanTitle: plan.Title,
	}
	_ = getSubscriptionPlanInfoCache().SetWithTTL(cacheKey, *info, subscriptionPlanInfoCacheTTL())
	return info, nil
}

// Update subscription used amount by delta (positive consume more, negative refund).
func PostConsumeUserSubscriptionDelta(userSubscriptionId int, delta int64) error {
	if userSubscriptionId <= 0 {
		return errors.New("invalid userSubscriptionId")
	}
	if delta == 0 {
		return nil
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		return postConsumeUserSubscriptionDeltaTx(tx, userSubscriptionId, delta)
	})
}

func postConsumeUserSubscriptionDeltaTx(tx *gorm.DB, userSubscriptionId int, delta int64) error {
	var sub UserSubscription
	if err := lockForUpdate(tx).
		Where("id = ?", userSubscriptionId).
		First(&sub).Error; err != nil {
		return err
	}
	if delta > 0 && sub.AmountUsed > math.MaxInt64-delta {
		return errors.New("subscription used amount overflow")
	}
	if delta < 0 && sub.AmountUsed < math.MinInt64-delta {
		return errors.New("subscription used amount underflow")
	}
	newUsed := sub.AmountUsed + delta
	if newUsed < 0 {
		newUsed = 0
	}
	if sub.AmountTotal > 0 && newUsed > sub.AmountTotal {
		return fmt.Errorf("subscription used exceeds total, used=%d total=%d", newUsed, sub.AmountTotal)
	}
	return tx.Model(&sub).Update("amount_used", newUsed).Error
}
