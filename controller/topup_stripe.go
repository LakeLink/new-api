package controller

import (
	"context"
	"encoding/json"
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
	stripeSetting := setting.GetStripeSettings()
	if stripeSetting.UnitPrice <= 0 || math.IsNaN(stripeSetting.UnitPrice) || math.IsInf(stripeSetting.UnitPrice, 0) {
		return nil, errors.New("Stripe 单价配置错误")
	}

	payUnits := decimal.NewFromInt(amount)
	if amountIsTokens {
		quotaPerUnit := common.CurrentQuotaPerUnit()
		if quotaPerUnit <= 0 ||
			math.IsNaN(quotaPerUnit) ||
			math.IsInf(quotaPerUnit, 0) ||
			quotaPerUnit > float64(common.MaxQuota) {
			return nil, errors.New("额度单位配置错误")
		}
		payUnits = payUnits.Div(decimal.NewFromFloat(quotaPerUnit))
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio <= 0 {
		topupGroupRatio = 1
	}
	if math.IsNaN(topupGroupRatio) || math.IsInf(topupGroupRatio, 0) {
		return nil, errors.New("充值分组倍率配置错误")
	}
	discount := 1.0
	if configured, ok := lookupPaymentAmountDiscount(operation_setting.GetPaymentSetting().AmountDiscount, amount); ok && configured > 0 {
		if math.IsNaN(configured) || math.IsInf(configured, 0) {
			return nil, errors.New("充值折扣配置错误")
		}
		discount = configured
	}
	payCentsDecimal := payUnits.
		Mul(decimal.NewFromFloat(stripeSetting.UnitPrice)).
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
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, paymentWebhookMaxBodyBytes)
	payload, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		if isPaymentWebhookBodyTooLarge(err) {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		c.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}

	signature := c.GetHeader("Stripe-Signature")
	stripeSetting := setting.GetStripeSettings()
	event, err := webhook.ConstructEventWithOptions(payload, signature, stripeSetting.WebhookSecret, webhook.ConstructEventOptions{
		IgnoreAPIVersionMismatch: true,
	})

	if err != nil {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe webhook 验签失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if event.ID == "" || event.Created <= 0 {
		logger.LogWarn(ctx, fmt.Sprintf(
			"Stripe webhook 缺少有效事件标识或时间 event_id=%q created=%d",
			event.ID,
			event.Created,
		))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	callerIp := c.ClientIP()
	logger.LogInfo(ctx, fmt.Sprintf("Stripe webhook 验签成功 event_type=%s client_ip=%s path=%q", string(event.Type), callerIp, c.Request.URL.Path))
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
	if paymentStatus != "paid" && !(setting.GetStripeSettings().PromotionCodesEnabled && paymentStatus == "no_payment_required") {
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

	topUp, err := model.FindTopUpByTradeNo(referenceId)
	if errors.Is(err, model.ErrTopUpNotFound) {
		logger.LogWarn(ctx, fmt.Sprintf("Stripe 异步支付失败但本地订单不存在 trade_no=%s client_ip=%s", referenceId, callerIp))
		return nil
	}
	if err != nil {
		return err
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
		order, err := model.FindSubscriptionOrderByTradeNo(referenceId)
		if err != nil {
			return err
		}
		if order.PaymentProvider != model.PaymentProviderStripe {
			return model.ErrPaymentMethodMismatch
		}
		amountTotal, err := strconv.ParseInt(event.GetObjectValue("amount_total"), 10, 64)
		if err != nil || amountTotal < 0 || !strings.EqualFold(event.GetObjectValue("currency"), "USD") {
			return errors.New("Stripe subscription checkout amount or currency is invalid")
		}
		expectedCents := decimal.NewFromFloat(order.Money).Mul(decimal.NewFromInt(100)).Round(0)
		if !expectedCents.IsPositive() ||
			expectedCents.GreaterThan(decimal.NewFromInt(math.MaxInt64)) ||
			amountTotal != expectedCents.IntPart() {
			return fmt.Errorf(
				"Stripe subscription checkout amount mismatch: expected=%s actual=%d",
				expectedCents.String(),
				amountTotal,
			)
		}
		if err := model.SetSubscriptionOrderProviderIdentifiersAt(
			referenceId,
			model.PaymentProviderStripe,
			customerId,
			subscriptionId,
			event.GetObjectValue("payment_intent"),
			"active",
			event.Created,
			common.GetJsonString(payload),
		); err != nil {
			return err
		}
		if err := model.CompleteSubscriptionOrder(
			referenceId,
			common.GetJsonString(payload),
			model.PaymentProviderStripe,
			"",
		); err != nil {
			return err
		}
		logger.LogInfo(ctx, fmt.Sprintf("Stripe 订阅订单处理成功 trade_no=%s event_type=%s client_ip=%s", referenceId, string(event.Type), callerIp))
		return nil
	}
	topUp, err := model.FindTopUpByTradeNo(referenceId)
	if err != nil {
		return err
	}
	if topUp.PaymentProvider != model.PaymentProviderStripe {
		return model.ErrPaymentMethodMismatch
	}
	amountTotal, err := strconv.ParseInt(event.GetObjectValue("amount_total"), 10, 64)
	if err != nil || amountTotal < 0 || !strings.EqualFold(event.GetObjectValue("currency"), "USD") {
		return errors.New("Stripe checkout amount or currency is invalid")
	}
	if math.IsNaN(topUp.Money) || math.IsInf(topUp.Money, 0) || topUp.Money <= 0 {
		return errors.New("Stripe checkout quote is invalid")
	}
	expectedCentsDecimal := decimal.NewFromFloat(topUp.Money).Mul(decimal.NewFromInt(100)).Round(0)
	if !expectedCentsDecimal.IsPositive() ||
		expectedCentsDecimal.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return errors.New("Stripe checkout quote is invalid")
	}
	expectedCents := expectedCentsDecimal.IntPart()
	if amountTotal > expectedCents || (!setting.GetStripeSettings().PromotionCodesEnabled && amountTotal != expectedCents) {
		return fmt.Errorf("Stripe checkout amount mismatch: expected=%d actual=%d", expectedCents, amountTotal)
	}

	err = model.Recharge(referenceId, customerId, event.GetObjectValue("payment_intent"), amountTotal, callerIp)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("Stripe 充值处理失败 trade_no=%s event_type=%s client_ip=%s error=%q", referenceId, string(event.Type), callerIp, err.Error()))
		return err
	}

	total, _ := strconv.ParseFloat(event.GetObjectValue("amount_total"), 64)
	currency := strings.ToUpper(event.GetObjectValue("currency"))
	logger.LogInfo(ctx, fmt.Sprintf("Stripe 充值成功 trade_no=%s amount_total=%.2f currency=%s event_type=%s client_ip=%s", referenceId, total/100, currency, string(event.Type), callerIp))
	return nil
}

