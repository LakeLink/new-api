package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/setting"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestStripeWebhookRemainsEnabledWhenNewSalesAreDisabled(t *testing.T) {
	originalAPISecret := setting.StripeApiSecret
	originalWebhookSecret := setting.StripeWebhookSecret
	originalPriceID := setting.StripePriceId
	t.Cleanup(func() {
		setting.StripeApiSecret = originalAPISecret
		setting.StripeWebhookSecret = originalWebhookSecret
		setting.StripePriceId = originalPriceID
	})

	setting.StripeWebhookSecret = ""
	setting.StripeApiSecret = "sk_test_123"
	setting.StripePriceId = "price_123"
	require.False(t, isStripeWebhookEnabled())

	setting.StripeWebhookSecret = "whsec_test"
	require.True(t, isStripeWebhookEnabled())

	setting.StripeApiSecret = ""
	setting.StripePriceId = ""
	require.True(t, isStripeWebhookEnabled())
}

func TestCreemWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	originalAPIKey := setting.CreemApiKey
	originalProducts := setting.CreemProducts
	originalWebhookSecret := setting.CreemWebhookSecret
	originalTestMode := setting.CreemTestMode
	t.Cleanup(func() {
		setting.CreemApiKey = originalAPIKey
		setting.CreemProducts = originalProducts
		setting.CreemWebhookSecret = originalWebhookSecret
		setting.CreemTestMode = originalTestMode
	})

	setting.CreemWebhookSecret = ""
	setting.CreemApiKey = "creem_api_key"
	setting.CreemProducts = `[{"productId":"prod_123"}]`
	require.False(t, isCreemWebhookEnabled())
	require.False(t, isCreemTopUpEnabled())

	setting.CreemTestMode = true
	require.False(t, isCreemWebhookEnabled(), "test mode must not make unsigned payment callbacks valid")
	require.False(t, isCreemTopUpEnabled())

	setting.CreemWebhookSecret = "creem_secret"
	require.True(t, isCreemWebhookEnabled())
	require.True(t, isCreemTopUpEnabled())

	setting.CreemProducts = "[]"
	setting.CreemApiKey = ""
	require.True(t, isCreemWebhookEnabled())
}

func TestWaffoWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	originalEnabled := setting.WaffoEnabled
	originalSandbox := setting.WaffoSandbox
	originalAPIKey := setting.WaffoApiKey
	originalPrivateKey := setting.WaffoPrivateKey
	originalPublicCert := setting.WaffoPublicCert
	originalSandboxAPIKey := setting.WaffoSandboxApiKey
	originalSandboxPrivateKey := setting.WaffoSandboxPrivateKey
	originalSandboxPublicCert := setting.WaffoSandboxPublicCert
	originalMerchantID := setting.WaffoMerchantId
	t.Cleanup(func() {
		setting.WaffoEnabled = originalEnabled
		setting.WaffoSandbox = originalSandbox
		setting.WaffoApiKey = originalAPIKey
		setting.WaffoPrivateKey = originalPrivateKey
		setting.WaffoPublicCert = originalPublicCert
		setting.WaffoSandboxApiKey = originalSandboxAPIKey
		setting.WaffoSandboxPrivateKey = originalSandboxPrivateKey
		setting.WaffoSandboxPublicCert = originalSandboxPublicCert
		setting.WaffoMerchantId = originalMerchantID
	})

	setting.WaffoEnabled = true
	setting.WaffoSandbox = false
	setting.WaffoMerchantId = "merchant"
	setting.WaffoApiKey = ""
	setting.WaffoPrivateKey = "private"
	setting.WaffoPublicCert = "public"
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoApiKey = "api"
	require.True(t, isWaffoWebhookEnabled())

	setting.WaffoEnabled = false
	require.True(t, isWaffoWebhookEnabled())

	setting.WaffoEnabled = true
	setting.WaffoSandbox = true
	setting.WaffoSandboxApiKey = ""
	setting.WaffoSandboxPrivateKey = "sandbox_private"
	setting.WaffoSandboxPublicCert = "sandbox_public"
	require.False(t, isWaffoWebhookEnabled())

	setting.WaffoSandboxApiKey = "sandbox_api"
	require.True(t, isWaffoWebhookEnabled())

	setting.WaffoMerchantId = ""
	require.False(t, isWaffoWebhookEnabled())
	require.False(t, isWaffoTopUpEnabled())
}

