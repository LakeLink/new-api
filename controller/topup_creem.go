package controller

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
)

const CreemSignatureHeader = "creem-signature"

var creemAdaptor = &CreemAdaptor{}

// 生成HMAC-SHA256签名
func generateCreemSignature(payload string, secret string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// 验证Creem webhook签名
func verifyCreemSignature(payload string, signature string, secret string) bool {
	if secret == "" {
		return false
	}

	expectedSignature := generateCreemSignature(payload, secret)
	return hmac.Equal([]byte(signature), []byte(expectedSignature))
}

type CreemPayRequest struct {
	ProductId     string `json:"product_id"`
	PaymentMethod string `json:"payment_method"`
}

type CreemProduct struct {
	ProductId string  `json:"productId"`
	Name      string  `json:"name"`
	Price     float64 `json:"price"`
	Currency  string  `json:"currency"`
	Quota     int64   `json:"quota"`
}

type CreemAdaptor struct {
}

func creemAmountCents(price float64) (int, error) {
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, errors.New("Creem product price must be finite and positive")
	}
	cents := decimal.NewFromFloat(price).Mul(decimal.NewFromInt(100)).Round(0)
	if !cents.IsPositive() || cents.GreaterThan(decimal.NewFromInt(math.MaxInt32)) {
		return 0, errors.New("Creem product price exceeds the supported cent range")
	}
	return int(cents.IntPart()), nil
}

func (*CreemAdaptor) RequestPay(c *gin.Context, req *CreemPayRequest) {
	if req.PaymentMethod != model.PaymentMethodCreem {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付渠道"})
		return
	}
	if !isCreemTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Creem 支付或 Webhook 未完整配置"})
		return
	}

	if req.ProductId == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "请选择产品"})
		return
	}

	// 解析产品列表
	var products []CreemProduct
	creemSetting := setting.GetCreemSettings()
	err := common.Unmarshal([]byte(creemSetting.Products), &products)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 产品配置解析失败 user_id=%d error=%q", c.GetInt("id"), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "产品配置错误"})
		return
	}

	// 查找对应的产品
	var selectedProduct *CreemProduct
	for _, product := range products {
		if product.ProductId == req.ProductId {
			selectedProduct = &product
			break
		}
	}

	if selectedProduct == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "产品不存在"})
		return
	}
	if _, err := creemAmountCents(selectedProduct.Price); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "error", "data": "产品价格配置错误"})
		return
	}
	creditQuota, err := model.TopUpQuotaForAmount(selectedProduct.Quota, true)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "error", "data": "产品充值额度超出允许范围"})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	// 生成唯一的订单引用ID
	reference := fmt.Sprintf("creem-api-ref-%d-%d-%s", user.Id, time.Now().UnixMilli(), randstr.String(4))
	referenceId := "ref_" + common.Sha1([]byte(reference))

	// 先创建订单记录，使用产品配置的金额和充值额度
	topUp := &model.TopUp{
		UserId:            id,
		Amount:            selectedProduct.Quota, // 充值额度
		Quota:             creditQuota,
		Money:             selectedProduct.Price, // 支付金额
		TradeNo:           referenceId,
		PaymentMethod:     model.PaymentMethodCreem,
		PaymentProvider:   model.PaymentProviderCreem,
		ProviderProductId: selectedProduct.ProductId,
		Currency:          strings.ToUpper(selectedProduct.Currency),
		CreateTime:        time.Now().Unix(),
		Status:            common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 创建充值订单失败 user_id=%d trade_no=%s product_id=%s error=%q", id, referenceId, selectedProduct.ProductId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	// 创建支付链接，传入用户邮箱
	checkoutUrl, err := genCreemLink(c.Request.Context(), referenceId, selectedProduct, user.Email, user.Username)
	if err != nil {
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 创建支付链接失败 user_id=%d trade_no=%s product_id=%s error=%q", id, referenceId, selectedProduct.ProductId, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 充值订单创建成功 user_id=%d trade_no=%s product_id=%s product_name=%q quota=%d money=%.2f", id, referenceId, selectedProduct.ProductId, selectedProduct.Name, selectedProduct.Quota, selectedProduct.Price))

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"checkout_url": checkoutUrl,
			"order_id":     referenceId,
		},
	})
}

