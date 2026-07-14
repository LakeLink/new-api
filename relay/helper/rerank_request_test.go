package helper

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

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
