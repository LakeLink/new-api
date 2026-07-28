package controller

import (
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
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/thanhpk/randstr"
	waffo "github.com/waffo-com/waffo-go"
	"github.com/waffo-com/waffo-go/config"
	"github.com/waffo-com/waffo-go/core"
	"github.com/waffo-com/waffo-go/types/order"
)

func getWaffoSDK() (*waffo.Waffo, error) {
	waffoSetting := setting.GetWaffoSettings()
	env := config.Sandbox
	apiKey := waffoSetting.SandboxAPIKey
	privateKey := waffoSetting.SandboxPrivateKey
	publicKey := waffoSetting.SandboxPublicCert
	if !waffoSetting.Sandbox {
		env = config.Production
		apiKey = waffoSetting.APIKey
		privateKey = waffoSetting.PrivateKey
		publicKey = waffoSetting.PublicCert
	}
	builder := config.NewConfigBuilder().
		APIKey(apiKey).
		PrivateKey(privateKey).
		WaffoPublicKey(publicKey).
		Environment(env)
	if waffoSetting.MerchantID != "" {
		builder = builder.MerchantID(waffoSetting.MerchantID)
	}
	cfg, err := builder.Build()
	if err != nil {
		return nil, err
	}
	return waffo.New(cfg), nil
}

func getWaffoUserEmail(user *model.User) string {
	return fmt.Sprintf("%d@examples.com", user.Id)
}

func getWaffoCurrency() string {
	if currency := setting.GetWaffoSettings().Currency; currency != "" {
		return currency
	}
	return "USD"
}

func buildWaffoTopUpGoodsInfo(amount int64) *order.GoodsInfo {
	appName := strings.TrimSpace(common.GetLegacyOptionString("SystemName", &common.SystemName))
	if appName == "" {
		appName = "New API"
	}
	return &order.GoodsInfo{
		GoodsName: fmt.Sprintf("Recharge %d credits", amount),
		AppName:   appName,
	}
}

// zeroDecimalCurrencies 零小数位币种，金额不能带小数点
var zeroDecimalCurrencies = map[string]bool{
	"IDR": true, "JPY": true, "KRW": true, "VND": true,
}

func formatWaffoAmount(amount float64, currency string) string {
	if zeroDecimalCurrencies[currency] {
		return fmt.Sprintf("%.0f", amount)
	}
	return fmt.Sprintf("%.2f", amount)
}

// getWaffoPayMoney converts the user-facing amount to USD for Waffo payment.
// Waffo only accepts USD, so this function handles the conversion from different
// display types (USD/CNY/TOKENS) to the actual USD amount to charge.
func getWaffoPayMoney(amount int64, group string) float64 {
	waffoSetting := setting.GetWaffoSettings()
	if amount <= 0 || waffoSetting.UnitPrice <= 0 ||
		math.IsNaN(waffoSetting.UnitPrice) || math.IsInf(waffoSetting.UnitPrice, 0) {
		return 0
	}
	payUnits := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		quotaPerUnit := common.GetLegacyOptionFloat64("QuotaPerUnit", &common.QuotaPerUnit)
		if quotaPerUnit <= 0 || math.IsNaN(quotaPerUnit) || math.IsInf(quotaPerUnit, 0) {
			return 0
		}
		payUnits = payUnits.Div(decimal.NewFromFloat(quotaPerUnit))
	}
	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}
	if topupGroupRatio <= 0 || math.IsNaN(topupGroupRatio) || math.IsInf(topupGroupRatio, 0) {
		return 0
	}
	discount := 1.0
	if ds, ok := lookupPaymentAmountDiscount(operation_setting.GetPaymentSetting().AmountDiscount, amount); ok {
		if ds > 0 {
			if math.IsNaN(ds) || math.IsInf(ds, 0) {
				return 0
			}
			discount = ds
		}
	}
	payMoney := payUnits.
		Mul(decimal.NewFromFloat(waffoSetting.UnitPrice)).
		Mul(decimal.NewFromFloat(topupGroupRatio)).
		Mul(decimal.NewFromFloat(discount))
	if !payMoney.IsPositive() || payMoney.GreaterThan(decimal.NewFromInt(99_999_999)) {
		return 0
	}
	return payMoney.InexactFloat64()
}

func getWaffoMinTopup() int64 {
	minimum := int64(setting.GetWaffoSettings().MinTopUp)
	if operation_setting.GetQuotaDisplayType() != operation_setting.QuotaDisplayTypeTokens {
		return minimum
	}
	return tokenDisplayMinimum(minimum)
}