func RequestCreemPay(c *gin.Context) {
	var req CreemPayRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	creemAdaptor.RequestPay(c, &req)
}

// 新的Creem Webhook结构体，匹配实际的webhook数据格式
type CreemWebhookEvent struct {
	Id        string `json:"id"`
	EventType string `json:"eventType"`
	CreatedAt int64  `json:"created_at"`
	Object    struct {
		Id        string `json:"id"`
		Object    string `json:"object"`
		RequestId string `json:"request_id"`
		Order     struct {
			Object      string `json:"object"`
			Id          string `json:"id"`
			Customer    string `json:"customer"`
			Product     string `json:"product"`
			Amount      int    `json:"amount"`
			Currency    string `json:"currency"`
			SubTotal    int    `json:"sub_total"`
			TaxAmount   int    `json:"tax_amount"`
			AmountDue   int    `json:"amount_due"`
			AmountPaid  *int64 `json:"amount_paid,omitempty"`
			Status      string `json:"status"`
			Type        string `json:"type"`
			Transaction string `json:"transaction"`
			CreatedAt   string `json:"created_at"`
			UpdatedAt   string `json:"updated_at"`
			Mode        string `json:"mode"`
		} `json:"order"`
		Subscription struct {
			Id     string `json:"id"`
			Status string `json:"status"`
		} `json:"subscription"`
		Checkout struct {
			RequestId string `json:"request_id"`
		} `json:"checkout"`
		Transaction struct {
			Id             string `json:"id"`
			AmountPaid     int64  `json:"amount_paid"`
			RefundedAmount int64  `json:"refunded_amount"`
			Subscription   string `json:"subscription"`
		} `json:"transaction"`
		LastTransaction struct {
			Id         string `json:"id"`
			AmountPaid int64  `json:"amount_paid"`
			Currency   string `json:"currency"`
			Status     string `json:"status"`
		} `json:"last_transaction"`
		Product struct {
			Id                string  `json:"id"`
			Object            string  `json:"object"`
			Name              string  `json:"name"`
			Description       string  `json:"description"`
			Price             int64   `json:"price"`
			Currency          string  `json:"currency"`
			BillingType       string  `json:"billing_type"`
			BillingPeriod     string  `json:"billing_period"`
			Status            string  `json:"status"`
			TaxMode           string  `json:"tax_mode"`
			TaxCategory       string  `json:"tax_category"`
			DefaultSuccessUrl *string `json:"default_success_url"`
			CreatedAt         string  `json:"created_at"`
			UpdatedAt         string  `json:"updated_at"`
			Mode              string  `json:"mode"`
		} `json:"product"`
		Units    int `json:"units"`
		Customer struct {
			Id        string `json:"id"`
			Object    string `json:"object"`
			Email     string `json:"email"`
			Name      string `json:"name"`
			Country   string `json:"country"`
			CreatedAt string `json:"created_at"`
			UpdatedAt string `json:"updated_at"`
			Mode      string `json:"mode"`
		} `json:"customer"`
		Status            string            `json:"status"`
		LastTransactionId string            `json:"last_transaction_id"`
		RefundAmount      int64             `json:"refund_amount"`
		Metadata          map[string]string `json:"metadata"`
		Mode              string            `json:"mode"`
	} `json:"object"`
}

