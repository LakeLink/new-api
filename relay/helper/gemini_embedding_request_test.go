package helper

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newGeminiEmbeddingContext(t *testing.T, path, body string) *gin.Context {
	t.Helper()
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(body))
	context.Request.Header.Set("Content-Type", "application/json")
	return context
}

func TestGetAndValidateGeminiEmbeddingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	_, err := GetAndValidateGeminiEmbeddingRequest(newGeminiEmbeddingContext(
		t,
		"/v1beta/models/gemini-embedding-2:embedContent",
		`{"content":{"parts":[]}}`,
	))
	require.ErrorContains(t, err, "content.parts is required")

	for _, dimensions := range []int{0, dto.MaxGeminiEmbeddingDimensions + 1} {
		t.Run(fmt.Sprintf("dimensions_%d", dimensions), func(t *testing.T) {
			_, err := GetAndValidateGeminiEmbeddingRequest(newGeminiEmbeddingContext(
				t,
				"/v1beta/models/gemini-embedding-2:embedContent",
				fmt.Sprintf(`{"content":{"parts":[{"text":"hello"}]},"outputDimensionality":%d}`, dimensions),
			))
			require.ErrorContains(t, err, "outputDimensionality")
		})
	}

	request, err := GetAndValidateGeminiEmbeddingRequest(newGeminiEmbeddingContext(
		t,
		"/v1beta/models/gemini-embedding-2:embedContent",
		`{"content":{"parts":[{"text":"hello"}]},"outputDimensionality":10}`,
	))
	require.NoError(t, err)
	require.NotNil(t, request.OutputDimensionality)
	assert.Equal(t, 10, *request.OutputDimensionality)
}

func TestGetAndValidateGeminiBatchEmbeddingRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	_, err := GetAndValidateGeminiBatchEmbeddingRequest(newGeminiEmbeddingContext(
		t,
		"/v1beta/models/gemini-embedding-2:batchEmbedContents",
		`{"requests":[]}`,
	))
	require.ErrorContains(t, err, "requests is required")

	_, err = GetAndValidateGeminiBatchEmbeddingRequest(newGeminiEmbeddingContext(
		t,
		"/v1beta/models/gemini-embedding-2:batchEmbedContents",
		`{"requests":[null]}`,
	))
	require.ErrorContains(t, err, "requests[0] is required")

	request, err := GetAndValidateGeminiBatchEmbeddingRequest(newGeminiEmbeddingContext(
		t,
		"/v1beta/models/gemini-embedding-2:batchEmbedContents",
		`{"requests":[{"content":{"parts":[{"text":"hello"}]},"outputDimensionality":128}]}`,
	))
	require.NoError(t, err)
	require.Len(t, request.Requests, 1)
}

func TestGeminiBatchEmbeddingNilEntryIsDefensive(t *testing.T) {
	request := &dto.GeminiBatchEmbeddingRequest{Requests: []*dto.GeminiEmbeddingRequest{nil}}

	assert.NotPanics(t, func() {
		request.SetModelName("models/gemini-embedding-2")
		_ = request.GetTokenCountMeta()
	})
}
