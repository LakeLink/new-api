package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestShouldRejectCrossProtocol(t *testing.T) {
	tests := []struct {
		name          string
		channelType   int
		setting       dto.ChannelSettings
		requestFormat types.RelayFormat
		want          bool
	}{
		{
			name:          "openai denies claude request",
			channelType:   constant.ChannelTypeOpenAI,
			setting:       dto.ChannelSettings{DenyCrossProtocol: true},
			requestFormat: types.RelayFormatClaude,
			want:          true,
		},
		{
			name:          "openai accepts openai request",
			channelType:   constant.ChannelTypeOpenAI,
			setting:       dto.ChannelSettings{DenyCrossProtocol: true},
			requestFormat: types.RelayFormatOpenAI,
			want:          false,
		},
		{
			name:          "anthropic denies openai responses request",
			channelType:   constant.ChannelTypeAnthropic,
			setting:       dto.ChannelSettings{DenyCrossProtocol: true},
			requestFormat: types.RelayFormatOpenAIResponses,
			want:          true,
		},
		{
			name:          "anthropic accepts claude request",
			channelType:   constant.ChannelTypeAnthropic,
			setting:       dto.ChannelSettings{DenyCrossProtocol: true},
			requestFormat: types.RelayFormatClaude,
			want:          false,
		},
		{
			name:          "disabled keeps existing behavior",
			channelType:   constant.ChannelTypeOpenAI,
			setting:       dto.ChannelSettings{DenyCrossProtocol: false},
			requestFormat: types.RelayFormatClaude,
			want:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, shouldRejectCrossProtocol(tt.channelType, tt.setting, tt.requestFormat))
		})
	}
}

func TestProtocolMismatchErrorRetriesSelection(t *testing.T) {
	err := types.NewError(errors.New("protocol mismatch"), types.ErrorCodeChannelProtocolMismatch)

	require.True(t, shouldRetry(nil, err, 1))
	require.True(t, types.IsChannelError(err))
}

func TestInitialContextChannelProtocolMismatchUsesOriginalFormatAndPreparesRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	common.SetContextKey(c, constant.ContextKeyChannelId, 123)
	common.SetContextKey(c, constant.ContextKeyChannelName, "openai-deny-cross-protocol")
	common.SetContextKey(c, constant.ContextKeyChannelType, constant.ChannelTypeOpenAI)
	common.SetContextKey(c, constant.ContextKeyChannelSetting, dto.ChannelSettings{DenyCrossProtocol: true})

	info := &relaycommon.RelayInfo{
		RelayFormat:             types.RelayFormatClaude,
		RequestConversionChain:  []types.RelayFormat{types.RelayFormatClaude, types.RelayFormatOpenAI},
		FinalRequestRelayFormat: types.RelayFormatOpenAI,
		OriginModelName:         "claude-sonnet-4",
	}
	retry := 0
	retryParam := &service.RetryParam{Ctx: c, Retry: &retry}

	channel, err := getChannel(c, info, retryParam)

	require.Nil(t, channel)
	require.NotNil(t, err)
	require.Equal(t, types.ErrorCodeChannelProtocolMismatch, err.GetErrorCode())
	require.NotNil(t, info.ChannelMeta)
	require.Equal(t, constant.ChannelTypeOpenAI, info.ChannelMeta.ChannelType)
}
