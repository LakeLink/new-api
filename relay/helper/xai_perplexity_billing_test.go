package helper

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestXAIPreConsumeMultiplier(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeXai)
	common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowServiceTier: true})
	info := &relaycommon.RelayInfo{
		OriginModelName: "grok-4.5",
		Request: &dto.GeneralOpenAIRequest{
			ServiceTier: []byte(`"priority"`),
		},
	}
	modelRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	require.True(t, ok)

	multiplier := xaiPreConsumeMultiplier(
		ctx,
		info,
		ratio_setting.XAILongContextThreshold+1,
		modelRatio,
		ratio_setting.GetDefaultCompletionRatio(info.OriginModelName),
		ratio_setting.GetDefaultCacheRatio(info.OriginModelName),
		ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName),
	)

	assert.Equal(t, 4.0, multiplier)
}

func TestXAIPreConsumeIgnoresFilteredOrCustomPriority(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeXai)
	info := &relaycommon.RelayInfo{
		OriginModelName: "grok-4.5",
		Request: &dto.GeneralOpenAIRequest{
			ServiceTier: []byte(`"priority"`),
		},
	}
	modelRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	require.True(t, ok)
	completionRatio := ratio_setting.GetDefaultCompletionRatio(info.OriginModelName)
	cacheRatio := ratio_setting.GetDefaultCacheRatio(info.OriginModelName)
	cacheCreationRatio := ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName)

	assert.Equal(t, 1.0, xaiPreConsumeMultiplier(ctx, info, 100, modelRatio, completionRatio, cacheRatio, cacheCreationRatio))
	common.SetContextKey(ctx, constant.ContextKeyChannelOtherSetting, dto.ChannelOtherSettings{AllowServiceTier: true})
	assert.Equal(t, 1.0, xaiPreConsumeMultiplier(ctx, info, 100, modelRatio+0.1, completionRatio, cacheRatio, cacheCreationRatio))
}

func TestPerplexityRequestFeePreConsume(t *testing.T) {
	originalQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = originalQuotaPerUnit })

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypePerplexity)
	info := &relaycommon.RelayInfo{
		OriginModelName: "sonar-pro",
		Request: &dto.GeneralOpenAIRequest{WebSearchOptions: &dto.WebSearchOptions{
			SearchContextSize: "medium",
			SearchType:        "pro",
		}},
	}
	priceData := types.PriceData{
		ModelRatio:         1.5,
		CompletionRatio:    5,
		CacheRatio:         1,
		CacheCreationRatio: 1.25,
		QuotaToPreConsume:  100,
		GroupRatioInfo:     types.GroupRatioInfo{GroupRatio: 2},
	}

	require.NoError(t, applyPerplexityRequestFeePreConsume(ctx, info, &priceData))
	assert.Equal(t, 18100, priceData.QuotaToPreConsume)
}
