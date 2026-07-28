package cohere

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
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

func TestCohereRerankConversionPreservesOptionalScalarSemantics(t *testing.T) {
	converted, err := requestConvertRerank2Cohere(dto.RerankRequest{
		Model:     "rerank-v4.0-fast",
		Query:     "query",
		Documents: []any{"one", "two"},
	})
	require.NoError(t, err)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var omitted map[string]any
	require.NoError(t, common.Unmarshal(encoded, &omitted))
	assert.NotContains(t, omitted, "top_n")
	assert.NotContains(t, omitted, "return_documents")
	assert.NotContains(t, omitted, "max_chunks_per_doc")

	topN := 2
	returnDocuments := false
	legacyMaxChunks := 3
	converted, err = requestConvertRerank2Cohere(dto.RerankRequest{
		Model:           "rerank-v4.0-fast",
		Query:           "query",
		Documents:       []any{"one", "two"},
		TopN:            &topN,
		ReturnDocuments: &returnDocuments,
		MaxChunkPerDoc:  &legacyMaxChunks,
	})
	require.NoError(t, err)
	encoded, err = common.Marshal(converted)
	require.NoError(t, err)
	var explicit map[string]any
	require.NoError(t, common.Unmarshal(encoded, &explicit))
	assert.Equal(t, float64(2), explicit["top_n"])
	assert.Equal(t, false, explicit["return_documents"])
	assert.Equal(t, float64(3), explicit["max_chunks_per_doc"])
	assert.NotContains(t, explicit, "max_chunk_per_doc")
}

func TestCohereRerankConversionRejectsInvalidOptionalBounds(t *testing.T) {
	zero := 0
	_, err := requestConvertRerank2Cohere(dto.RerankRequest{
		Model:     "rerank-v4.0-fast",
		Query:     "query",
		Documents: []any{"document"},
		TopN:      &zero,
	})
	require.ErrorContains(t, err, "top_n must be at least 1")

	hugeMaxChunks := relaycommon.CohereRerankMaxChunks + 1
	_, err = requestConvertRerank2Cohere(dto.RerankRequest{
		Model:           "rerank-v4.0-fast",
		Query:           "query",
		Documents:       []any{"document"},
		MaxChunksPerDoc: &hugeMaxChunks,
	})
	require.ErrorContains(t, err, "must not exceed")
}

func TestCohereRerankHandlerUsesProviderBilledSearchUnits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	info := &relaycommon.RelayInfo{
		PriceData: types.PriceData{UsePrice: true},
		RerankerInfo: &relaycommon.RerankerInfo{
			Documents: []any{"one", "two"},
		},
	}
	info.SetEstimatePromptTokens(11)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body: io.NopCloser(strings.NewReader(`{
			"results":[{"index":0,"relevance_score":0.9}],
			"meta":{"billed_units":{"search_units":2}}
		}`)),
	}

	usage, apiErr := cohereRerankHandler(ctx, resp, info)
	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.Equal(t, 11, usage.PromptTokens)
	require.NotNil(t, info.CohereSearchUnits)
	assert.Equal(t, float64(2), *info.CohereSearchUnits)
	assert.Equal(t, float64(2), info.PriceData.OtherRatios()[relaycommon.CohereRerankSearchUnitsRatioKey])
}

func TestCohereRerankHandlerRejectsUntrustedSearchUnits(t *testing.T) {
	tests := []struct {
		name        string
		billedUnits string
		errorText   string
	}{
		{name: "missing", billedUnits: `{}`, errorText: "missing billed_units.search_units"},
		{name: "zero", billedUnits: `{"search_units":0}`, errorText: "positive finite"},
		{name: "fractional", billedUnits: `{"search_units":1.5}`, errorText: "whole number"},
		{name: "over provider maximum", billedUnits: `{"search_units":101}`, errorText: "exceed"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{
				PriceData:    types.PriceData{UsePrice: true},
				RerankerInfo: &relaycommon.RerankerInfo{},
			}
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"results":[],"meta":{"billed_units":` + test.billedUnits + `}}`)),
			}

			_, apiErr := cohereRerankHandler(ctx, resp, info)
			require.NotNil(t, apiErr)
			assert.Contains(t, apiErr.Error(), test.errorText)
			assert.Nil(t, info.CohereSearchUnits)
		})
	}
}
