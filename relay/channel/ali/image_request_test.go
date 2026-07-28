package ali

import (
	"fmt"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOAIImageToAliDefaultsStandardParametersWithoutExtraFields(t *testing.T) {
	info := &relaycommon.RelayInfo{}
	request, err := oaiImage2AliImageRequest(info, dto.ImageRequest{
		Model:  "wanx-v1",
		Prompt: "a lighthouse",
		Size:   "1024x1024",
	}, false)

	require.NoError(t, err)
	require.NotNil(t, request.Parameters.N)
	assert.Equal(t, 1, *request.Parameters.N)
	assert.Equal(t, "1024*1024", request.Parameters.Size)
	assert.Equal(t, float64(1), info.PriceData.OtherRatios()["n"])
}

func TestOAIImageToAliValidatesNativeImageCount(t *testing.T) {
	tests := []struct {
		name string
		n    int
	}{
		{name: "zero", n: 0},
		{name: "negative", n: -1},
		{name: "above maximum", n: dto.MaxImageN + 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var request dto.ImageRequest
			require.NoError(t, common.Unmarshal([]byte(fmt.Sprintf(
				`{"model":"wanx-v1","prompt":"a lighthouse","parameters":{"n":%d}}`,
				test.n,
			)), &request))

			_, err := oaiImage2AliImageRequest(&relaycommon.RelayInfo{}, request, false)

			require.ErrorContains(t, err, "parameters.n must be an integer between 1")
		})
	}
}

func TestOAIImageToAliUsesNativeImageCountForRequestAndBilling(t *testing.T) {
	var request dto.ImageRequest
	require.NoError(t, common.Unmarshal([]byte(
		`{"model":"wanx-v1","prompt":"a lighthouse","n":2,"size":"1024x1024","parameters":{"n":4}}`,
	), &request))
	info := &relaycommon.RelayInfo{}

	converted, err := oaiImage2AliImageRequest(info, request, false)

	require.NoError(t, err)
	require.NotNil(t, converted.Parameters.N)
	assert.Equal(t, 4, *converted.Parameters.N)
	assert.Equal(t, "1024*1024", converted.Parameters.Size)
	assert.Equal(t, float64(4), info.PriceData.OtherRatios()["n"])
}