func CreemWebhook(c *gin.Context) {
	if !isCreemWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	// 读取body内容用于打印，同时保留原始数据供后续使用
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, paymentWebhookMaxBodyBytes)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		if isPaymentWebhookBodyTooLarge(err) {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// 获取签名头
	signature := c.GetHeader(CreemSignatureHeader)
	if signature == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 缺少签名 path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	// 验证签名
	if !verifyCreemSignature(string(bodyBytes), signature, setting.GetCreemSettings().WebhookSecret) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 验签失败 path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusUnauthorized)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem webhook 验签成功 path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))

	// 解析新格式的webhook数据
	var webhookEvent CreemWebhookEvent
	if err := common.Unmarshal(bodyBytes, &webhookEvent); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem webhook 解析失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if webhookEvent.Id == "" || webhookEvent.CreatedAt <= 0 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf(
			"Creem webhook 缺少有效事件标识或时间 event_id=%q created_at=%d",
			webhookEvent.Id,
			webhookEvent.CreatedAt,
		))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem webhook 解析成功 event_type=%s event_id=%s request_id=%s order_id=%s order_status=%s", webhookEvent.EventType, webhookEvent.Id, webhookEvent.Object.RequestId, webhookEvent.Object.Order.Id, webhookEvent.Object.Order.Status))

	// 根据事件类型处理不同的webhook
	switch webhookEvent.EventType {
	case "checkout.completed":
		handleCheckoutCompleted(c, &webhookEvent)
	case "subscription.active", "subscription.trialing", "subscription.update", "subscription.scheduled_cancel", "subscription.past_due", "subscription.expired":
		if err := handleCreemSubscriptionStatus(&webhookEvent); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅状态同步失败 event_id=%s subscription_id=%s error=%q", webhookEvent.Id, webhookEvent.Object.Id, err.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	case "subscription.paid":
		if err := handleCreemSubscriptionPaid(&webhookEvent); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅续费处理失败 event_id=%s subscription_id=%s error=%q", webhookEvent.Id, webhookEvent.Object.Id, err.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	case "subscription.canceled", "subscription.paused":
		if err := handleCreemSubscriptionTerminal(&webhookEvent); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅终止处理失败 event_id=%s subscription_id=%s error=%q", webhookEvent.Id, webhookEvent.Object.Id, err.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	case "refund.created", "dispute.created":
		if err := handleCreemReversal(&webhookEvent); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 退款/争议处理失败 event_id=%s error=%q", webhookEvent.Id, err.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		c.Status(http.StatusOK)
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem webhook 忽略事件 event_type=%s event_id=%s", webhookEvent.EventType, webhookEvent.Id))
		c.Status(http.StatusOK)
	}
}

func handleCreemSubscriptionStatus(event *CreemWebhookEvent) error {
	if event == nil || event.Object.Id == "" {
		return errors.New("Creem subscription event missing id")
	}
	status := event.Object.Status
	if status == "" {
		status = strings.TrimPrefix(event.EventType, "subscription.")
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.Id, "event_type": event.EventType, "status": status,
	})
	return model.UpdateProviderSubscriptionStatusAt(
		model.PaymentProviderCreem,
		event.Object.Id,
		status,
		event.CreatedAt,
		payload,
	)
}

func creemSubscriptionPaidAmount(event *CreemWebhookEvent) (float64, error) {
	if event == nil {
		return 0, errors.New("Creem subscription.paid event is nil")
	}
	paidAmountMinor := event.Object.LastTransaction.AmountPaid
	if paidAmountMinor <= 0 {
		// The webhook guide's compact subscription.paid example omits the
		// expanded transaction. Product price is a compatibility fallback; when
		// present, last_transaction.amount_paid is authoritative because it
		// includes discounts and tax.
		paidAmountMinor = event.Object.Product.Price
	}
	if paidAmountMinor <= 0 {
		return 0, errors.New("Creem subscription.paid missing paid amount")
	}
	return float64(paidAmountMinor) / 100, nil
}

