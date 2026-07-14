package controller

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/checkout/session"
	"github.com/stripe/stripe-go/v81/webhook"
	"github.com/thanhpk/randstr"
)

var stripeAdaptor = &StripeAdaptor{}

// StripePayRequest represents a payment request for Stripe checkout.
type StripePayRequest struct {
	// Amount is the quantity of units to purchase.
	Amount int64 `json:"amount"`
	// PaymentMethod specifies the payment method (e.g., "stripe").
	PaymentMethod string `json:"payment_method"`
	// SuccessURL is the optional custom URL to redirect after successful payment.
	// If empty, defaults to the server's console log page.
	SuccessURL string `json:"success_url,omitempty"`
	// CancelURL is the optional custom URL to redirect when payment is canceled.
	// If empty, defaults to the server's console topup page.
	CancelURL string `json:"cancel_url,omitempty"`
}

type StripeAdaptor struct {
}

type stripeTopUpQuote struct {
	Quota    int
	PayMoney float64
	PayCents int64
}

func buildStripeTopUpQuote(amount int64, group string) (*stripeTopUpQuote, error) {
	amountIsTokens := operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens
	quota, err := model.TopUpQuotaForAmount(amount, amountIsTokens)
	if err != nil {
		return nil, err
	}
	if setting.StripeUnitPrice <= 0 || math.IsNaN(setting.StripeUnitPrice) || math.IsInf(setting.StripeUnitPrice, 0) {
		return nil, errors.New("Stripe 单价配置错误")
	}

	payUnits := decimal.NewFromInt(amount)
	if amountIsTokens {
		if common.QuotaPerUnit <= 0 {
			return nil, errors.New("额度单位配置错误")
		}
		payUnits = payUnits.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio <= 0 {
		topupGroupRatio = 1
	}
	if math.IsNaN(topupGroupRatio) || math.IsInf(topupGroupRatio, 0) {
		return nil, errors.New("充值分组倍率配置错误")
	}
	discount := 1.0
	if configured, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok && configured > 0 {
		if math.IsNaN(configured) || math.IsInf(configured, 0) {
			return nil, errors.New("充值折扣配置错误")
		}
		discount = configured
	}
	payCentsDecimal := payUnits.
		Mul(decimal.NewFromFloat(setting.StripeUnitPrice)).
		Mul(decimal.NewFromFloat(topupGroupRatio)).
		Mul(decimal.NewFromFloat(discount)).
		Mul(decimal.NewFromInt(100)).
		Round(0)
	if !payCentsDecimal.IsPositive() || payCentsDecimal.GreaterThan(decimal.NewFromInt(99999999)) {
		return nil, errors.New("Stripe 支付金额超出允许范围")
	}
	payCents := payCentsDecimal.IntPart()
	return &stripeTopUpQuote{
		Quota:    quota,
		PayMoney: decimal.NewFromInt(payCents).Div(decimal.NewFromInt(100)).InexactFloat64(),
		PayCents: payCents,
	}, nil
}

func (*StripeAdaptor) RequestAmount(c *gin.Context, req *StripePayRequest) {
	if req.Amount < getStripeMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getStripeMinTopup())})
		return
	}
	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	quote, err := buildStripeTopUpQuote(req.Amount, group)
	if err != nil || quote.PayCents < 1 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(quote.PayMoney, 'f', 2, 64)})
}

func (*StripeAdaptor) RequestPay(c *gin.Context, req *StripePayRequest) {
	if req.PaymentMethod != model.PaymentMethodStripe {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付渠道"})
		return
	}
	if req.Amount < getStripeMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": fmt.Sprintf("充值数量不能小于 %d", getStripeMinTopup()), "data": 10})
		return
	}
	if req.SuccessURL != "" && common.ValidateRedirectURL(req.SuccessURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "支付成功重定向URL不在可信任域名列表中", "data": ""})
		return
	}

	if req.CancelURL != "" && common.ValidateRedirectURL(req.CancelURL) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "支付取消重定向URL不在可信任域名列表中", "data": ""})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	quote, err := buildStripeTopUpQuote(req.Amount, group)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "error", "data": err.Error()})
		return
	}

	reference := fmt.Sprintf("new-api-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "ref_" + common.Sha1([]byte(reference))

	topUp := &model.TopUp{
		UserId:          id,
		Amount:          req.Amount,
		Quota:           quote.Quota,
		Money:           quote.PayMoney,
		TradeNo:         referenceId,
		PaymentMethod:   model.PaymentMethodStripe,
		PaymentProvider: model.PaymentProviderStripe,
		Currency:        "USD",
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, referenceId, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	payLink, err := genStripeLink(referenceId, user.StripeCustomer, user.Email, quote.PayCents, req.SuccessURL, req.CancelURL)
	if err != nil {
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Stripe 创建 Checkout Session 失败 user_id=%d trade_no=%s amount=%d error=%q", id, referenceId, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Stripe 充值订单创建成功 user_id=%d trade_no=%s amount=%d quota=%d money=%.2f", id, referenceId, req.Amount, quote.Quota, quote.PayMoney))
	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"pay_link": payLink,
		},
	})
}