type WaffoPayRequest struct {
	Amount         int64  `json:"amount"`
	PayMethodIndex *int   `json:"pay_method_index"` // 服务端支付方式列表的索引，nil 表示由 Waffo 自动选择
	PayMethodType  string `json:"pay_method_type"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
	PayMethodName  string `json:"pay_method_name"`  // Deprecated: 兼容旧前端，优先使用 pay_method_index
}

func RequestWaffoAmount(c *gin.Context) {
	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	waffoMinTopup := getWaffoMinTopup()
	if req.Amount < waffoMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", waffoMinTopup)})
		return
	}
	if _, err := model.TopUpQuotaForAmount(req.Amount, operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "error", "data": "充值额度超出允许范围"})
		return
	}

	id := c.GetInt("id")
	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}

	payMoney := getWaffoPayMoney(req.Amount, group)
	if payMoney <= 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "success", "data": strconv.FormatFloat(payMoney, 'f', 2, 64)})
}

// RequestWaffoPay 创建 Waffo 支付订单
func RequestWaffoPay(c *gin.Context) {
	if !isWaffoTopUpEnabled() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "Waffo 支付或 Webhook 未完整配置"})
		return
	}

	var req WaffoPayRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}
	waffoMinTopup := getWaffoMinTopup()
	if req.Amount < waffoMinTopup {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", waffoMinTopup)})
		return
	}

	id := c.GetInt("id")
	user, err := model.GetUserById(id, false)
	if err != nil || user == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "用户不存在"})
		return
	}

	// 从服务端配置查找支付方式，客户端只传索引或旧字段
	var resolvedPayMethodType, resolvedPayMethodName string
	methods := setting.GetWaffoPayMethods()
	if req.PayMethodIndex != nil {
		// 新协议：按索引查找
		idx := *req.PayMethodIndex
		if idx < 0 || idx >= len(methods) {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付方式索引无效 user_id=%d pay_method_index=%d method_count=%d", id, idx, len(methods)))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付方式"})
			return
		}
		resolvedPayMethodType = methods[idx].PayMethodType
		resolvedPayMethodName = methods[idx].PayMethodName
	} else if req.PayMethodType != "" {
		// 兼容旧前端：验证客户端传的值在服务端列表中
		valid := false
		for _, m := range methods {
			if m.PayMethodType == req.PayMethodType && m.PayMethodName == req.PayMethodName {
				valid = true
				resolvedPayMethodType = m.PayMethodType
				resolvedPayMethodName = m.PayMethodName
				break
			}
		}
		if !valid {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 支付方式无效 user_id=%d pay_method_type=%s pay_method_name=%q", id, req.PayMethodType, req.PayMethodName))
			c.JSON(http.StatusOK, gin.H{"message": "error", "data": "不支持的支付方式"})
			return
		}
	}
	// resolvedPayMethodType/Name 为空时，Waffo 自动选择支付方式

	group, err := model.GetUserGroup(id, true)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	creditQuota, err := model.TopUpQuotaForAmount(req.Amount, operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"message": "error", "data": "充值额度超出允许范围"})
		return
	}
	payMoney := getWaffoPayMoney(req.Amount, group)
	if payMoney < 0.01 {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	currency := getWaffoCurrency()
	chargeAmount := formatWaffoAmount(payMoney, currency)
	chargedMoney, err := decimal.NewFromString(chargeAmount)
	if err != nil || !chargedMoney.IsPositive() {
		c.JSON(http.StatusBadRequest, gin.H{"message": "error", "data": "支付金额无效"})
		return
	}

	// 生成唯一订单号，paymentRequestId 与 merchantOrderId 保持一致，简化追踪
	merchantOrderId := fmt.Sprintf("WAFFO-%d-%d-%s", id, time.Now().UnixMilli(), randstr.String(6))
	paymentRequestId := merchantOrderId

	// Token 模式下归一化 Amount（存等价美元/CNY 数量，避免 RechargeWaffo 双重放大）
	amount := req.Amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		amount = int64(float64(req.Amount) / common.CurrentQuotaPerUnit())
		if amount < 1 {
			amount = 1
		}
	}

	// 创建本地订单
	topUp := &model.TopUp{
		UserId:          id,
		Amount:          amount,
		Quota:           creditQuota,
		Money:           chargedMoney.InexactFloat64(),
		TradeNo:         merchantOrderId,
		PaymentMethod:   model.PaymentMethodWaffo,
		PaymentProvider: model.PaymentProviderWaffo,
		Currency:        currency,
		CreateTime:      time.Now().Unix(),
		Status:          common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建充值订单失败 user_id=%d trade_no=%s amount=%d error=%q", id, merchantOrderId, req.Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	sdk, err := getWaffoSDK()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo SDK 初始化失败 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付配置错误"})
		return
	}

	callbackAddr := service.GetCallbackAddress()
	notifyUrl := callbackAddr + "/api/waffo/webhook"
	waffoSetting := setting.GetWaffoSettings()
	if waffoSetting.NotifyURL != "" {
		notifyUrl = waffoSetting.NotifyURL
	}
	returnUrl := paymentReturnPath("/console/topup?show_history=true")
	if waffoSetting.ReturnURL != "" {
		returnUrl = waffoSetting.ReturnURL
	}

	goodsInfo := buildWaffoTopUpGoodsInfo(req.Amount)
	createParams := &order.CreateOrderParams{
		PaymentRequestID: paymentRequestId,
		MerchantOrderID:  merchantOrderId,
		OrderAmount:      chargeAmount,
		OrderCurrency:    currency,
		OrderDescription: goodsInfo.GoodsName,
		OrderRequestedAt: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		NotifyURL:        notifyUrl,
		MerchantInfo: &order.MerchantInfo{
			MerchantID: waffoSetting.MerchantID,
		},
		UserInfo: &order.UserInfo{
			UserID:       strconv.Itoa(user.Id),
			UserEmail:    getWaffoUserEmail(user),
			UserTerminal: "WEB",
		},
		PaymentInfo: &order.PaymentInfo{
			ProductName:   "ONE_TIME_PAYMENT",
			PayMethodType: resolvedPayMethodType,
			PayMethodName: resolvedPayMethodName,
		},
		GoodsInfo:          goodsInfo,
		SuccessRedirectURL: returnUrl,
		FailedRedirectURL:  returnUrl,
	}
	resp, err := sdk.Order().Create(c.Request.Context(), createParams, nil)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 创建订单失败 user_id=%d trade_no=%s error=%q", id, merchantOrderId, err.Error()))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	if !resp.IsSuccess() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 创建订单业务失败 user_id=%d trade_no=%s code=%s message=%q", id, merchantOrderId, resp.Code, resp.Message))
		topUp.Status = common.TopUpStatusFailed
		_ = topUp.Update()
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}

	orderData := resp.GetData()
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 充值订单创建成功 user_id=%d trade_no=%s amount=%d money=%.2f pay_method_type=%s pay_method_name=%q", id, merchantOrderId, req.Amount, topUp.Money, resolvedPayMethodType, resolvedPayMethodName))

	paymentUrl := orderData.FetchRedirectURL()
	if paymentUrl == "" {
		paymentUrl = orderData.OrderAction
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "success",
		"data": gin.H{
			"payment_url": paymentUrl,
			"order_id":    merchantOrderId,
		},
	})
}

// webhookPayloadWithSubInfo 扩展 PAYMENT_NOTIFICATION，包含 SDK 未定义的 subscriptionInfo 字段
type webhookPayloadWithSubInfo struct {
	EventType string `json:"eventType"`
	Result    struct {
		core.PaymentNotificationResult
		SubscriptionInfo *webhookSubscriptionInfo `json:"subscriptionInfo,omitempty"`
	} `json:"result"`
}

type webhookSubscriptionInfo struct {
	Period              string `json:"period,omitempty"`
	MerchantRequest     string `json:"merchantRequest,omitempty"`
	SubscriptionID      string `json:"subscriptionId,omitempty"`
	SubscriptionRequest string `json:"subscriptionRequest,omitempty"`
}

// WaffoWebhook 处理 Waffo 回调通知（支付/退款/订阅）
func WaffoWebhook(c *gin.Context) {
	if !isWaffoWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 被拒绝 reason=webhook_disabled path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusForbidden)
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, paymentWebhookMaxBodyBytes)
	bodyBytes, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 读取请求体失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		if isPaymentWebhookBodyTooLarge(err) {
			c.AbortWithStatus(http.StatusRequestEntityTooLarge)
			return
		}
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	sdk, err := getWaffoSDK()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook SDK 初始化失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	wh := sdk.Webhook()
	bodyStr := string(bodyBytes)
	signature := c.GetHeader("X-SIGNATURE")

	// 验证请求签名
	if !wh.VerifySignature(bodyStr, signature) {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo webhook 验签失败 path=%q client_ip=%s", c.Request.URL.Path, c.ClientIP()))
		c.AbortWithStatus(http.StatusBadRequest)
		return
	}

	var event core.WebhookEvent
	if err := common.Unmarshal(bodyBytes, &event); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo webhook 解析失败 path=%q client_ip=%s error=%q", c.Request.URL.Path, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, "invalid payload")
		return
	}

	switch event.EventType {
	case core.EventPayment:
		// 解析为扩展类型，区分普通支付和订阅支付
		var payload webhookPayloadWithSubInfo
		if err := common.Unmarshal(bodyBytes, &payload); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调载荷解析失败 event_type=%s client_ip=%s error=%q", event.EventType, c.ClientIP(), err.Error()))
			sendWaffoWebhookResponse(c, wh, false, "invalid payment payload")
			return
		}
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo webhook 验签并解析成功 event_type=%s merchant_order_id=%s order_status=%s client_ip=%s", event.EventType, payload.Result.MerchantOrderID, payload.Result.OrderStatus, c.ClientIP()))
		handleWaffoPayment(c, wh, &payload.Result.PaymentNotificationResult)
	case core.EventRefund:
		var notification core.RefundNotification
		if err := common.Unmarshal(bodyBytes, &notification); err != nil || notification.Result == nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 退款回调载荷解析失败 client_ip=%s", c.ClientIP()))
			sendWaffoWebhookResponse(c, wh, false, "invalid refund payload")
			return
		}
		handleWaffoRefund(c, wh, notification.Result)
	default:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo webhook 忽略事件 event_type=%s client_ip=%s", event.EventType, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, true, "")
	}
}

// handleWaffoPayment 处理支付完成通知
func handleWaffoPayment(c *gin.Context, wh *core.WebhookHandler, result *core.PaymentNotificationResult) {
	if !waffoWebhookMerchantMatches(result.MerchantInfo, setting.GetWaffoSettings().MerchantID) {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo 支付回调商户不匹配 trade_no=%s acquiring_order_id=%s",
			result.MerchantOrderID, result.AcquiringOrderID,
		))
		sendWaffoWebhookResponse(c, wh, false, "merchant mismatch")
		return
	}
	switch result.OrderStatus {
	case core.OrderStatusPaySuccess:
		// Continue below and fulfill the order.
	case core.OrderStatusOrderClose:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 订单状态非成功，忽略充值 trade_no=%s order_status=%s client_ip=%s", result.MerchantOrderID, result.OrderStatus, c.ClientIP()))
		if result.MerchantOrderID != "" {
			if err := model.UpdatePendingTopUpStatus(result.MerchantOrderID, model.PaymentProviderWaffo, common.TopUpStatusFailed); err != nil && err != model.ErrTopUpNotFound && err != model.ErrTopUpStatusInvalid {
				logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 标记失败订单状态失败 trade_no=%s error=%q", result.MerchantOrderID, err.Error()))
			}
		}
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	case core.OrderStatusPayInProgress, core.OrderStatusAuthorizationRequired, core.OrderStatusAuthedWaitingCapture:
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 订单仍在处理 trade_no=%s order_status=%s client_ip=%s", result.MerchantOrderID, result.OrderStatus, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	default:
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 未知订单状态 trade_no=%s order_status=%s client_ip=%s", result.MerchantOrderID, result.OrderStatus, c.ClientIP()))
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	}

	merchantOrderId := result.MerchantOrderID

	LockOrder(merchantOrderId)
	defer UnlockOrder(merchantOrderId)
	topUp, lookupErr := model.FindTopUpByTradeNo(merchantOrderId)
	if lookupErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 查询充值订单失败 trade_no=%s error=%q", merchantOrderId, lookupErr.Error()))
		sendWaffoWebhookResponse(c, wh, false, "order lookup failed")
		return
	}
	if topUp.PaymentProvider != model.PaymentProviderWaffo {
		sendWaffoWebhookResponse(c, wh, false, "order provider mismatch")
		return
	}
	actualAmount, err := decimal.NewFromString(result.OrderAmount)
	if err != nil || !actualAmount.Equal(decimal.NewFromFloat(topUp.Money).Round(2)) || !strings.EqualFold(result.OrderCurrency, topUp.Currency) {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付金额或币种不匹配 trade_no=%s", merchantOrderId))
		sendWaffoWebhookResponse(c, wh, false, "payment amount mismatch")
		return
	}

	if strings.TrimSpace(result.AcquiringOrderID) == "" {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 支付回调缺少平台订单号 trade_no=%s", merchantOrderId))
		sendWaffoWebhookResponse(c, wh, false, "missing acquiring order id")
		return
	}
	if err := model.RechargeWaffo(merchantOrderId, result.AcquiringOrderID, c.ClientIP()); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 充值处理失败 trade_no=%s client_ip=%s error=%q", merchantOrderId, c.ClientIP(), err.Error()))
		sendWaffoWebhookResponse(c, wh, false, err.Error())
		return
	}

	logger.LogInfo(c.Request.Context(), fmt.Sprintf("Waffo 充值成功 trade_no=%s client_ip=%s", merchantOrderId, c.ClientIP()))
	sendWaffoWebhookResponse(c, wh, true, "")
}

func handleWaffoRefund(c *gin.Context, wh *core.WebhookHandler, result *core.RefundNotificationResult) {
	tradeNo := result.OrigPaymentRequestID
	if tradeNo == "" {
		sendWaffoWebhookResponse(c, wh, false, "missing original payment id")
		return
	}
	topUp, lookupErr := model.FindTopUpByTradeNo(tradeNo)
	if lookupErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo 退款回调查询订单失败 trade_no=%s acquiring_order_id=%s error=%q",
			tradeNo, result.AcquiringOrderID, lookupErr.Error(),
		))
		sendWaffoWebhookResponse(c, wh, false, "refund order lookup failed")
		return
	}
	if topUp.PaymentProvider != model.PaymentProviderWaffo ||
		strings.TrimSpace(topUp.ProviderPaymentId) == "" ||
		strings.TrimSpace(result.AcquiringOrderID) != strings.TrimSpace(topUp.ProviderPaymentId) {
		logger.LogError(c.Request.Context(), fmt.Sprintf(
			"Waffo 退款回调平台订单号不匹配 trade_no=%s acquiring_order_id=%s",
			tradeNo, result.AcquiringOrderID,
		))
		sendWaffoWebhookResponse(c, wh, false, "refund order mismatch")
		return
	}
	switch result.RefundStatus {
	case core.RefundStatusInProgress, core.RefundStatusFailed:
		sendWaffoWebhookResponse(c, wh, true, "")
		return
	case core.RefundStatusFullyRefunded:
		if err := model.ReverseTopUpByTradeNo(model.PaymentProviderWaffo, tradeNo, 0, true); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 全额退款撤销额度失败 trade_no=%s error=%q", tradeNo, err.Error()))
			sendWaffoWebhookResponse(c, wh, false, err.Error())
			return
		}
	case core.RefundStatusPartiallyRefunded:
		remaining, err := decimal.NewFromString(result.RemainingRefundAmount)
		if topUp == nil || err != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 部分退款缺少可验证的累计金额 trade_no=%s", tradeNo))
			sendWaffoWebhookResponse(c, wh, true, "")
			return
		}
		cumulative := decimal.NewFromFloat(topUp.Money).Sub(remaining).InexactFloat64()
		if err := model.ReverseTopUpByTradeNo(model.PaymentProviderWaffo, tradeNo, cumulative, false); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("Waffo 部分退款撤销额度失败 trade_no=%s error=%q", tradeNo, err.Error()))
			sendWaffoWebhookResponse(c, wh, false, err.Error())
			return
		}
	default:
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Waffo 未知退款状态 trade_no=%s refund_status=%s", tradeNo, result.RefundStatus))
	}
	sendWaffoWebhookResponse(c, wh, true, "")
}

func waffoWebhookMerchantMatches(merchantInfo map[string]interface{}, configuredMerchantID string) bool {
	configured := strings.TrimSpace(configuredMerchantID)
	if configured == "" {
		return false
	}
	merchantID, ok := merchantInfo["merchantId"].(string)
	return ok && strings.TrimSpace(merchantID) == configured
}

// sendWaffoWebhookResponse 发送签名响应
func sendWaffoWebhookResponse(c *gin.Context, wh *core.WebhookHandler, success bool, msg string) {
	var body, sig string
	if success {
		body, sig = wh.BuildSuccessResponse()
	} else {
		body, sig = wh.BuildFailedResponse(msg)
	}
	c.Header("X-SIGNATURE", sig)
	status := http.StatusOK
	if !success {
		status = http.StatusInternalServerError
	}
	c.Data(status, "application/json", []byte(body))
}