func validateCreemSubscriptionProduct(plan *model.SubscriptionPlan, event *CreemWebhookEvent) error {
	if plan == nil || event == nil {
		return errors.New("Creem subscription product context is missing")
	}
	if event.Object.Product.Id == "" || event.Object.Product.Id != plan.CreemProductId {
		return errors.New("Creem subscription product does not match local plan")
	}
	if strings.TrimSpace(event.Object.Product.Currency) == "" {
		return errors.New("Creem subscription product currency is missing")
	}
	if event.Object.LastTransaction.Id != "" &&
		event.Object.LastTransaction.Id != event.Object.LastTransactionId {
		return errors.New("Creem subscription transaction identifier changed")
	}
	if event.Object.LastTransaction.Currency != "" &&
		!strings.EqualFold(event.Object.LastTransaction.Currency, event.Object.Product.Currency) {
		return errors.New("Creem subscription transaction currency does not match product")
	}
	if event.Object.LastTransaction.Status != "" &&
		event.Object.LastTransaction.Status != "paid" {
		return errors.New("Creem subscription transaction is not paid")
	}
	return nil
}

func validateCreemSubscriptionCheckout(order *model.SubscriptionOrder, plan *model.SubscriptionPlan, event *CreemWebhookEvent) error {
	if order == nil || plan == nil || event == nil {
		return errors.New("Creem subscription checkout context is missing")
	}
	if order.PaymentProvider != model.PaymentProviderCreem {
		return model.ErrPaymentMethodMismatch
	}
	if err := validateCreemSubscriptionProduct(plan, event); err != nil {
		return err
	}
	expectedAmount, err := creemAmountCents(order.Money)
	if err != nil {
		return err
	}
	if event.Object.Order.Type != "recurring" ||
		event.Object.Order.Transaction == "" ||
		event.Object.Order.Amount != expectedAmount ||
		!strings.EqualFold(event.Object.Order.Currency, event.Object.Product.Currency) {
		return errors.New("Creem subscription checkout product, amount, or currency is invalid")
	}
	return nil
}

func handleCreemSubscriptionPaid(event *CreemWebhookEvent) error {
	if event == nil || event.Object.Id == "" || event.Object.LastTransactionId == "" {
		return errors.New("Creem subscription.paid missing identifiers")
	}
	sourceOrder, err := model.FindSubscriptionOrderByProviderSubscription(
		model.PaymentProviderCreem,
		event.Object.Id,
	)
	if err != nil {
		return err
	}
	plan, err := model.GetSubscriptionOrderPlan(sourceOrder)
	if err != nil {
		return err
	}
	if err := validateCreemSubscriptionProduct(plan, event); err != nil {
		return err
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.Id, "event_type": event.EventType, "transaction_id": event.Object.LastTransactionId,
	})
	paidAmount, err := creemSubscriptionPaidAmount(event)
	if err != nil {
		return err
	}
	return model.RenewProviderSubscriptionAt(
		model.PaymentProviderCreem,
		event.Object.Id,
		event.Object.LastTransactionId,
		event.Object.LastTransactionId,
		paidAmount,
		event.CreatedAt,
		payload,
	)
}

func handleCreemSubscriptionTerminal(event *CreemWebhookEvent) error {
	if event == nil || event.Object.Id == "" {
		return errors.New("Creem subscription event missing id")
	}
	status := event.Object.Status
	if status == "" {
		status = strings.TrimPrefix(event.EventType, "subscription.")
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.Id, "event_type": event.EventType, "status": status,
	})
	return model.CancelProviderSubscriptionAt(
		model.PaymentProviderCreem,
		event.Object.Id,
		status,
		event.CreatedAt,
		payload,
	)
}

