package jina

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
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
	require.ErrorContains(t, err, "non-empty string")
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

func TestCurrentJinaModelsAreAdvertised(t *testing.T) {
	assert.Contains(t, ModelList, "jina-embeddings-v5-omni-small")
	assert.Contains(t, ModelList, "jina-reranker-v3")
	assert.Contains(t, ModelList, "jina-clip-v2")
}
