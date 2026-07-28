package operation_setting

import (
	"fmt"
	"math"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
)

// 额度展示类型
const (
	QuotaDisplayTypeUSD    = "USD"
	QuotaDisplayTypeCNY    = "CNY"
	QuotaDisplayTypeTokens = "TOKENS"
	QuotaDisplayTypeCustom = "CUSTOM"
)

type GeneralSetting struct {
	DocsLink            string `json:"docs_link"`
	PingIntervalEnabled bool   `json:"ping_interval_enabled"`
	PingIntervalSeconds int    `json:"ping_interval_seconds"`
	// 当前站点额度展示类型：USD / CNY / TOKENS
	QuotaDisplayType string `json:"quota_display_type"`
	// 自定义货币符号，用于 CUSTOM 展示类型
	CustomCurrencySymbol string `json:"custom_currency_symbol"`
	// 自定义货币与美元汇率（1 USD = X Custom）
	CustomCurrencyExchangeRate float64 `json:"custom_currency_exchange_rate"`
}

// 默认配置
var generalSetting = GeneralSetting{
	DocsLink:                   "https://docs.newapi.pro",
	PingIntervalEnabled:        false,
	PingIntervalSeconds:        60,
	QuotaDisplayType:           QuotaDisplayTypeUSD,
	CustomCurrencySymbol:       "¤",
	CustomCurrencyExchangeRate: 1.0,
}

func (s GeneralSetting) Validate() error {
	maxSeconds := int64(math.MaxInt64 / int64(time.Second))
	if s.PingIntervalSeconds < 1 || int64(s.PingIntervalSeconds) > maxSeconds {
		return fmt.Errorf("ping interval must be in the range [1, %d] seconds", maxSeconds)
	}
	switch s.QuotaDisplayType {
	case QuotaDisplayTypeUSD, QuotaDisplayTypeCNY, QuotaDisplayTypeTokens, QuotaDisplayTypeCustom:
	default:
		return fmt.Errorf("quota display type must be one of USD, CNY, TOKENS, or CUSTOM")
	}
	rate := s.CustomCurrencyExchangeRate
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 || rate > float64(common.MaxQuota) {
		return fmt.Errorf("custom currency exchange rate must be finite and in the range (0, %d]", common.MaxQuota)
	}
	return nil
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("general_setting", &generalSetting)
}

func GetGeneralSetting() *GeneralSetting {
	return config.Snapshot[GeneralSetting]("general_setting")
}

// IsCurrencyDisplay 是否以货币形式展示（美元或人民币）
func IsCurrencyDisplay() bool {
	return GetGeneralSetting().QuotaDisplayType != QuotaDisplayTypeTokens
}

// IsCNYDisplay 是否以人民币展示
func IsCNYDisplay() bool {
	return GetGeneralSetting().QuotaDisplayType == QuotaDisplayTypeCNY
}

// GetQuotaDisplayType 返回额度展示类型
func GetQuotaDisplayType() string {
	return GetGeneralSetting().QuotaDisplayType
}

// GetCurrencySymbol 返回当前展示类型对应符号
func GetCurrencySymbol() string {
	setting := GetGeneralSetting()
	switch setting.QuotaDisplayType {
	case QuotaDisplayTypeUSD:
		return "$"
	case QuotaDisplayTypeCNY:
		return "¥"
	case QuotaDisplayTypeCustom:
		if setting.CustomCurrencySymbol != "" {
			return setting.CustomCurrencySymbol
		}
		return "¤"
	default:
		return ""
	}
}

// GetUsdToCurrencyRate 返回 1 USD = X <currency> 的 X（TOKENS 不适用）
func GetUsdToCurrencyRate(usdToCny float64) float64 {
	setting := GetGeneralSetting()
	switch setting.QuotaDisplayType {
	case QuotaDisplayTypeUSD:
		return 1
	case QuotaDisplayTypeCNY:
		return usdToCny
	case QuotaDisplayTypeCustom:
		if setting.CustomCurrencyExchangeRate > 0 {
			return setting.CustomCurrencyExchangeRate
		}
		return 1
	default:
		return 1
	}
}
