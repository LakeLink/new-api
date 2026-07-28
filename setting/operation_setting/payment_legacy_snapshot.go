package operation_setting

import (
	"strconv"

	"github.com/QuantumNous/new-api/common"
)

type LegacyPaymentSetting struct {
	PayAddress            string
	CustomCallbackAddress string
	EpayID                string
	EpayKey               string
	Price                 float64
	MinTopUp              int
	USDExchangeRate       float64
	PayMethods            []map[string]string
}

func GetLegacyPaymentSetting() LegacyPaymentSetting {
	common.OptionMapRWMutex.RLock()
	defer common.OptionMapRWMutex.RUnlock()

	setting := LegacyPaymentSetting{
		PayAddress:            legacyPaymentStringLocked("PayAddress", PayAddress),
		CustomCallbackAddress: legacyPaymentStringLocked("CustomCallbackAddress", CustomCallbackAddress),
		EpayID:                legacyPaymentStringLocked("EpayId", EpayId),
		EpayKey:               legacyPaymentStringLocked("EpayKey", EpayKey),
		Price:                 legacyPaymentFloatLocked("Price", Price),
		MinTopUp:              legacyPaymentIntLocked("MinTopUp", MinTopUp),
		USDExchangeRate:       legacyPaymentFloatLocked("USDExchangeRate", USDExchangeRate),
	}
	if raw, ok := common.OptionMap["PayMethods"]; ok {
		if err := common.UnmarshalJsonStr(raw, &setting.PayMethods); err == nil {
			return setting
		}
	}
	setting.PayMethods = make([]map[string]string, len(PayMethods))
	for i, method := range PayMethods {
		setting.PayMethods[i] = make(map[string]string, len(method))
		for key, value := range method {
			setting.PayMethods[i][key] = value
		}
	}
	return setting
}

func legacyPaymentStringLocked(key, fallback string) string {
	if value, ok := common.OptionMap[key]; ok {
		return value
	}
	return fallback
}

func legacyPaymentIntLocked(key string, fallback int) int {
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

func legacyPaymentFloatLocked(key string, fallback float64) float64 {
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
