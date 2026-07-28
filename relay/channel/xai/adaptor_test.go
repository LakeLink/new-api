package xai

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertImageRequestRejectsUnsupportedEdits(t *testing.T) {
	_, err := (&Adaptor{}).ConvertImageRequest(nil, &relaycommon.RelayInfo{
		RelayMode: relayconstant.RelayModeImagesEdits,
	}, dto.ImageRequest{Model: "grok-imagine-image", Prompt: "edit"})

	require.Error(t, err)
	assert.ErrorContains(t, err, "not supported")
}

func TestConvertOpenAIRequestDoesNotOverwriteExplicitZeroMaxCompletionTokens(t *testing.T) {
	legacyMax := uint(4096)
	explicitZero := uint(0)
	request := &dto.GeneralOpenAIRequest{
		Model:               "grok-3-mini",
		MaxTokens:           &legacyMax,
		MaxCompletionTokens: &explicitZero,
	}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "grok-3-mini"}}

	converted, err := (&Adaptor{}).ConvertOpenAIRequest(nil, info, request)
	require.NoError(t, err)
	upstream, ok := converted.(*dto.GeneralOpenAIRequest)
	require.True(t, ok)
	require.NotNil(t, upstream.MaxCompletionTokens)
	assert.Zero(t, *upstream.MaxCompletionTokens)
	require.NotNil(t, upstream.MaxTokens)
	assert.Equal(t, legacyMax, *upstream.MaxTokens)
}
