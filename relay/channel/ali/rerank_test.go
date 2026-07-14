package ali

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/require"
)

func TestAliRerankRejectsMultimodalQuery(t *testing.T) {
	_, err := ConvertRerankRequest(dto.RerankRequest{
		Model:     "gte-rerank-v2",
		Query:     map[string]any{"image": "https://example.test/query.png"},
		Documents: []any{"document"},
	})
	require.ErrorContains(t, err, "must be a non-empty string")
}