func handleCreemReversal(event *CreemWebhookEvent) error {
	if event == nil {
		return errors.New("Creem reversal is nil")
	}
	payload := common.GetJsonString(map[string]interface{}{
		"event_id": event.Id, "event_type": event.EventType, "object_id": event.Object.Id,
	})
	subscriptionId := event.Object.Subscription.Id
	if subscriptionId == "" {
		subscriptionId = event.Object.Transaction.Subscription
	}
	paymentId := event.Object.Transaction.Id
	full := event.EventType == "dispute.created" ||
		(event.Object.Transaction.AmountPaid > 0 &&
			event.Object.Transaction.RefundedAmount >= event.Object.Transaction.AmountPaid)
	refundMinor := event.Object.Transaction.RefundedAmount
	amountMode := model.ProviderRefundAmountCumulative
	if refundMinor == 0 {
		refundMinor = event.Object.RefundAmount
		amountMode = model.ProviderRefundAmountIncremental
	}
	if paymentId != "" {
		err := model.ReverseTopUpByProviderPayment(
			model.PaymentProviderCreem,
			paymentId,
			float64(refundMinor)/100,
			full,
		)
		if err == nil {
			return nil
		}
		if !errors.Is(err, model.ErrTopUpNotFound) {
			return err
		}
		err = model.ApplySubscriptionRefundByProviderPayment(
			model.PaymentProviderCreem,
			paymentId,
			model.SubscriptionRefundInput{
				EventID:           event.Id,
				AmountMinor:       refundMinor,
				PaidAmountMinor:   event.Object.Transaction.AmountPaid,
				AmountMode:        amountMode,
				Full:              full,
				ProviderEventTime: event.CreatedAt,
				ProviderPayload:   payload,
			},
		)
		if err == nil || !errors.Is(err, model.ErrSubscriptionOrderNotFound) || subscriptionId == "" {
			return err
		}
	}
	if subscriptionId != "" {
		return model.ApplySubscriptionRefundByProviderSubscription(
			model.PaymentProviderCreem,
			subscriptionId,
			model.SubscriptionRefundInput{
				EventID:           event.Id,
				AmountMinor:       refundMinor,
				PaidAmountMinor:   event.Object.Transaction.AmountPaid,
				AmountMode:        amountMode,
				Full:              full,
				ProviderEventTime: event.CreatedAt,
				ProviderPayload:   payload,
			},
		)
	}
	tradeNo := event.Object.Checkout.RequestId
	if tradeNo == "" {
		return errors.New("Creem reversal missing original payment identifier")
	}
	return model.ReverseTopUpByTradeNo(model.PaymentProviderCreem, tradeNo, 0, true)
}