func RequestStripeAmount(c *gin.Context) {
	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	stripeAdaptor.RequestAmount(c, &req)
}

func RequestStripePay(c *gin.Context) {
	var req StripePayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	stripeAdaptor.RequestPay(c, &req)
}

func StripeWebhook(c *gin.Context) {
	ctx := c.Request.Context()
	if !isStripeWebhookEnabled() {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.RequestURI, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, paymentWebhookMaxBodyBytes)
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	signature := c.GetHeader("Stripe-Signature")
	event, err := webhook.ConstructEventWithOptions(payload, signature, setting.StripeWebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})

	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 验签失败 path=%q client_ip=%s error=%q", c.Request.RequestURI, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	callerIp := c.ClientIP()
	logger.LogInfo(ctx, fmt.Sprintf("Stripe webhook 验签成功 event_type=%s client_ip=%s path=%q", string(event.Type), callerIp, c.Request.RequestURI))
	var processingErr error
	switch event.Type {
	case stripe.EventTypeCheckoutSessionCompleted:
		processingErr = sessionCompleted(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionExpired:
		processingErr = sessionExpired(ctx, event)
	case stripe.EventTypeCheckoutSessionAsyncPaymentSucceeded:
		processingErr = sessionAsyncPaymentSucceeded(ctx, event, callerIp)
	case stripe.EventTypeCheckoutSessionAsyncPaymentFailed:
		processingErr = sessionAsyncPaymentFailed(ctx, event, callerIp)
	case stripe.EventTypeInvoicePaid:
		processingErr = handleStripeInvoicePaid(event)
	case stripe.EventTypeInvoicePaymentFailed:
		processingErr = handleStripeInvoicePaymentFailed(event)
	case stripe.EventTypeCustomerSubscriptionUpdated, stripe.EventTypeCustomerSubscriptionDeleted:
		processingErr = handleStripeSubscriptionLifecycle(event)
	case stripe.EventTypeChargeRefunded:
		processingErr = handleStripeChargeRefunded(event)
	case stripe.EventTypeChargeDisputeCreated:
		processingErr = handleStripeDisputeCreated(event)
	default:
		logger.LogInfo(ctx, fmt.Sprintf("Stripe webhook 忽略事件 event_type=%s client_ip=%s", string(event.Type), callerIp))
	}
	if processingErr != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe webhook 处理失败 event_id=%s event_type=%s client_ip=%s error=%q", event.ID, string(event.Type), callerIp, processingErr.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	c.Status(http.StatusOK)
}

