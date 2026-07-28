package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelPriceHelperPreConsumesCohereSearchUnits(t *testing.T) {
	savedModelPrices := ratio_setting.ModelPrice2JSONString()
	savedGroupRatios := ratio_setting.GroupRatio2JSONString()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(savedModelPrices))
		require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(savedGroupRatios))
	})
	ratio_setting.InitRatioSettings()
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"default":1}`))

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	ctx.Set("group", "default")
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeCohere)
	oneChunk := 1
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeRerank,
		OriginModelName: "rerank-v4.0-pro",
		UserGroup:       "default",
		UsingGroup:      "default",
		Request: &dto.RerankRequest{
			Documents:       make([]any, 101),
			MaxChunksPerDoc: &oneChunk,
		},
	}

	priceData, err := ModelPriceHelper(ctx, info, 0, &types.TokenCountMeta{})
	require.NoError(t, err)
	assert.True(t, priceData.UsePrice)
	assert.Equal(t, 2500, priceData.QuotaToPreConsume)
	assert.Equal(t, float64(2), priceData.OtherRatios()[relaycommon.CohereRerankSearchUnitsRatioKey])
}

func TestApplyCohereRerankSearchUnitPreConsumeUsesConservativeUnits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeCohere)
	oneChunk := 1
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeRerank,
		OriginModelName: "rerank-v4.0-pro",
		Request: &dto.RerankRequest{
			Documents:       make([]any, 101),
			MaxChunksPerDoc: &oneChunk,
		},
	}
	priceData := types.PriceData{
		UsePrice:   true,
		ModelPrice: 2.5 / 1000,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: 2,
		},
	}
	priceData.AddOtherRatio("tenant_markup", 1.5)

	err := applyCohereRerankSearchUnitPreConsume(ctx, info, &priceData)
	require.NoError(t, err)
	assert.Equal(t, 7500, priceData.QuotaToPreConsume)
	assert.Equal(t, float64(2), priceData.OtherRatios()[relaycommon.CohereRerankSearchUnitsRatioKey])
}

func TestApplyCohereRerankSearchUnitPreConsumeFailsClosedOnSaturation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeCohere)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeRerank,
		OriginModelName: "rerank-v4.0-fast",
		Request: &dto.RerankRequest{
			Documents: []any{"document"},
		},
	}
	priceData := types.PriceData{
		UsePrice:   true,
		ModelPrice: float64(common.MaxQuota)/common.QuotaPerUnit + 1,
		GroupRatioInfo: types.GroupRatioInfo{
			GroupRatio: 1,
		},
	}

	err := applyCohereRerankSearchUnitPreConsume(ctx, info, &priceData)
	require.Error(t, err)
	require.NotNil(t, info.QuotaClamp)
	assert.Equal(t, common.QuotaClampOverflow, info.QuotaClamp.Kind)
	assert.Zero(t, priceData.QuotaToPreConsume)
}

func TestApplyCohereRerankSearchUnitPreConsumeRequiresPerSearchPrice(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeCohere)
	info := &relaycommon.RelayInfo{
		RelayMode:       relayconstant.RelayModeRerank,
		OriginModelName: "custom-rerank",
		Request: &dto.RerankRequest{
			Documents: []any{"document"},
		},
	}

	err := applyCohereRerankSearchUnitPreConsume(ctx, info, &types.PriceData{})
	require.ErrorContains(t, err, "requires a fixed price per search unit")
}