// 处理支付完成事件
func handleCheckoutCompleted(c *gin.Context, event *CreemWebhookEvent) {
	// 验证订单状态
	if event.Object.Order.Status != "paid" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 订单状态未支付，忽略处理 request_id=%s order_id=%s order_status=%s", event.Object.RequestId, event.Object.Order.Id, event.Object.Order.Status))
		c.Status(http.StatusOK)
		return
	}

	// 获取引用ID（这是我们创建订单时传递的request_id）
	referenceId := event.Object.RequestId
	if referenceId == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem webhook 缺少 request_id event_id=%s order_id=%s", event.Id, event.Object.Order.Id))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	LockOrder(referenceId)
	defer UnlockOrder(referenceId)
	if event.Object.Subscription.Id != "" {
		order, lookupErr := model.FindSubscriptionOrderByTradeNo(referenceId)
		if errors.Is(lookupErr, model.ErrSubscriptionOrderNotFound) {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅订单不存在 trade_no=%s", referenceId))
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		if lookupErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 查询订阅订单失败 trade_no=%s error=%q", referenceId, lookupErr.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		plan, err := model.GetSubscriptionOrderPlan(order)
		if err != nil || validateCreemSubscriptionCheckout(order, plan, event) != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf(
				"Creem 订阅产品、金额或币种不匹配 trade_no=%s product_id=%q amount=%d currency=%q",
				referenceId,
				event.Object.Product.Id,
				event.Object.Order.Amount,
				event.Object.Order.Currency,
			))
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		payload := common.GetJsonString(map[string]interface{}{
			"event_id": event.Id, "event_type": event.EventType, "order_id": event.Object.Order.Id,
		})
		if err := model.SetSubscriptionOrderProviderIdentifiersAt(
			referenceId,
			model.PaymentProviderCreem,
			event.Object.Customer.Id,
			event.Object.Subscription.Id,
			event.Object.Order.Transaction,
			event.Object.Subscription.Status,
			event.CreatedAt,
			payload,
		); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅标识绑定失败 trade_no=%s error=%q", referenceId, err.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		providerPayload := common.GetJsonString(map[string]interface{}{
			"event_id": event.Id, "event_type": event.EventType, "order_id": event.Object.Order.Id,
			"transaction_id": event.Object.Order.Transaction,
		})
		if err := model.CompleteSubscriptionOrder(
			referenceId,
			providerPayload,
			model.PaymentProviderCreem,
			"",
		); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 订阅订单处理失败 trade_no=%s creem_order_id=%s error=%q", referenceId, event.Object.Order.Id, err.Error()))
			c.AbortWithStatus(http.StatusInternalServerError)
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 订阅订单处理成功 trade_no=%s creem_order_id=%s", referenceId, event.Object.Order.Id))
		c.Status(http.StatusOK)
		return
	}

	// 验证订单类型，目前只处理一次性付款（充值）
	if event.Object.Order.Type != "onetime" {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 暂不支持该订单类型，忽略处理 request_id=%s creem_order_id=%s order_type=%s", referenceId, event.Object.Order.Id, event.Object.Order.Type))
		c.Status(http.StatusOK)
		return
	}

	paidAmountMinor := int64(event.Object.Order.Amount)
	if event.Object.Order.AmountPaid != nil {
		if *event.Object.Order.AmountPaid < 0 {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 充值实付金额无效 trade_no=%s", referenceId))
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		paidAmountMinor = *event.Object.Order.AmountPaid
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 支付完成回调 trade_no=%s creem_order_id=%s amount_paid=%d currency=%s", referenceId, event.Object.Order.Id, paidAmountMinor, event.Object.Order.Currency))

	// 查询本地订单确认存在
	topUp, lookupErr := model.FindTopUpByTradeNo(referenceId)
	if errors.Is(lookupErr, model.ErrTopUpNotFound) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem 充值订单不存在 trade_no=%s creem_order_id=%s", referenceId, event.Object.Order.Id))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if lookupErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 查询充值订单失败 trade_no=%s creem_order_id=%s error=%q", referenceId, event.Object.Order.Id, lookupErr.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	if topUp.ProviderProductId != "" && topUp.ProviderProductId != event.Object.Product.Id {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 充值产品不匹配 trade_no=%s", referenceId))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	if topUp.Currency != "" && !strings.EqualFold(topUp.Currency, event.Object.Order.Currency) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 充值币种不匹配 trade_no=%s", referenceId))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}
	expectedBaseAmount, err := creemAmountCents(topUp.Money)
	if err != nil || event.Object.Order.Amount != expectedBaseAmount {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 充值金额不匹配 trade_no=%s expected=%d actual=%d", referenceId, expectedBaseAmount, event.Object.Order.Amount))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	// 处理充值，传入客户邮箱和姓名信息
	customerEmail := event.Object.Customer.Email
	customerName := event.Object.Customer.Name

	// 防护性检查，确保邮箱和姓名不为空字符串
	if customerEmail == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem 回调客户邮箱为空 trade_no=%s creem_order_id=%s", referenceId, event.Object.Order.Id))
	}
	if customerName == "" {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Creem 回调客户姓名为空 trade_no=%s creem_order_id=%s", referenceId, event.Object.Order.Id))
	}

	err = model.RechargeCreem(referenceId, event.Object.Order.Transaction, paidAmountMinor, customerEmail, customerName, c.ClientIP())
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Creem 充值处理失败 trade_no=%s creem_order_id=%s client_ip=%s error=%q", referenceId, event.Object.Order.Id, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Creem 充值成功 trade_no=%s creem_order_id=%s quota=%d money=%.2f client_ip=%s", referenceId, event.Object.Order.Id, topUp.Amount, topUp.Money, c.ClientIP()))
	c.Status(http.StatusOK)
}