func sessionCompleted(ctx context.Context, event stripe.Event, callerIp string) error {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "complete" != status {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe checkout.completed 状态异常，忽略处理 trade_no=%s status=%s client_ip=%s", referenceId, status, callerIp))
		return nil
	}

	paymentStatus := event.GetObjectValue("payment_status")
	if paymentStatus != "paid" && !(setting.StripePromotionCodesEnabled && paymentStatus == "no_payment_required") {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe Checkout 支付未完成，等待异步结果 trade_no=%s payment_status=%s client_ip=%s", referenceId, paymentStatus, callerIp))
		return nil
	}

	return fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentSucceeded handles delayed payment methods (bank transfer, SEPA, etc.)
// that confirm payment after the checkout session completes.
func sessionAsyncPaymentSucceeded(ctx context.Context, event stripe.Event, callerIp string) error {
	customerId := event.GetObjectValue("customer")
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 异步支付成功 trade_no=%s client_ip=%s", referenceId, callerIp))

	return fulfillOrder(ctx, event, referenceId, customerId, callerIp)
}

// sessionAsyncPaymentFailed marks orders as failed when delayed payment methods
// ultimately fail (e.g. bank transfer not received, SEPA rejected).
func sessionAsyncPaymentFailed(ctx context.Context, event stripe.Event, callerIp string) error {
	referenceId := event.GetObjectValue("client_reference_id")
	logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败 trade_no=%s client_ip=%s", referenceId, callerIp))

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败事件缺少订单号 client_ip=%s", callerIp))
		return nil
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)

	topUp := model.GetTopUpByTradeNo(referenceId)
	if topUp == nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败但本地订单不存在 trade_no=%s client_ip=%s", referenceId, callerIp))
		return nil
	}

	if topUp.PaymentProvider != model.PaymentProviderStripe {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败但订单支付网关不匹配 trade_no=%s payment_provider=%s client_ip=%s", referenceId, topUp.PaymentProvider, callerIp))
		return nil
	}

	if topUp.Status != common.TopUpStatusPending {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 异步支付失败但订单状态非 pending，忽略处理 trade_no=%s status=%s client_ip=%s", referenceId, topUp.Status, callerIp))
		return nil
	}

	topUp.Status = common.TopUpStatusFailed
	if err := topUp.Update(); err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 标记充值订单失败状态失败 trade_no=%s client_ip=%s error=%q", referenceId, callerIp, err.Error()))
		return err
	}
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 充值订单已标记为失败 trade_no=%s client_ip=%s", referenceId, callerIp))
	return nil
}

// fulfillOrder is the shared logic for crediting quota after payment is confirmed.
func fulfillOrder(ctx context.Context, event stripe.Event, referenceId string, customerId string, callerIp string) error {
	if len(referenceId) == 0 {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 完成订单时缺少订单号 client_ip=%s", callerIp))
		return errors.New("Stripe 事件缺少 client_reference_id")
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	payload := map[string]any{
		"event_id":     event.ID,
		"customer":     customerId,
		"amount_total": event.GetObjectValue("amount_total"),
		"currency":     strings.ToUpper(event.GetObjectValue("currency")),
		"event_type":   string(event.Type),
	}
	if subscriptionId := event.GetObjectValue("subscription"); subscriptionId != "" {
		if err := model.SetSubscriptionOrderProviderIdentifiers(
			referenceId,
			model.PaymentProviderStripe,
			customerId,
			subscriptionId,
			event.GetObjectValue("payment_intent"),
			"active",
			common.GetJsonString(payload),
		); err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
			return err
		}
	}
	if err := model.CompleteSubscriptionOrder(referenceId, common.GetJsonString(payload), model.PaymentProviderStripe, ""); err == nil {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 订阅订单处理成功 trade_no=%s event_type=%s client_ip=%s", referenceId, string(event.Type), callerIp))
		return nil
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(ctx, fmt.Sprintf("Stripe 订阅订单处理失败 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, err.Error()))
		return err
	}
	topUp := model.GetTopUpByTradeNo(referenceId)
	if topUp == nil || topUp.PaymentProvider != model.PaymentProviderStripe {
		return model.ErrTopUpNotFound
	}
	amountTotal, err := strconv.ParseInt(event.GetObjectValue("amount_total"), 10, 64)
	if err != nil || amountTotal < 0 || !strings.EqualFold(event.GetObjectValue("currency"), "USD") {
		return errors.New("Stripe checkout amount or currency is invalid")
	}
	expectedCents := decimal.NewFromFloat(topUp.Money).Mul(decimal.NewFromInt(100)).Round(0).IntPart()
	if amountTotal > expectedCents || (!setting.StripePromotionCodesEnabled && amountTotal != expectedCents) {
		return fmt.Errorf("Stripe checkout amount mismatch: expected=%d actual=%d", expectedCents, amountTotal)
	}

	err = model.Recharge(referenceId, customerId, event.GetObjectValue("payment_intent"), callerIp)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值处理失败 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, err.Error()))
		return err
	}

	total, _ := strconv.ParseFloat(event.GetObjectValue("amount_total"), 64)
	currency := strings.ToUpper(event.GetObjectValue("currency"))
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 充值成功 trade_no=%s amount_total=%.2f currency=%s event_type=%s client_ip=%s", referenceId, total/100, currency, string(event.Type), callerIp))
	return nil
}