type stripeInvoicePaymentsPayload struct {
	Payments struct {
		HasMore bool `json:"has_more"`
		Data    []struct {
			AmountPaid int64  `json:"amount_paid"`
			IsDefault  bool   `json:"is_default"`
			Status     string `json:"status"`
			Payment    struct {
				PaymentIntent json.RawMessage `json:"payment_intent"`
				Charge        json.RawMessage `json:"charge"`
				PaymentRecord json.RawMessage `json:"payment_record"`
			} `json:"payment"`
		} `json:"data"`
	} `json:"payments"`
}

func stripeExpandableObjectID(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var id string
	if err := common.Unmarshal(raw, &id); err == nil {
		return id
	}
	var object struct {
		ID string `json:"id"`
	}
	if err := common.Unmarshal(raw, &object); err != nil {
		return ""
	}
	return object.ID
}

// stripeInvoicePaymentIdentifiers supports both legacy
// invoice.payment_intent payloads and Basil/Clover invoice.payments payloads.
func stripeInvoicePaymentIdentifiers(event stripe.Event, invoice *stripe.Invoice) ([]string, bool) {
	if invoice == nil {
		return nil, false
	}
	identifiers := make([]string, 0, 4)
	seen := map[string]struct{}{}
	appendIdentifier := func(identifier string) {
		if identifier == "" {
			return
		}
		if _, exists := seen[identifier]; exists {
			return
		}
		seen[identifier] = struct{}{}
		identifiers = append(identifiers, identifier)
	}
	if invoice.PaymentIntent != nil && invoice.PaymentIntent.ID != "" {
		appendIdentifier(invoice.PaymentIntent.ID)
	}
	if invoice.Charge != nil && invoice.Charge.ID != "" {
		appendIdentifier(invoice.Charge.ID)
	}
	if event.Data == nil {
		return identifiers, false
	}
	var payload stripeInvoicePaymentsPayload
	if err := common.Unmarshal(event.Data.Raw, &payload); err != nil {
		return identifiers, false
	}
	defaultIdentifiers := make([]string, 0, len(payload.Payments.Data))
	otherIdentifiers := make([]string, 0, len(payload.Payments.Data))
	for _, invoicePayment := range payload.Payments.Data {
		if invoicePayment.Status != "paid" || invoicePayment.AmountPaid <= 0 {
			continue
		}
		id := stripeExpandableObjectID(invoicePayment.Payment.PaymentIntent)
		if id == "" {
			id = stripeExpandableObjectID(invoicePayment.Payment.Charge)
		}
		if id == "" {
			id = stripeExpandableObjectID(invoicePayment.Payment.PaymentRecord)
		}
		if id == "" {
			continue
		}
		if invoicePayment.IsDefault {
			defaultIdentifiers = append(defaultIdentifiers, id)
		} else {
			otherIdentifiers = append(otherIdentifiers, id)
		}
	}
	for _, identifier := range defaultIdentifiers {
		appendIdentifier(identifier)
	}
	for _, identifier := range otherIdentifiers {
		appendIdentifier(identifier)
	}
	return identifiers, payload.Payments.HasMore
}

