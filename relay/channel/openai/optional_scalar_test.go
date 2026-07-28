package openai

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReasoningModelConversionDoesNotOverwriteExplicitZeroMaxCompletionTokens(t *testing.T) {
	legacyMax := uint(4096)
	explicitZero := uint(0)
	request := &dto.GeneralOpenAIRequest{
		Model:               "gpt-5",
		MaxTokens:           &legacyMax,
		MaxCompletionTokens: &explicitZero,
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelType:       constant.ChannelTypeOpenAI,
			UpstreamModelName: "gpt-5",
		},
	}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	upstream, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, upstream.MaxCompletionTokens)
	assert.Zero(t, *upstream.MaxCompletionTokens)
	require.NotNil(t, upstream.MaxTokens)
	assert.Equal(t, legacyMax, *upstream.MaxTokens)
}
