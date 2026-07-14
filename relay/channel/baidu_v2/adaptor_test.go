package baidu_v2

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaiduV2ConvertersServeAdvertisedJSONRoutes(t *testing.T) {
	adaptor := &Adaptor{}
	embedding := dto.EmbeddingRequest{
		Model: "embedding-v1",
		Input: []any{map[string]any{"text": "caption", "image": "https://example.test/image.png"}},
	}
	convertedEmbedding, err := adaptor.ConvertEmbeddingRequest(nil, nil, embedding)
	require.NoError(t, err)
	assert.Equal(t, embedding, convertedEmbedding)

	rerank := dto.RerankRequest{Model: "bce-reranker-base", Query: "weather", Documents: []any{"Shanghai", "Beijing"}}
	convertedRerank, err := adaptor.ConvertRerankRequest(nil, relayconstant.RelayModeRerank, rerank)
	require.NoError(t, err)
	assert.Equal(t, rerank, convertedRerank)

	n := uint(2)
	info := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
	convertedImage, err := adaptor.ConvertImageRequest(nil, info, dto.ImageRequest{
		Model:          "flux.1-schnell",
		Prompt:         "a lake",
		N:              &n,
		Size:           "1024x1024",
		ResponseFormat: "url",
		User:           []byte(`"user-123"`),
		Extra: map[string]json.RawMessage{
			"negative_prompt": json.RawMessage(`"low quality"`),
			"steps":           json.RawMessage(`12`),
		},
	})
	require.NoError(t, err)
	payload, ok := convertedImage.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "flux.1-schnell", payload["model"])
	assert.Equal(t, float64(2), payload["n"])
	assert.Equal(t, "user-123", payload["user"])
	assert.Equal(t, "low quality", payload["negative_prompt"])
	assert.Equal(t, float64(12), payload["steps"])
}

func TestBaiduV2ImageConversionEnforcesProviderContract(t *testing.T) {
	adaptor := &Adaptor{}
	generationInfo := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesGenerations}
	n := uint(2)
	_, err := adaptor.ConvertImageRequest(nil, generationInfo, dto.ImageRequest{Model: "qwen-image", Prompt: "cat", N: &n})
	require.ErrorContains(t, err, "only supports n=1")

	n = 5
	_, err = adaptor.ConvertImageRequest(nil, generationInfo, dto.ImageRequest{Model: "flux.1-schnell", Prompt: "cat", N: &n})
	require.ErrorContains(t, err, "between 1 and 4")

	_, err = adaptor.ConvertImageRequest(nil, generationInfo, dto.ImageRequest{Model: "flux.1-schnell", Prompt: "cat", ResponseFormat: "b64_json"})
	require.ErrorContains(t, err, "use url")

	editInfo := &relaycommon.RelayInfo{RelayMode: relayconstant.RelayModeImagesEdits}
	converted, err := adaptor.ConvertImageRequest(nil, editInfo, dto.ImageRequest{
		Model:  "qwen-image-edit",
		Prompt: "make it blue",
		Image:  []byte(`"https://example.test/input.png"`),
	})
	require.NoError(t, err)
	payload := converted.(map[string]any)
	assert.Equal(t, "https://example.test/input.png", payload["image"])

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	ctx.Request.Header.Set("Content-Type", "multipart/form-data; boundary=test")
	_, err = adaptor.ConvertImageRequest(ctx, editInfo, dto.ImageRequest{Model: "qwen-image-edit", Prompt: "edit"})
	require.ErrorContains(t, err, "JSON image URL")
}

func TestBaiduV2ChannelIdentity(t *testing.T) {
	assert.Equal(t, "baidu_v2", (&Adaptor{}).GetChannelName())
}

func TestBaiduV2RerankRejectsMultimodalQuery(t *testing.T) {
	_, err := (&Adaptor{}).ConvertRerankRequest(nil, relayconstant.RelayModeRerank, dto.RerankRequest{
		Model:     "bce-reranker-base",
		Query:     map[string]any{"image": "https://example.test/query.png"},
		Documents: []any{"document"},
	})
	require.ErrorContains(t, err, "must be a non-empty string")
}
