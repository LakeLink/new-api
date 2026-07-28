package controller

import (
	"errors"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
)

const paymentWebhookMaxBodyBytes int64 = 1 << 20

func isPaymentWebhookBodyTooLarge(err error) bool {
	var maxBytesErr *http.MaxBytesError
	return errors.As(err, &maxBytesErr)
}

func isStripeTopUpEnabled() bool {
	stripeSetting := setting.GetStripeSettings()
	return strings.TrimSpace(stripeSetting.APISecret) != "" &&
		strings.TrimSpace(stripeSetting.WebhookSecret) != "" &&
		stripeSetting.UnitPrice > 0 && !math.IsNaN(stripeSetting.UnitPrice) && !math.IsInf(stripeSetting.UnitPrice, 0)
}

func isStripeWebhookConfigured() bool {
	return strings.TrimSpace(setting.GetStripeSettings().WebhookSecret) != ""
}

func isStripeWebhookEnabled() bool {
	return isStripeWebhookConfigured()
}

func isCreemTopUpEnabled() bool {
	creemSetting := setting.GetCreemSettings()
	products := strings.TrimSpace(creemSetting.Products)
	return strings.TrimSpace(creemSetting.APIKey) != "" &&
		isCreemWebhookConfigured() &&
		products != "" &&
		products != "[]"
}

func isCreemWebhookConfigured() bool {
	return strings.TrimSpace(setting.GetCreemSettings().WebhookSecret) != ""
}

func isCreemWebhookEnabled() bool {
	return isCreemWebhookConfigured()
}

func isWaffoTopUpEnabled() bool {
	if !setting.GetWaffoSettings().Enabled {
		return false
	}

	return isWaffoWebhookConfigured()
}

func isWaffoWebhookConfigured() bool {
	waffoSetting := setting.GetWaffoSettings()
	if strings.TrimSpace(waffoSetting.MerchantID) == "" {
		return false
	}
	if waffoSetting.Sandbox {
		return strings.TrimSpace(waffoSetting.SandboxAPIKey) != "" &&
			strings.TrimSpace(waffoSetting.SandboxPrivateKey) != "" &&
			strings.TrimSpace(waffoSetting.SandboxPublicCert) != ""
	}

	return strings.TrimSpace(waffoSetting.APIKey) != "" &&
		strings.TrimSpace(waffoSetting.PrivateKey) != "" &&
		strings.TrimSpace(waffoSetting.PublicCert) != ""
}

func isWaffoWebhookEnabled() bool {
	return isWaffoWebhookConfigured()
}

func isWaffoPancakeTopUpEnabled() bool {
	// Presence-of-credentials = enabled. Webhook public keys ship inside
	// the SDK; mode (test/prod) is read from each event.
	waffoSetting := setting.GetWaffoSettings()
	return strings.TrimSpace(waffoSetting.PancakeMerchantID) != "" &&
		strings.TrimSpace(waffoSetting.PancakePrivateKey) != "" &&
		strings.TrimSpace(waffoSetting.PancakeStoreID) != "" &&
		strings.TrimSpace(waffoSetting.PancakeProductID) != ""
}

func isWaffoPancakeWebhookConfigured() bool {
	// Pancake webhook verification uses Waffo's bundled environment public
	// keys and does not depend on credentials used to create new checkouts.
	return true
}

func isWaffoPancakeWebhookEnabled() bool {
	return isWaffoPancakeWebhookConfigured()
}

func isEpayTopUpEnabled() bool {
	return isEpayWebhookConfigured() && len(operation_setting.GetLegacyPaymentSetting().PayMethods) > 0
}

func isEpayWebhookConfigured() bool {
	paymentSetting := operation_setting.GetLegacyPaymentSetting()
	return strings.TrimSpace(paymentSetting.PayAddress) != "" &&
		strings.TrimSpace(paymentSetting.EpayID) != "" &&
		strings.TrimSpace(paymentSetting.EpayKey) != ""
}

func isEpayWebhookEnabled() bool {
	return isEpayWebhookConfigured()
}
