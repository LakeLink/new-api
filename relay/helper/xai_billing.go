package helper

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/setting/model_setting"
	"github.com/QuantumNous/new-api/setting/ratio_setting"

	"github.com/gin-gonic/gin"
)

func xaiPreConsumeMultiplier(c *gin.Context, info *relaycommon.RelayInfo, promptTokens int, modelRatio, completionRatio, cacheRatio, cacheCreationRatio float64) float64 {
	if common.GetContextKeyInt(c, constant.ContextKeyChannelType) != constant.ChannelTypeXai {
		return 1
	}
	defaultRatio, ok := ratio_setting.GetDefaultModelRatioMap()[info.OriginModelName]
	if !ok || modelRatio != defaultRatio ||
		completionRatio != ratio_setting.GetDefaultCompletionRatio(info.OriginModelName) ||
		cacheRatio != ratio_setting.GetDefaultCacheRatio(info.OriginModelName) ||
		cacheCreationRatio != ratio_setting.GetDefaultCreateCacheRatio(info.OriginModelName) {
		return 1
	}

	multiplier := 1.0
	channelOtherSettings, _ := common.GetContextKeyType[dto.ChannelOtherSettings](c, constant.ContextKeyChannelOtherSetting)
	channelSettings, _ := common.GetContextKeyType[dto.ChannelSettings](c, constant.ContextKeyChannelSetting)
	passesRawBody := model_setting.GetGlobalSettings().PassThroughRequestEnabled ||
		channelSettings.PassThroughBodyEnabled ||
		(info.ChannelMeta != nil && info.ChannelSetting.PassThroughBodyEnabled)
	hasParamOverride := info.ChannelMeta != nil && len(info.ChannelMeta.ParamOverride) > 0
	if channelOtherSettings.AllowServiceTier || passesRawBody || hasParamOverride {
		requestedServiceTier := ""
		switch request := info.Request.(type) {
		case *dto.GeneralOpenAIRequest:
			if len(request.ServiceTier) > 0 {
				_ = common.Unmarshal(request.ServiceTier, &requestedServiceTier)
			}
		case *dto.OpenAIResponsesRequest:
			requestedServiceTier = request.ServiceTier
		case *dto.OpenAIResponsesCompactionRequest:
			requestedServiceTier = request.ServiceTier
		}
		if strings.EqualFold(requestedServiceTier, "priority") {
			multiplier *= 2
		}
	}

	if promptTokens > ratio_setting.XAILongContextThreshold && ratio_setting.IsXAILongContextModel(info.OriginModelName) {
		multiplier *= 2
	}
	return multiplier
}