type CreemCheckoutRequest struct {
	ProductId string `json:"product_id"`
	RequestId string `json:"request_id"`
	Customer  struct {
		Email string `json:"email"`
	} `json:"customer"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type CreemCheckoutResponse struct {
	CheckoutUrl string `json:"checkout_url"`
	Id          string `json:"id"`
}

func parseCreemCheckoutResponse(resp *http.Response) (*CreemCheckoutResponse, error) {
	if resp == nil || resp.Body == nil {
		return nil, fmt.Errorf("Creem API returned an empty response")
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Creem API http status %d", resp.StatusCode)
	}
	body, err := service.ReadResponseBodyWithLimit(resp.Body, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("读取 Creem 响应失败: %w", err)
	}
	var checkoutResp CreemCheckoutResponse
	if err = common.Unmarshal(body, &checkoutResp); err != nil {
		return nil, fmt.Errorf("解析响应失败: %v", err)
	}
	if checkoutResp.CheckoutUrl == "" {
		return nil, fmt.Errorf("Creem API resp no checkout url")
	}
	return &checkoutResp, nil
}

func genCreemLink(ctx context.Context, referenceId string, product *CreemProduct, email string, username string) (string, error) {
	creemSetting := setting.GetCreemSettings()
	if creemSetting.APIKey == "" {
		return "", fmt.Errorf("未配置Creem API密钥")
	}

	// 根据测试模式选择 API 端点
	apiUrl := "https://api.creem.io/v1/checkouts"
	if creemSetting.TestMode {
		apiUrl = "https://test-api.creem.io/v1/checkouts"
		logger.LogInfo(ctx, fmt.Sprintf("Creem 使用测试环境 api_url=%s", common.MaskSensitiveInfo(apiUrl)))
	}

	// 构建请求数据，确保包含用户邮箱
	requestData := CreemCheckoutRequest{
		ProductId: product.ProductId,
		RequestId: referenceId, // 这个作为订单ID传递给Creem
		Customer: struct {
			Email string `json:"email"`
		}{
			Email: email, // 用户邮箱会在支付页面预填充
		},
		Metadata: map[string]string{
			"username":     username,
			"reference_id": referenceId,
			"product_name": product.Name,
			"quota":        fmt.Sprintf("%d", product.Quota),
		},
	}

	// 序列化请求数据
	jsonData, err := common.Marshal(requestData)
	if err != nil {
		return "", fmt.Errorf("序列化请求数据失败: %v", err)
	}

	// 创建 HTTP 请求
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, apiUrl, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", fmt.Errorf("创建HTTP请求失败: %v", service.SanitizeNetworkError(err))
	}

	// 设置请求头
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", creemSetting.APIKey)

	logger.LogInfo(ctx, fmt.Sprintf("Creem 支付请求已发送 api_url=%s product_id=%s trade_no=%s", common.MaskSensitiveInfo(apiUrl), product.ProductId, referenceId))

	// 发送请求
	client := service.GetHttpClient()
	if client == nil {
		client = service.GetHttpClientWithTimeout(30 * time.Second)
	}
	resp, err := service.DoUpstreamRequest(client, req)
	if err != nil {
		return "", fmt.Errorf("发送HTTP请求失败: %v", err)
	}
	defer resp.Body.Close()

	logger.LogInfo(ctx, fmt.Sprintf("Creem API 响应已收到 trade_no=%s status_code=%d", referenceId, resp.StatusCode))

	checkoutResp, err := parseCreemCheckoutResponse(resp)
	if err != nil {
		return "", err
	}

	logger.LogInfo(ctx, fmt.Sprintf("Creem 支付链接创建成功 trade_no=%s response_id=%s", referenceId, checkoutResp.Id))
	return checkoutResp.CheckoutUrl, nil
}
