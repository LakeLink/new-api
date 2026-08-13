package helper

import (
	"fmt"
	"math"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	hosttypes "github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// applyCohereRerankSearchUnitPreConsume changes Cohere Rerank's fixed model
// price from a per-request charge into the provider's documented per-search
// charge. It uses a conservative chunk bound so the later provider-reported
// settlement normally refunds quota instead of unexpectedly overdrawing it.
func applyCohereRerankSearchUnitPreConsume(c *gin.Context, info *relaycommon.RelayInfo, priceData *hosttypes.PriceData) error {
	if info == nil || priceData == nil || info.RelayMode != relayconstant.RelayModeRerank ||
		common.GetContextKeyInt(c, constant.ContextKeyChannelType) != constant.ChannelTypeCohere {
		return nil
	}
	request, ok := info.Request.(*dto.RerankRequest)
	if !ok {
		return fmt.Errorf("invalid Cohere rerank request type %T", info.Request)
	}
	if !priceData.UsePrice {
		return fmt.Errorf("Cohere rerank model %s requires a fixed price per search unit", info.OriginModelName)
	}
	if priceData.ModelPrice < 0 || math.IsNaN(priceData.ModelPrice) || math.IsInf(priceData.ModelPrice, 0) {
		return fmt.Errorf("Cohere rerank model %s has an invalid search-unit price", info.OriginModelName)
	}
	groupRatio := priceData.GroupRatioInfo.GroupRatio
	if groupRatio < 0 || math.IsNaN(groupRatio) || math.IsInf(groupRatio, 0) {
		return fmt.Errorf("Cohere rerank model %s has an invalid group ratio", info.OriginModelName)
	}

	searchUnits, err := relaycommon.EstimateCohereRerankSearchUnits(request)
	if err != nil {
		return err
	}
	priceData.AddOtherRatio(relaycommon.CohereRerankSearchUnitsRatioKey, float64(searchUnits))
	quotaDecimal := decimal.NewFromFloat(priceData.ModelPrice).
		Mul(decimal.NewFromFloat(common.CurrentQuotaPerUnit())).
		Mul(decimal.NewFromFloat(groupRatio))
	quotaDecimal = priceData.ApplyOtherRatiosToDecimal(quotaDecimal)
	quota, clamp := common.QuotaFromDecimalChecked(quotaDecimal)
	if clamp != nil {
		if info.QuotaClamp == nil {
			info.QuotaClamp = clamp
		}
		return clamp
	}
	if quota < 0 {
		return fmt.Errorf("Cohere rerank pre-consume quota cannot be negative: %d", quota)
	}
	priceData.QuotaToPreConsume = quota
	return nil
}
