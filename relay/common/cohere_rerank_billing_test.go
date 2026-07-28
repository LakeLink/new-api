package common

import (
	"math"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEstimateCohereRerankSearchUnitsUsesChunkUpperBound(t *testing.T) {
	oneChunk := 1
	tests := []struct {
		name      string
		documents int
		maxChunks *int
		want      int
	}{
		{name: "one request remains one unit", documents: 1, want: 1},
		{name: "explicit chunks cross unit boundary", documents: 101, maxChunks: &oneChunk, want: 2},
		{name: "omitted max chunks uses v1 default", documents: 100, want: 10},
		{name: "document maximum caps accepted search units", documents: CohereRerankMaxChunks, want: CohereRerankMaxSearchUnits},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := &dto.RerankRequest{
				Documents:       make([]any, test.documents),
				MaxChunksPerDoc: test.maxChunks,
			}
			got, err := EstimateCohereRerankSearchUnits(request)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestEstimateCohereRerankSearchUnitsRejectsUnsafeExplicitProduct(t *testing.T) {
	maxChunks := math.MaxInt
	_, err := EstimateCohereRerankSearchUnits(&dto.RerankRequest{
		Documents:       []any{"document"},
		MaxChunksPerDoc: &maxChunks,
	})
	require.ErrorContains(t, err, "must not exceed")
}

func TestValidateCohereRerankSearchUnitsBoundsProviderMultiplier(t *testing.T) {
	require.NoError(t, ValidateCohereRerankSearchUnits(1))
	require.NoError(t, ValidateCohereRerankSearchUnits(CohereRerankMaxSearchUnits))

	for _, invalid := range []float64{0, -1, 1.5, CohereRerankMaxSearchUnits + 1, math.NaN(), math.Inf(1)} {
		assert.Error(t, ValidateCohereRerankSearchUnits(invalid), "search_units=%v", invalid)
	}
}
