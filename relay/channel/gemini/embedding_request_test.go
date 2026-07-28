package gemini

import (
	"errors"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertEmbeddingRequestValidatesDimensions(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		dimensions int
		errorText  string
	}{
		{name: "zero", model: "gemini-embedding-2", dimensions: 0, errorText: "between 1 and 3072"},
		{name: "too large", model: "gemini-embedding-2", dimensions: 3073, errorText: "between 1 and 3072"},
		{name: "legacy model too large", model: "text-embedding-004", dimensions: 769, errorText: "between 1 and 768"},
		{name: "unsupported model", model: "embedding-001", dimensions: 128, errorText: "not supported"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: test.model},
			}
			_, err := (&Adaptor{}).ConvertEmbeddingRequest(nil, info, dto.EmbeddingRequest{
				Model:      test.model,
				Input:      "hello",
				Dimensions: &test.dimensions,
			})
			require.ErrorContains(t, err, test.errorText)
			var apiError *types.NewAPIError
			require.True(t, errors.As(err, &apiError))
			assert.Equal(t, http.StatusBadRequest, apiError.StatusCode)
		})
	}
}

func TestConvertEmbeddingRequestForwardsDimensionsToEveryBatchItem(t *testing.T) {
	dimensions := 10
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gemini-embedding-2"},
	}

	converted, err := (&Adaptor{}).ConvertEmbeddingRequest(nil, info, dto.EmbeddingRequest{
		Model:      "gemini-embedding-2",
		Input:      []string{"first", "second"},
		Dimensions: &dimensions,
	})

	require.NoError(t, err)
	assert.True(t, info.IsGeminiBatchEmbedding)
	payload, ok := converted.(map[string]interface{})
	require.True(t, ok)
	requests, ok := payload["requests"].([]map[string]interface{})
	require.True(t, ok)
	require.Len(t, requests, 2)
	assert.Equal(t, dimensions, requests[0]["outputDimensionality"])
	assert.Equal(t, dimensions, requests[1]["outputDimensionality"])
}
