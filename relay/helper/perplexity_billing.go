package helper

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func applyPerplexityRequestFeePreConsume(c *gin.Context, info *relaycommon.RelayInfo, priceData *types.PriceData) error {
	if info == nil || priceData == nil || common.GetContextKeyInt(c, constant.ContextKeyChannelType) != constant.ChannelTypePerplexity ||
		priceData.UsePrice || info.TieredBillingSnapshot != nil {
		return nil
	}
	defaultRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	if !ok || priceData.ModelRatio != defaultRatio ||
		priceData.CompletionRatio != ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) ||
		priceData.CacheRatio != ratio_setting.GetDefaultCacheRatio(info.OriginModelName) ||
		priceData.CacheCreationRatio != ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName) {
		return nil
	}

	searchContextSize, searchType := relaycommon.PerplexityRequestPricingParams(info.Request)
	feeUSD, ok := ratio_setting.GetPerplexityRequestPrice(info.OriginModelName, searchContextSize, searchType)
	if !ok || feeUSD <= 0 {
		return nil
	}
	groupRatio := priceData.GroupRatioInfo.GroupRatio
	if groupRatio < 0 || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) {
		return fmt.Errorf("invalid Perplexity billing group ratio")
	}
	feeQuota := decimal.NewFromFloat(feeUSD).
		Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).
		Mul(decimal.NewFromFloat(groupRatio))
	totalQuota, clamp := common.QuotaFromDecimalChecked(decimal.NewFromInt(int64(priceData.QuotaToPreConsume)).Add(feeQuota))
	if clamp != nil {
		return clamp
	}
	priceData.QuotaToPreConsume = totalQuota
	return nil
}
