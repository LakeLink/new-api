package service

import (
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/shopspring/decimal"
)

const xaiUSDTicksPerDollar = int64(10_000_000_000)

type providerReportedCost struct {
	USD    decimal.Decimal
	Quota  int
	Source string
}

func usesDefaultProviderPricing(relayInfo *relaycommon.RelayInfo) bool {
	if relayInfo == nil || relayInfo.ChannelMeta == nil || relayInfo.TieredBillingSnapshot != nil {
		return false
	}

	switch relayInfo.ChannelType {
	case constant.ChannelTypeXai:
		if relayInfo.PriceData.UsePrice {
			defaultPrice, ok := ratio_setting.GetDefaultModelPriceMap()[relayInfo.OriginModelName]
			return ok && relayInfo.PriceData.ModelPrice == defaultPrice
		}
	case constant.ChannelTypePerplexity:
		if relayInfo.PriceData.UsePrice {
			return false
		}
	default:
		return false
	}

	defaultRatio, ok := ratio_setting.GetDefaultModelRatioMap()[relayInfo.OriginModelName]
	return ok && relayInfo.PriceData.ModelRatio == defaultRatio &&
		relayInfo.PriceData.CompletionRatio == ratio_setting.GetDefaultCompletionRatio(relayInfo.OriginModelName) &&
		relayInfo.PriceData.CacheRatio == ratio_setting.GetDefaultCacheRatio(relayInfo.OriginModelName) &&
		relayInfo.PriceData.CacheCreationRatio == ratio_setting.GetDefaultCreateCacheRatio(relayInfo.OriginModelName)
}

func providerReportedUsageCost(relayInfo *relaycommon.RelayInfo, usage *dto.Usage) (providerReportedCost, bool) {
	if usage == nil || !usesDefaultProviderPricing(relayInfo) {
		return providerReportedCost{}, false
	}

	var costUSD decimal.Decimal
	var source string
	switch relayInfo.ChannelType {
	case constant.ChannelTypeXai:
		if usage.CostInUSDTicks == nil || *usage.CostInUSDTicks <= 0 {
			return providerReportedCost{}, false
		}
		costUSD = decimal.NewFromInt(*usage.CostInUSDTicks).Div(decimal.NewFromInt(xaiUSDTicksPerDollar))
		source = "xai.usage.cost_in_usd_ticks"
	case constant.ChannelTypePerplexity:
		if usage.Cost == nil {
			return providerReportedCost{}, false
		}
		encodedCost, err := common.Marshal(usage.Cost)
		if err != nil {
			return providerReportedCost{}, false
		}
		var parsedCost struct {
			TotalCost decimal.Decimal `json:"total_cost"`
		}
		if err := common.Unmarshal(encodedCost, &parsedCost); err != nil {
			return providerReportedCost{}, false
		}
		costUSD = parsedCost.TotalCost
		source = "perplexity.usage.cost.total_cost"
	default:
		return providerReportedCost{}, false
	}
	if !costUSD.GreaterThan(decimal.Zero) {
		return providerReportedCost{}, false
	}

	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	if groupRatio < 0 || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) {
		return providerReportedCost{}, false
	}
	quotaPerUnit := common.CurrentQuotaPerUnit()
	if quotaPerUnit <= 0 ||
		math.IsNaN(quotaPerUnit) ||
		math.IsInf(quotaPerUnit, 0) ||
		quotaPerUnit > float64(common.MaxQuota) {
		return providerReportedCost{}, false
	}
	quotaDecimal := costUSD.Mul(decimal.NewFromFloat(quotaPerUnit)).Mul(decimal.NewFromFloat(groupRatio))
	quota, clamp := common.QuotaFromDecimalChecked(quotaDecimal)
	noteQuotaClamp(relayInfo, clamp)
	if groupRatio > 0 && quota == 0 {
		quota = 1
	}

	return providerReportedCost{USD: costUSD, Quota: quota, Source: source}, true
}

func perplexityRequestFee(relayInfo *relaycommon.RelayInfo) (float64, decimal.Decimal, bool) {
	if relayInfo == nil || relayInfo.ChannelMeta == nil || relayInfo.ChannelType != constant.ChannelTypePerplexity ||
		!usesDefaultProviderPricing(relayInfo) {
		return 0, decimal.Zero, false
	}
	searchContextSize, searchType := relaycommon.PerplexityRequestPricingParams(relayInfo.Request)
	feeUSD, ok := ratio_setting.GetPerplexityRequestPrice(relayInfo.OriginModelName, searchContextSize, searchType)
	if !ok || feeUSD <= 0 {
		return 0, decimal.Zero, false
	}
	groupRatio := relayInfo.PriceData.GroupRatioInfo.GroupRatio
	if groupRatio < 0 || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) {
		return 0, decimal.Zero, false
	}
	feeQuota := decimal.NewFromFloat(feeUSD).
		Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).
		Mul(decimal.NewFromFloat(groupRatio))
	return feeUSD, feeQuota, true
}

func attachProviderReportedCost(other map[string]interface{}, cost providerReportedCost) {
	if other == nil || cost.Source == "" {
		return
	}
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	if !ok || adminInfo == nil {
		adminInfo = map[string]interface{}{}
		other["admin_info"] = adminInfo
	}
	adminInfo["provider_reported_cost"] = map[string]interface{}{
		"source": cost.Source,
		"usd":    cost.USD.String(),
		"quota":  cost.Quota,
	}
}