func TestWaffoPancakeWebhookUsesBundledVerificationKeys(t *testing.T) {
	originalMerchantID := setting.WaffoPancakeMerchantID
	originalPrivateKey := setting.WaffoPancakePrivateKey
	originalStoreID := setting.WaffoPancakeStoreID
	originalProductID := setting.WaffoPancakeProductID
	t.Cleanup(func() {
		setting.WaffoPancakeMerchantID = originalMerchantID
		setting.WaffoPancakePrivateKey = originalPrivateKey
		setting.WaffoPancakeStoreID = originalStoreID
		setting.WaffoPancakeProductID = originalProductID
	})

	// Presence of all checkout credentials enables new sales. Webhook public
	// keys are bundled in the SDK and there is no separate Enabled toggle —
	// clear any required checkout field to disable new sales.
	setting.WaffoPancakeMerchantID = ""
	setting.WaffoPancakePrivateKey = "private"
	setting.WaffoPancakeStoreID = "store"
	setting.WaffoPancakeProductID = "product"
	require.True(t, isWaffoPancakeWebhookEnabled())
	require.False(t, isWaffoPancakeTopUpEnabled())

	setting.WaffoPancakeMerchantID = "merchant"
	require.True(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeProductID = ""
	require.True(t, isWaffoPancakeWebhookEnabled())

	setting.WaffoPancakeProductID = "product"
	setting.WaffoPancakePrivateKey = ""
	require.True(t, isWaffoPancakeWebhookEnabled())
	require.False(t, isWaffoPancakeTopUpEnabled())

	setting.WaffoPancakePrivateKey = "private"
	setting.WaffoPancakeStoreID = ""
	require.False(t, isWaffoPancakeTopUpEnabled())
}

func TestWaffoPancakeWebhookStoreOwnershipRequiresExactConfiguredStore(t *testing.T) {
	require.True(t, waffoPancakeWebhookStoreMatches("store_123", "store_123"))
	require.True(t, waffoPancakeWebhookStoreMatches(" store_123 ", " store_123 "))
	require.False(t, waffoPancakeWebhookStoreMatches("other_store", "store_123"))
	require.False(t, waffoPancakeWebhookStoreMatches("", "store_123"))
	require.False(t, waffoPancakeWebhookStoreMatches("store_123", ""))
}

func TestWaffoWebhookMerchantOwnershipRequiresExactConfiguredMerchant(t *testing.T) {
	require.True(t, waffoWebhookMerchantMatches(
		map[string]interface{}{"merchantId": "merchant_123"},
		"merchant_123",
	))
	require.True(t, waffoWebhookMerchantMatches(
		map[string]interface{}{"merchantId": " merchant_123 "},
		" merchant_123 ",
	))
	require.False(t, waffoWebhookMerchantMatches(
		map[string]interface{}{"merchantId": "other_merchant"},
		"merchant_123",
	))
	require.False(t, waffoWebhookMerchantMatches(nil, "merchant_123"))
	require.False(t, waffoWebhookMerchantMatches(
		map[string]interface{}{"merchantId": "merchant_123"},
		"",
	))
}

func TestEpayWebhookEnabledRequiresTopUpAndWebhookConfig(t *testing.T) {
	originalPayAddress := operation_setting.PayAddress
	originalEpayID := operation_setting.EpayId
	originalEpayKey := operation_setting.EpayKey
	originalPayMethods := operation_setting.PayMethods
	t.Cleanup(func() {
		operation_setting.PayAddress = originalPayAddress
		operation_setting.EpayId = originalEpayID
		operation_setting.EpayKey = originalEpayKey
		operation_setting.PayMethods = originalPayMethods
	})

	operation_setting.PayAddress = "https://pay.example.com"
	operation_setting.EpayId = "epay_id"
	operation_setting.EpayKey = ""
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	require.False(t, isEpayWebhookEnabled())

	operation_setting.EpayKey = "epay_key"
	require.True(t, isEpayWebhookEnabled())

	operation_setting.PayMethods = nil
	require.True(t, isEpayWebhookEnabled())
}
