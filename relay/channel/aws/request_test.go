package aws

import (
	"bytes"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBedrockAPIKeyUsesSDKBearerTransport(t *testing.T) {
	adaptor := &Adaptor{}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{
			ChannelOtherSettings: dto.ChannelOtherSettings{AwsKeyType: dto.AwsKeyTypeApiKey},
			ApiKey:               "bedrock-api-key|us-east-1",
			UpstreamModelName:    "claude-3-5-sonnet-20240620",
		},
	}

	requestURL, err := adaptor.GetRequestURL(info)

	require.NoError(t, err)
	assert.Empty(t, requestURL)
	assert.Equal(t, ClientModeAKSK, adaptor.ClientMode)
}

func TestBedrockAPIKeyRequiresTokenAndRegion(t *testing.T) {
	for _, apiKey := range []string{"", "token", "token|", "|us-east-1", "token|region|extra"} {
		t.Run(apiKey, func(t *testing.T) {
			adaptor := &Adaptor{}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{
					ChannelOtherSettings: dto.ChannelOtherSettings{AwsKeyType: dto.AwsKeyTypeApiKey},
					ApiKey:               apiKey,
				},
			}

			_, err := adaptor.GetRequestURL(info)

			require.Error(t, err)
		})
	}
}

func TestNovaRequestPreservesExplicitZeroInferenceValues(t *testing.T) {
	zeroUint := uint(0)
	zeroFloat := 0.0
	zeroInt := 0
	request := &dto.GeneralOpenAIRequest{
		Model:               "amazon.nova-pro-v1:0",
		Messages:            []dto.Message{{Role: "user", Content: "hello"}},
		MaxCompletionTokens: &zeroUint,
		Temperature:         &zeroFloat,
		TopP:                &zeroFloat,
		TopK:                &zeroInt,
	}

	converted, err := convertToNovaRequest(request)
	require.NoError(t, err)
	require.NotNil(t, converted.InferenceConfig)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)

	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	config, ok := payload["inferenceConfig"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(0), config["maxTokens"])
	assert.Equal(t, float64(0), config["temperature"])
	assert.Equal(t, float64(0), config["topP"])
	assert.Equal(t, float64(0), config["topK"])
}

func TestBedrockClaudeRequestMapsLegacyMaxTokensAndPreservesZero(t *testing.T) {
	converted, err := formatRequest(
		bytes.NewBufferString(`{"messages":[],"max_tokens_to_sample":0}`),
		http.Header{},
	)
	require.NoError(t, err)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Equal(t, float64(0), payload["max_tokens"])
	assert.NotContains(t, payload, "max_tokens_to_sample")
}

func TestBedrockCatalogMapsClaudeOpus5ModelID(t *testing.T) {
	assert.Equal(t, "anthropic.claude-opus-5", getAwsModelID("claude-opus-5"))

	models := (&Adaptor{}).GetModelList()
	assert.Contains(t, models, "claude-opus-5")
}

func TestNovaRequestSeparatesSystemInstructions(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "nova-pro-v1:0",
		Messages: []dto.Message{
			{Role: "system", Content: "system rule"},
			{Role: "developer", Content: "developer rule"},
			{Role: "user", Content: "hello"},
		},
	}

	converted, err := convertToNovaRequest(request)

	require.NoError(t, err)
	assert.Equal(t, []NovaContent{{Text: "system rule"}, {Text: "developer rule"}}, converted.System)
	require.Len(t, converted.Messages, 1)
	assert.Equal(t, "user", converted.Messages[0].Role)
	assert.Equal(t, "hello", converted.Messages[0].Content[0].Text)
}

func TestNovaRequestRejectsUnsupportedChatModalities(t *testing.T) {
	request := &dto.GeneralOpenAIRequest{
		Model: "nova-lite-v1:0",
		Messages: []dto.Message{{
			Role: "user",
			Content: []any{
				map[string]any{"type": "text", "text": "describe"},
				map[string]any{"type": "image_url", "image_url": map[string]any{"url": "https://example.test/image.png"}},
			},
		}},
	}

	_, err := convertToNovaRequest(request)

	require.ErrorContains(t, err, "image_url content is not supported")
}

func TestNovaCrossRegionUsesOnlyPublishedGeoProfiles(t *testing.T) {
	modelID := "amazon.nova-pro-v1:0"

	assert.True(t, awsModelCanCrossRegion(modelID, "us"))
	assert.True(t, awsModelCanCrossRegion(modelID, "eu"))
	assert.False(t, awsModelCanCrossRegion(modelID, "ap"))
	assert.Equal(t, "us.amazon.nova-pro-v1:0", awsModelCrossRegion(modelID, "us"))
	assert.True(t, isNovaTextModel("us.amazon.nova-pro-v1:0"))
}

func TestBedrockChatCatalogExcludesNonChatNovaModels(t *testing.T) {
	models := (&Adaptor{}).GetModelList()

	assert.NotContains(t, models, "nova-canvas-v1:0")
	assert.NotContains(t, models, "nova-reel-v1:0")
	assert.NotContains(t, models, "nova-sonic-v1:0")
}

func TestNovaResponsePreservesAllTextAndStopReason(t *testing.T) {
	response := NovaResponse{
		StopReason: "max_tokens",
		Usage: NovaUsage{
			InputTokens:  3,
			OutputTokens: 4,
			TotalTokens:  7,
		},
	}
	response.Output.Message.Content = []NovaResponseContent{
		{Text: "hello "},
		{Text: "world"},
	}

	converted := novaResponseToOpenAI("chatcmpl-test", 123, "nova-pro-v1:0", response)

	require.Len(t, converted.Choices, 1)
	assert.Equal(t, "hello world", converted.Choices[0].Message.Content)
	assert.Equal(t, "length", converted.Choices[0].FinishReason)
	assert.Equal(t, 7, converted.Usage.TotalTokens)
}

func TestNovaResponseHandlesEmptyContentWithoutPanicking(t *testing.T) {
	response := NovaResponse{StopReason: "content_filtered"}

	converted := novaResponseToOpenAI("chatcmpl-test", 123, "nova-pro-v1:0", response)

	require.Len(t, converted.Choices, 1)
	assert.Equal(t, "", converted.Choices[0].Message.Content)
	assert.Equal(t, "content_filter", converted.Choices[0].FinishReason)
}
