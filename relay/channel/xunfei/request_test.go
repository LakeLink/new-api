package xunfei

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestOpenAIToXunfeiPreservesExplicitZeroScalars(t *testing.T) {
	topK := 0
	maxTokens := uint(0)
	request := requestOpenAI2Xunfei(dto.GeneralOpenAIRequest{
		TopK:                &topK,
		MaxCompletionTokens: &maxTokens,
	}, "app-id", "4.0Ultra")

	require.NotNil(t, request.Parameter.Chat.TopK)
	assert.Equal(t, 0, *request.Parameter.Chat.TopK)
	require.NotNil(t, request.Parameter.Chat.MaxTokens)
	assert.Equal(t, uint(0), *request.Parameter.Chat.MaxTokens)

	body, err := common.Marshal(request)
	require.NoError(t, err)
	assert.Contains(t, string(body), `"top_k":0`)
	assert.Contains(t, string(body), `"max_tokens":0`)
}

func TestRequestOpenAIToXunfeiPrefersMaxCompletionTokensWhenExplicitlyZero(t *testing.T) {
	maxTokens := uint(1024)
	maxCompletionTokens := uint(0)

	request := requestOpenAI2Xunfei(dto.GeneralOpenAIRequest{
		MaxTokens:           &maxTokens,
		MaxCompletionTokens: &maxCompletionTokens,
	}, "app-id", "4.0Ultra")

	require.NotNil(t, request.Parameter.Chat.MaxTokens)
	assert.Equal(t, uint(0), *request.Parameter.Chat.MaxTokens)
}

func TestBuildXunfeiAuthURLRejectsMalformedEndpoint(t *testing.T) {
	_, err := buildXunfeiAuthURL(
		"wss://spark-api.xf-yun.com/%zz/chat",
		"api-key",
		"api-secret",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid Xunfei endpoint")
}

func TestBuildXunfeiAuthURLRejectsNonWebSocketScheme(t *testing.T) {
	_, err := buildXunfeiAuthURL(
		"https://spark-api.xf-yun.com/v3.5/chat",
		"api-key",
		"api-secret",
	)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "websocket endpoint")
}