func handleStripeInvoicePaid(event stripe.Event) error {
	var invoice stripe.Invoice
	if event.Data == nil || common.Unmarshal(event.Data.Raw, &invoice) != nil {
		return errors.New("invalid Stripe invoice payload")
	}
	subscriptionId := ""
	if invoice.Subscription != nil {
		subscriptionId = invoice.Subscription.ID
	}
	if subscriptionId == "" {
		subscriptionId = event.GetObjectValue("parent", "subscription_details", "subscription")
	}
	if subscriptionId == "" {
		return nil
	}
	paymentId := ""
	if invoice.PaymentIntent != nil {
		paymentId = invoice.PaymentIntent.ID
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "invoice_id": invoice.ID,
		"billing_reason": string(invoice.BillingReason), "amount_paid": invoice.AmountPaid,
		"currency": strings.ToUpper(string(invoice.Currency)),
	})
	if string(invoice.BillingReason) == "subscription_create" {
		return model.SetProviderSubscriptionPayment(model.PaymentProviderStripe, subscriptionId, paymentId, "active", payload)
	}
	if string(invoice.BillingReason) != "subscription_cycle" {
		return nil
	}
	return model.RenewProviderSubscription(
		model.PaymentProviderStripe,
		subscriptionId,
		invoice.ID,
		paymentId,
		float64(invoice.AmountPaid)/100,
		payload,
	)
}

func handleStripeInvoicePaymentFailed(event stripe.Event) error {
	var invoice stripe.Invoice
	if event.Data == nil || common.Unmarshal(event.Data.Raw, &invoice) != nil {
		return errors.New("invalid Stripe invoice payload")
	}
	subscriptionId := ""
	if invoice.Subscription != nil {
		subscriptionId = invoice.Subscription.ID
	}
	if subscriptionId == "" {
		subscriptionId = event.GetObjectValue("parent", "subscription_details", "subscription")
	}
	if subscriptionId == "" {
		return nil
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "invoice_id": invoice.ID,
	})
	return model.UpdateProviderSubscriptionStatus(model.PaymentProviderStripe, subscriptionId, "past_due", payload)
}

func handleStripeSubscriptionLifecycle(event stripe.Event) error {
	var subscription stripe.Subscription
	if event.Data == nil || common.Unmarshal(event.Data.Raw, &subscription) != nil || subscription.ID == "" {
		return errors.New("invalid Stripe subscription payload")
	}
	status := string(subscription.Status)
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "status": status,
	})
	switch status {
	case "canceled", "unpaid", "incomplete_expired", "paused":
		return model.CancelProviderSubscription(model.PaymentProviderStripe, subscription.ID, status, payload)
	default:
		return model.UpdateProviderSubscriptionStatus(model.PaymentProviderStripe, subscription.ID, status, payload)
	}
}

func handleStripeChargeRefunded(event stripe.Event) error {
	var charge stripe.Charge
	if event.Data == nil || common.Unmarshal(event.Data.Raw, &charge) != nil {
		return errors.New("invalid Stripe charge payload")
	}
	paymentId := ""
	if charge.PaymentIntent != nil {
		paymentId = charge.PaymentIntent.ID
	}
	if paymentId == "" {
		return nil
	}
	full := charge.Refunded
	err := model.ReverseTopUpByProviderPayment(model.PaymentProviderStripe, paymentId, float64(charge.AmountRefunded)/100, full)
	if err == nil {
		return nil
	}
	if !errors.Is(err, model.ErrTopUpNotFound) {
		return err
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "charge_id": charge.ID,
	})
	return model.CancelProviderSubscriptionByPayment(model.PaymentProviderStripe, paymentId, "refunded", payload)
}

func handleStripeDisputeCreated(event stripe.Event) error {
	var dispute stripe.Dispute
	if event.Data == nil || common.Unmarshal(event.Data.Raw, &dispute) != nil {
		return errors.New("invalid Stripe dispute payload")
	}
	paymentId := ""
	if dispute.PaymentIntent != nil {
		paymentId = dispute.PaymentIntent.ID
	} else if dispute.Charge != nil && dispute.Charge.PaymentIntent != nil {
		paymentId = dispute.Charge.PaymentIntent.ID
	}
	if paymentId == "" {
		return nil
	}
	err := model.ReverseTopUpByProviderPayment(model.PaymentProviderStripe, paymentId, 0, true)
	if err == nil {
		return nil
	}
	if !errors.Is(err, model.ErrTopUpNotFound) {
		return err
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "dispute_id": dispute.ID,
	})
	return model.CancelProviderSubscriptionByPayment(model.PaymentProviderStripe, paymentId, "disputed", payload)
}

