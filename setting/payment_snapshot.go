package setting

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
)

type StripeSettings struct {
	APISecret             string
	WebhookSecret         string
	PriceID               string
	UnitPrice             float64
	MinTopUp              int
	PromotionCodesEnabled bool
}

func GetStripeSettings() StripeSettings {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return StripeSettings{
		APISecret:             optionStringLocked("StripeApiSecret", StripeApiSecret),
		WebhookSecret:         optionStringLocked("StripeWebhookSecret", StripeWebhookSecret),
		PriceID:               optionStringLocked("StripePriceId", StripePriceId),
		UnitPrice:             optionFloat64Locked("StripeUnitPrice", StripeUnitPrice),
		MinTopUp:              optionIntLocked("StripeMinTopUp", StripeMinTopUp),
		PromotionCodesEnabled: optionBoolLocked("StripePromotionCodesEnabled", StripePromotionCodesEnabled),
	}
}

type CreemSettings struct {
	APIKey        string
	Products      string
	TestMode      bool
	WebhookSecret string
}

func GetCreemSettings() CreemSettings {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return CreemSettings{
		APIKey:        optionStringLocked("CreemApiKey", CreemApiKey),
		Products:      optionStringLocked("CreemProducts", CreemProducts),
		TestMode:      optionBoolLocked("CreemTestMode", CreemTestMode),
		WebhookSecret: optionStringLocked("CreemWebhookSecret", CreemWebhookSecret),
	}
}

type WaffoSettings struct {
	Enabled               bool
	APIKey                string
	PrivateKey            string
	PublicCert            string
	SandboxPublicCert     string
	SandboxAPIKey         string
	SandboxPrivateKey     string
	Sandbox               bool
	MerchantID            string
	NotifyURL             string
	ReturnURL             string
	SubscriptionReturnURL string
	Currency              string
	UnitPrice             float64
	MinTopUp              int
	PancakeMerchantID     string
	PancakePrivateKey     string
	PancakeReturnURL      string
	PancakeUnitPrice      float64
	PancakeMinTopUp       int
	PancakeStoreID        string
	PancakeProductID      string
}

func GetWaffoSettings() WaffoSettings {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()
	return WaffoSettings{
		Enabled:               optionBoolLocked("WaffoEnabled", WaffoEnabled),
		APIKey:                optionStringLocked("WaffoApiKey", WaffoApiKey),
		PrivateKey:            optionStringLocked("WaffoPrivateKey", WaffoPrivateKey),
		PublicCert:            optionStringLocked("WaffoPublicCert", WaffoPublicCert),
		SandboxPublicCert:     optionStringLocked("WaffoSandboxPublicCert", WaffoSandboxPublicCert),
		SandboxAPIKey:         optionStringLocked("WaffoSandboxApiKey", WaffoSandboxApiKey),
		SandboxPrivateKey:     optionStringLocked("WaffoSandboxPrivateKey", WaffoSandboxPrivateKey),
		Sandbox:               optionBoolLocked("WaffoSandbox", WaffoSandbox),
		MerchantID:            optionStringLocked("WaffoMerchantId", WaffoMerchantId),
		NotifyURL:             optionStringLocked("WaffoNotifyUrl", WaffoNotifyUrl),
		ReturnURL:             optionStringLocked("WaffoReturnUrl", WaffoReturnUrl),
		SubscriptionReturnURL: optionStringLocked("WaffoSubscriptionReturnUrl", WaffoSubscriptionReturnUrl),
		Currency:              optionStringLocked("WaffoCurrency", WaffoCurrency),
		UnitPrice:             optionFloat64Locked("WaffoUnitPrice", WaffoUnitPrice),
		MinTopUp:              optionIntLocked("WaffoMinTopUp", WaffoMinTopUp),
		PancakeMerchantID:     optionStringLocked("WaffoPancakeMerchantID", WaffoPancakeMerchantID),
		PancakePrivateKey:     optionStringLocked("WaffoPancakePrivateKey", WaffoPancakePrivateKey),
		PancakeReturnURL:      optionStringLocked("WaffoPancakeReturnURL", WaffoPancakeReturnURL),
		PancakeUnitPrice:      optionFloat64Locked("WaffoPancakeUnitPrice", WaffoPancakeUnitPrice),
		PancakeMinTopUp:       optionIntLocked("WaffoPancakeMinTopUp", WaffoPancakeMinTopUp),
		PancakeStoreID:        optionStringLocked("WaffoPancakeStoreID", WaffoPancakeStoreID),
		PancakeProductID:      optionStringLocked("WaffoPancakeProductID", WaffoPancakeProductID),
	}
}

func optionStringLocked(key, fallback string) string {
	if value, ok := common.OptionMap[key]; ok {
		return value
	}
	return fallback
}

func optionBoolLocked(key string, fallback bool) bool {
	value, ok := common.OptionMap[key]
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func optionIntLocked(key string, fallback int) int {
	value, ok := common.OptionMap[key]
	if !ok {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func optionFloat64Locked(key string, fallback float64) float64 {
	value, ok := common.OptionMap[key]
	if !ok {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
