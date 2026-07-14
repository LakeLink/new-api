package cohere

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestCohereRerankRejectsMultimodalQuery(t *testing.T) {
	_, err := requestConvertRerank2Cohere(dto.RerankRequest{
		Model:     "rerank-v3.5",
		Query:     map[string]any{"image": "https://example.test/query.png"},
		Documents: []any{"document"},
	})
	require.ErrorContains(t, err, "must be a non-empty string")
}
