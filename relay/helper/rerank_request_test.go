package helper

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAndValidateRerankRequestAcceptsMultimodalQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", bytes.NewBufferString(
		`{"model":"jina-reranker-m0","query":{"image":"https://example.test/query.png"},"documents":[{"text":"caption"}]}`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")

	request, err := GetAndValidateRerankRequest(ctx)
	require.NoError(t, err)
	require.True(t, request.HasValidQuery())
	assert.Equal(t, "https://example.test/query.png", request.Query.(map[string]any)["image"])
}

func TestGetAndValidateRerankRequestRejectsEmptyMultimodalQuery(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", bytes.NewBufferString(
		`{"model":"jina-reranker-m0","query":{"image":""},"documents":["document"]}`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")

	_, err := GetAndValidateRerankRequest(ctx)
	require.ErrorContains(t, err, "query is empty")
}

func TestGetAndValidateRerankRequestRejectsInvalidOptionalScalars(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		errorText string
	}{
		{
			name:      "zero top n",
			body:      `{"model":"rerank-v4.0-fast","query":"query","documents":["document"],"top_n":0}`,
			errorText: "top_n must be at least 1",
		},
		{
			name:      "zero max chunks",
			body:      `{"model":"rerank-v4.0-fast","query":"query","documents":["document"],"max_chunks_per_doc":0}`,
			errorText: "max_chunks_per_doc must be at least 1",
		},
		{
			name:      "conflicting legacy spelling",
			body:      `{"model":"rerank-v4.0-fast","query":"query","documents":["document"],"max_chunks_per_doc":2,"max_chunk_per_doc":3}`,
			errorText: "conflicts",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", bytes.NewBufferString(test.body))
			ctx.Request.Header.Set("Content-Type", "application/json")

			_, err := GetAndValidateRerankRequest(ctx)
			require.ErrorContains(t, err, test.errorText)
		})
	}
}

func TestGetAndValidateRerankRequestBoundsCohereChunkMultiplier(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(ctx, constant.ContextKeyChannelType, constant.ChannelTypeCohere)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/rerank", bytes.NewBufferString(
		`{"model":"rerank-v4.0-fast","query":"query","documents":["document"],"max_chunks_per_doc":10001}`,
	))
	ctx.Request.Header.Set("Content-Type", "application/json")

	_, err := GetAndValidateRerankRequest(ctx)
	require.ErrorContains(t, err, "must not exceed 10000")
}
