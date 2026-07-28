package service

import (
	"math"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func defaultProviderPriceData(model string, groupRatio float64) types.PriceData {
	return types.PriceData{
		ModelRatio:         ratio_setting.GetDefaultModelRatioMap()[model],
		CompletionRatio:    ratio_setting.GetDefaultCompletionRatio(model),
		CacheRatio:         ratio_setting.GetDefaultCacheRatio(model),
		CacheCreationRatio: ratio_setting.GetDefaultCreateCacheRatio(model),
		GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: groupRatio},
	}
}

func TestProviderReportedUsageCostUsesExactXAITicks(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	ticks := int64(15_000_000_000)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai},
		OriginModelName: "grok-4.5",
		PriceData:       defaultProviderPriceData("grok-4.5", 2),
	}

	cost, ok := providerReportedUsageCost(info, &dto.Usage{CostInUSDTicks: &ticks})

	require.True(t, ok)
	assert.Equal(t, "1.5", cost.USD.String())
	assert.Equal(t, 1_500_000, cost.Quota)
	assert.Equal(t, "xai.usage.cost_in_usd_ticks", cost.Source)
}

func TestProviderReportedUsageCostUsesPerplexityTotalCost(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypePerplexity},
		OriginModelName: "sonar-pro",
		PriceData:       defaultProviderPriceData("sonar-pro", 2),
	}
	cost, ok := providerReportedUsageCost(info, &dto.Usage{Cost: map[string]interface{}{
		"total_cost":   0.018,
		"request_cost": 0.018,
	}})

	require.True(t, ok)
	assert.Equal(t, "0.018", cost.USD.String())
	assert.Equal(t, 18000, cost.Quota)
	assert.Equal(t, "perplexity.usage.cost.total_cost", cost.Source)
}

func TestProviderReportedUsageCostRejectsCustomOrInvalidValues(t *testing.T) {
	ticks := int64(10_000_000_000)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai},
		OriginModelName: "grok-4.5",
		PriceData:       defaultProviderPriceData("grok-4.5", 1),
	}
	info.PriceData.ModelRatio = 1.1
	_, ok := providerReportedUsageCost(info, &dto.Usage{CostInUSDTicks: &ticks})
	assert.False(t, ok)

	info.PriceData = defaultProviderPriceData("grok-4.5", 1)
	negativeTicks := int64(-1)
	_, ok = providerReportedUsageCost(info, &dto.Usage{CostInUSDTicks: &negativeTicks})
	assert.False(t, ok)

	info.ChannelMeta.ChannelType = constant.ChannelTypePerplexity
	info.OriginModelName = "sonar"
	info.PriceData = defaultProviderPriceData("sonar", 1)
	_, ok = providerReportedUsageCost(info, &dto.Usage{Cost: map[string]interface{}{"total_cost": -1}})
	assert.False(t, ok)
}

func TestProviderReportedUsageCostRejectsInvalidQuotaUnit(t *testing.T) {
	common.OptionMapRWMutex.Lock()
	optionMapWasNil := common.OptionMap == nil
	if optionMapWasNil {
		common.OptionMap = make(map[string]string)
	}
	originalValue, hadOriginalValue := common.OptionMap["QuotaPerUnit"]
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		defer common.OptionMapRWMutex.Unlock()
		if optionMapWasNil {
			common.OptionMap = nil
			return
		}
		if hadOriginalValue {
			common.OptionMap["QuotaPerUnit"] = originalValue
			return
		}
		delete(common.OptionMap, "QuotaPerUnit")
	})

	ticks := int64(10_000_000_000)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai},
		OriginModelName: "grok-4.5",
		PriceData:       defaultProviderPriceData("grok-4.5", 1),
	}
	for _, quotaPerUnit := range []float64{
		0,
		-1,
		math.NaN(),
		math.Inf(1),
		float64(common.MaxQuota) + 1,
	} {
		common.OptionMapRWMutex.Lock()
		common.OptionMap["QuotaPerUnit"] = strconv.FormatFloat(quotaPerUnit, 'g', -1, 64)
		common.OptionMapRWMutex.Unlock()

		_, ok := providerReportedUsageCost(info, &dto.Usage{CostInUSDTicks: &ticks})
		assert.False(t, ok)
	}
}

func TestProviderReportedUsageCostAuditsSaturation(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	ticks := int64(math.MaxInt64)
	info := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai},
		OriginModelName: "grok-4.5",
		PriceData:       defaultProviderPriceData("grok-4.5", 1),
	}

	cost, ok := providerReportedUsageCost(info, &dto.Usage{CostInUSDTicks: &ticks})

	require.True(t, ok)
	assert.Equal(t, common.MaxQuota, cost.Quota)
	require.NotNil(t, info.QuotaClamp)
	assert.Equal(t, common.QuotaClampOverflow, info.QuotaClamp.Kind)
	other := map[string]interface{}{}
	attachQuotaSaturationToOther(other, info.QuotaClamp)
	adminInfo := other["admin_info"].(map[string]interface{})
	assert.NotNil(t, adminInfo["quota_saturation"])
}

func TestProviderBillingRemainsBillableWithZeroTokens(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	perplexityInfo := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypePerplexity},
		OriginModelName: "sonar",
		PriceData:       defaultProviderPriceData("sonar", 1),
		Request:         &dto.GeneralOpenAIRequest{Model: "sonar"},
	}
	perplexitySummary := calculateTextQuotaSummary(ctx, perplexityInfo, &dto.Usage{})
	assert.Equal(t, 2500, perplexitySummary.Quota)
	assert.True(t, hasBillableTextUsage(perplexitySummary, perplexityInfo))

	ticks := int64(10_000_000_000)
	xaiInfo := &relaycommon.RelayInfo{
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXai},
		OriginModelName: "grok-4.5",
		PriceData:       defaultProviderPriceData("grok-4.5", 1),
	}
	xaiSummary := calculateTextQuotaSummary(ctx, xaiInfo, &dto.Usage{})
	reportedCost, ok := providerReportedUsageCost(xaiInfo, &dto.Usage{CostInUSDTicks: &ticks})
	require.True(t, ok)
	xaiSummary.Quota = reportedCost.Quota
	xaiSummary.ProviderReportedCost = &reportedCost
	assert.Equal(t, 500000, xaiSummary.Quota)
	assert.True(t, hasBillableTextUsage(xaiSummary, xaiInfo))
}

func TestXAITokenMultipliersDoNotIncreaseToolSurcharges(t *testing.T) {
	priceData := types.PriceData{}
	priceData.AddOtherRatio("xai_priority", 2)
	priceData.AddOtherRatio("xai_long_context", 2)
	surcharge := decimal.NewFromInt(100)

	normalized := normalizeToolCallSurchargeForSharedRatios(surcharge, priceData)
	afterSharedRatios := priceData.ApplyOtherRatiosToDecimal(normalized)

	assert.True(t, surcharge.Equal(afterSharedRatios))
}

func TestAttachProviderReportedCostIsAdminOnly(t *testing.T) {
	other := map[string]interface{}{"admin_info": map[string]interface{}{"use_channel": []string{"xai"}}}
	cost := providerReportedCost{USD: decimal.NewFromFloat(0.25), Quota: 125000, Source: "xai.usage.cost_in_usd_ticks"}

	attachProviderReportedCost(other, cost)

	adminInfo := other["admin_info"].(map[string]interface{})
	assert.NotNil(t, adminInfo["provider_reported_cost"])
	_, exposedAtTopLevel := other["provider_reported_cost"]
	assert.False(t, exposedAtTopLevel)
}