func sessionExpired(ctx context.Context, event stripe.Event) error {
	referenceId := event.GetObjectValue("client_reference_id")
	status := event.GetObjectValue("status")
	if "expired" != status {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe checkout.expired 状态异常，忽略处理 trade_no=%s status=%s", referenceId, status))
		return nil
	}

	if len(referenceId) == 0 {
		logger.LogWarn(ctx, "Stripe checkout.expired 缺少订单号")
		return nil
	}

	// Subscription order expiration
	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	if err := model.ExpireSubscriptionOrder(referenceId, model.PaymentProviderStripe); err == nil {
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 订阅订单已过期 trade_no=%s", referenceId))
		return nil
	} else if err != nil && !errors.Is(err, model.ErrSubscriptionOrderNotFound) {
		logger.LogError(ctx, fmt.Sprintf("Stripe 订阅订单过期处理失败 trade_no=%s error=%q", referenceId, err.Error()))
		return err
	}

	err := model.UpdatePendingTopUpStatus(referenceId, model.PaymentProviderStripe, common.TopUpStatusExpired)
	if errors.Is(err, model.ErrTopUpNotFound) {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 充值订单不存在，无法标记过期 trade_no=%s", referenceId))
		return nil
	}
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值订单过期处理失败 trade_no=%s error=%q", referenceId, err.Error()))
		return err
	}

	logger.LogInfo(ctx, fmt.Sprintf("Stripe 充值订单已过期 trade_no=%s", referenceId))
	return nil
}

// genStripeLink generates a Stripe Checkout session URL for payment.
// It creates a new checkout session with the specified parameters and returns the payment URL.
//
// Parameters:
//   - referenceId: unique reference identifier for the transaction
//   - customerId: existing Stripe customer ID (empty string if new customer)
//   - email: customer email address for new customer creation
//   - amount: quantity of units to purchase
//   - successURL: custom URL to redirect after successful payment (empty for default)
//   - cancelURL: custom URL to redirect when payment is canceled (empty for default)
//
// Returns the checkout session URL or an error if the session creation fails.
func genStripeLink(referenceId string, customerId string, email string, payCents int64, successURL string, cancelURL string) (string, error) {
	if !strings.HasPrefix(setting.StripeApiSecret, "sk_") && !strings.HasPrefix(setting.StripeApiSecret, "rk_") {
		return "", fmt.Errorf("无效的Stripe API密钥")
	}

	stripe.Key = setting.StripeApiSecret

	// Use custom URLs if provided, otherwise use defaults
	if successURL == "" {
		successURL = paymentReturnPath("/console/log")
	}
	if cancelURL == "" {
		cancelURL = paymentReturnPath("/console/topup")
	}

	params := &stripe.CheckoutSessionParams{
		ClientReferenceID: stripe.String(referenceId),
		SuccessURL:        stripe.String(successURL),
		CancelURL:         stripe.String(cancelURL),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				PriceData: &stripe.CheckoutSessionLineItemPriceDataParams{
					Currency: stripe.String(string(stripe.CurrencyUSD)),
					ProductData: &stripe.CheckoutSessionLineItemPriceDataProductDataParams{
						Name: stripe.String("API quota top-up"),
					},
					UnitAmount: stripe.Int64(payCents),
				},
				Quantity: stripe.Int64(1),
			},
		},
		Mode:                stripe.String(string(stripe.CheckoutSessionModePayment)),
		AllowPromotionCodes: stripe.Bool(setting.StripePromotionCodesEnabled),
	}
	params.AddMetadata("trade_no", referenceId)

	if "" == customerId {
		if "" != email {
			params.CustomerEmail = stripe.String(email)
		}

		params.CustomerCreation = stripe.String(string(stripe.CheckoutSessionCustomerCreationAlways))
	} else {
		params.Customer = stripe.String(customerId)
	}

	result, err := session.New(params)
	if err != nil {
		return "", err
	}

	return result.URL, nil
}

func getStripePayMoney(amount float64, group string) float64 {
	quote, err := buildStripeTopUpQuote(int64(amount), group)
	if err != nil {
		return 0
	}
	return quote.PayMoney
}

func getStripeMinTopup() int64 {
	minTopup := setting.StripeMinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		value := decimal.NewFromInt(int64(minTopup)).Mul(decimal.NewFromFloat(common.QuotaPerUnit))
		if value.GreaterThan(decimal.NewFromInt(common.MaxQuota)) {
			return int64(common.MaxQuota)
		}
		return value.Ceil().IntPart()
	}
	return int64(minTopup)
}
