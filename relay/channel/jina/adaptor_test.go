package jina

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertRerankRequestPreservesMultimodalM0Query(t *testing.T) {
	topN := 2
	returnDocuments := false
	request := dto.RerankRequest{
		Model: "jina-reranker-m0",
		Query: map[string]any{"image": "https://example.test/query.png"},
		Documents: []any{
			map[string]any{"text": "caption"},
			map[string]any{"image": "https://example.test/document.png"},
		},
		TopN:            &topN,
		ReturnDocuments: &returnDocuments,
	}

	converted, err := (&Adaptor{}).ConvertRerankRequest(nil, 0, request)
	require.NoError(t, err)
	payload := converted.(rerankRequest)
	assert.Equal(t, request.Query, payload.Query)
	assert.Equal(t, request.Documents, payload.Documents)
	assert.Equal(t, topN, *payload.TopN)
	assert.False(t, *payload.ReturnDocuments)
}

func TestConvertRerankRequestRejectsImagesForTextModel(t *testing.T) {
	request := dto.RerankRequest{
		Model:     "jina-reranker-v3",
		Query:     map[string]any{"image": "https://example.test/query.png"},
		Documents: []any{"document"},
	}

	_, err := (&Adaptor{}).ConvertRerankRequest(nil, 0, request)
	require.ErrorContains(t, err, "does not support image input")
}

func TestConvertEmbeddingRequestMapsJinaEncodingAndPreservesObjects(t *testing.T) {
	dimensions := 512
	request := dto.EmbeddingRequest{
		Model: "jina-embeddings-v4",
		Input: []any{
			map[string]any{"text": "caption"},
			map[string]any{"image": "https://example.test/image.png"},
		},
		EncodingFormat: "base64",
		Dimensions:     &dimensions,
		User:           "must-not-be-forwarded",
	}

	converted, err := (&Adaptor{}).ConvertEmbeddingRequest(nil, nil, request)
	require.NoError(t, err)
	payload := converted.(embeddingRequest)
	assert.Equal(t, request.Input, payload.Input)
	assert.Equal(t, "base64", payload.EmbeddingType)
	assert.Equal(t, dimensions, *payload.Dimensions)
}

func TestConvertEmbeddingRequestRejectsUnsupportedModelModality(t *testing.T) {
	tests := []struct {
		name    string
		request dto.EmbeddingRequest
		field   string
	}{
		{
			name:    "text model rejects image",
			request: dto.EmbeddingRequest{Model: "jina-embeddings-v5-text-small", Input: map[string]any{"image": "https://example.test/image.png"}},
			field:   "image",
		},
		{
			name:    "v4 rejects audio",
			request: dto.EmbeddingRequest{Model: "jina-embeddings-v4", Input: map[string]any{"audio": "audio-base64"}},
			field:   "audio",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (&Adaptor{}).ConvertEmbeddingRequest(nil, nil, test.request)
			require.Error(t, err)
			assert.ErrorContains(t, err, test.field)
		})
	}
}

func TestConvertEmbeddingRequestAcceptsPublishedMultimodalInputs(t *testing.T) {
	tests := []dto.EmbeddingRequest{
		{Model: "jina-embeddings-v4", Input: map[string]any{"pdf": "https://example.test/document.pdf"}},
		{Model: "jina-embeddings-v5-omni-nano", Input: map[string]any{"audio": "audio-base64"}},
		{Model: "jina-embeddings-v5-omni-small", Input: map[string]any{"video": "https://example.test/video.mp4"}},
	}

	for _, request := range tests {
		t.Run(request.Model, func(t *testing.T) {
			_, err := (&Adaptor{}).ConvertEmbeddingRequest(nil, nil, request)
			require.NoError(t, err)
		})
	}
}

func TestConvertRerankRequestRejectsUnsupportedMediaHiddenBesideText(t *testing.T) {
	request := dto.RerankRequest{
		Model: "jina-reranker-m0",
		Query: "query",
		Documents: []any{
			map[string]any{"text": "caption", "audio": "audio-base64"},
		},
	}

	_, err := (&Adaptor{}).ConvertRerankRequest(nil, 0, request)
	require.ErrorContains(t, err, "audio")
}

func TestJinaEmbeddingHandlerBillsAggregateMultimodalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	body := `{"object":"list","data":[],"model":"jina-embeddings-v5-omni-small","usage":{"total_tokens":100,"prompt_tokens":10,"image_tokens":30,"audio_tokens":20,"video_tokens":40}}`
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}

	usage, apiErr := jinaEmbeddingHandler(ctx, &relaycommon.RelayInfo{}, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 100, usage.PromptTokens)
	assert.Equal(t, 100, usage.TotalTokens)
	assert.Equal(t, 30, usage.PromptTokensDetails.ImageTokens)
	assert.Equal(t, 20, usage.PromptTokensDetails.AudioTokens)
	assert.JSONEq(t, body, recorder.Body.String())
}

func TestJinaEmbeddingHandlerRejectsUntrustedUsage(t *testing.T) {
	tests := []struct {
		name  string
		usage string
	}{
		{name: "negative total", usage: `{"total_tokens":-1,"prompt_tokens":0}`},
		{name: "total over billing bound", usage: `{"total_tokens":` + fmt.Sprint(common.MaxQuota+1) + `,"prompt_tokens":1}`},
		{name: "media exceeds total", usage: `{"total_tokens":10,"prompt_tokens":1,"image_tokens":11}`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"data":[],"usage":` + test.usage + `}`)),
				Header:     make(http.Header),
			}

			usage, apiErr := jinaEmbeddingHandler(ctx, &relaycommon.RelayInfo{}, resp)

			assert.Nil(t, usage)
			require.NotNil(t, apiErr)
			assert.Contains(t, apiErr.Error(), "invalid jina embedding usage")
			assert.Empty(t, recorder.Body.String())
		})
	}
}

func TestJinaRerankResponseCannotSettleNegativeUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{
		RelayMode:   constant.RelayModeRerank,
		ChannelMeta: &relaycommon.ChannelMeta{},
	}
	info.SetEstimatePromptTokens(123)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(`{"results":[],"usage":{"total_tokens":-10}}`)),
		Header:     make(http.Header),
	}

	usage, apiErr := (&Adaptor{}).DoResponse(ctx, resp, info)

	require.Nil(t, apiErr)
	rerankUsage := usage.(*dto.Usage)
	assert.Equal(t, 123, rerankUsage.PromptTokens)
	assert.Equal(t, 123, rerankUsage.TotalTokens)
}

func TestCurrentJinaModelsAreAdvertised(t *testing.T) {
	assert.Contains(t, ModelList, "jina-embeddings-v5-omni-small")
	assert.Contains(t, ModelList, "jina-reranker-v3")
	assert.Contains(t, ModelList, "jina-clip-v2")
}