func stripeInvoicePaymentIdentifier(event stripe.Event, invoice *stripe.Invoice) string {
	identifiers, _ := stripeInvoicePaymentIdentifiers(event, invoice)
	if len(identifiers) == 0 {
		return ""
	}
	return identifiers[0]
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
	if invoice.AmountPaid <= 0 || !strings.EqualFold(string(invoice.Currency), "USD") {
		return errors.New("Stripe subscription invoice amount or currency is invalid")
	}
	paymentIDs, hasMorePayments := stripeInvoicePaymentIdentifiers(event, &invoice)
	if hasMorePayments {
		return errors.New("Stripe invoice contains more payment references than the webhook payload")
	}
	paymentId := ""
	if len(paymentIDs) > 0 {
		paymentId = paymentIDs[0]
	}
	if paymentId == "" {
		return errors.New("Stripe paid subscription invoice has no traceable payment reference")
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "invoice_id": invoice.ID,
		"billing_reason": string(invoice.BillingReason), "amount_paid": invoice.AmountPaid,
		"currency": strings.ToUpper(string(invoice.Currency)),
	})
	if string(invoice.BillingReason) == "subscription_create" {
		if err := model.SetProviderSubscriptionPaymentAt(
			model.PaymentProviderStripe,
			subscriptionId,
			paymentId,
			"active",
			event.Created,
			payload,
		); err != nil {
			return err
		}
		return model.BindSubscriptionProviderPayments(
			model.PaymentProviderStripe,
			subscriptionId,
			"",
			paymentIDs,
		)
	}
	if string(invoice.BillingReason) != "subscription_cycle" {
		return nil
	}
	return model.RenewProviderSubscriptionWithPaymentsAt(
		model.PaymentProviderStripe,
		subscriptionId,
		invoice.ID,
		paymentId,
		paymentIDs,
		float64(invoice.AmountPaid)/100,
		event.Created,
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
	return model.UpdateProviderSubscriptionStatusAt(
		model.PaymentProviderStripe,
		subscriptionId,
		"past_due",
		event.Created,
		payload,
	)
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
		return model.CancelProviderSubscriptionAt(
			model.PaymentProviderStripe,
			subscription.ID,
			status,
			event.Created,
			payload,
		)
	default:
		return model.UpdateProviderSubscriptionStatusAt(
			model.PaymentProviderStripe,
			subscription.ID,
			status,
			event.Created,
			payload,
		)
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
		paymentId = charge.ID
	}
	invoiceID := ""
	if charge.Invoice != nil {
		invoiceID = charge.Invoice.ID
	}
	full := charge.Refunded
	if paymentId != "" {
		err := model.ReverseTopUpByProviderPayment(model.PaymentProviderStripe, paymentId, float64(charge.AmountRefunded)/100, full)
		if err == nil {
			return nil
		}
		if !errors.Is(err, model.ErrTopUpNotFound) {
			return err
		}
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "charge_id": charge.ID,
	})
	refund := model.SubscriptionRefundInput{
		EventID:           event.ID,
		AmountMinor:       charge.AmountRefunded,
		AmountMode:        model.ProviderRefundAmountCumulative,
		AmountScope:       charge.ID,
		ProviderEventTime: event.Created,
		ProviderPayload:   payload,
	}
	// SubscriptionOrder.Money is the validated checkout/invoice total. Do not
	// replace that invoice-wide threshold with charge.Amount: current Stripe
	// invoices can be funded by multiple partial payments.
	if paymentId != "" {
		err := model.ApplySubscriptionRefundByProviderPayment(
			model.PaymentProviderStripe,
			paymentId,
			refund,
		)
		if err == nil || !errors.Is(err, model.ErrSubscriptionOrderNotFound) || invoiceID == "" {
			return err
		}
	}
	if invoiceID != "" {
		return model.ApplySubscriptionRefundByProviderInvoice(
			model.PaymentProviderStripe,
			invoiceID,
			refund,
		)
	}
	return nil
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
	if paymentId == "" && dispute.Charge != nil {
		paymentId = dispute.Charge.ID
	}
	if paymentId == "" {
		if dispute.Charge == nil || dispute.Charge.Invoice == nil {
			return nil
		}
	}
	if paymentId != "" {
		err := model.ReverseTopUpByProviderPayment(model.PaymentProviderStripe, paymentId, 0, true)
		if err == nil {
			return nil
		}
		if !errors.Is(err, model.ErrTopUpNotFound) {
			return err
		}
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.ID, "event_type": string(event.Type), "dispute_id": dispute.ID,
	})
	refund := model.SubscriptionRefundInput{
		EventID:           event.ID,
		AmountMinor:       dispute.Amount,
		AmountMode:        model.ProviderRefundAmountCumulative,
		AmountScope:       paymentId,
		Full:              true,
		ProviderEventTime: event.Created,
		ProviderPayload:   payload,
	}
	if paymentId != "" {
		err := model.ApplySubscriptionRefundByProviderPayment(
			model.PaymentProviderStripe,
			paymentId,
			refund,
		)
		if err == nil || !errors.Is(err, model.ErrSubscriptionOrderNotFound) ||
			dispute.Charge == nil || dispute.Charge.Invoice == nil {
			return err
		}
	}
	return model.ApplySubscriptionRefundByProviderInvoice(
		model.PaymentProviderStripe,
		dispute.Charge.Invoice.ID,
		refund,
	)
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
	stripeSetting := setting.GetStripeSettings()
	if !strings.HasPrefix(stripeSetting.APISecret, "sk_") && !strings.HasPrefix(stripeSetting.APISecret, "rk_") {
		return "", fmt.Errorf("无效的Stripe API密钥")
	}

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
		AllowPromotionCodes: stripe.Bool(stripeSetting.PromotionCodesEnabled),
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

	stripeClient := session.Client{
		B:   stripe.GetBackend(stripe.APIBackend),
		Key: stripeSetting.APISecret,
	}
	result, err := stripeClient.New(params)
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
	minTopup := setting.GetStripeSettings().MinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return tokenDisplayMinimum(int64(minTopup))
	}
	return int64(minTopup)
}
