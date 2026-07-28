package cohere

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCohereRequestDistinguishesOmittedAndExplicitZeroMaxTokens(t *testing.T) {
	explicitZero := uint(0)
	for _, test := range []struct {
		name    string
		request dto.GeneralOpenAIRequest
		want    float64
	}{
		{name: "omitted uses gateway default", request: dto.GeneralOpenAIRequest{}, want: 4000},
		{name: "explicit zero remains zero", request: dto.GeneralOpenAIRequest{MaxTokens: &explicitZero}, want: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			encoded, err := common.Marshal(requestOpenAI2Cohere(test.request))
			require.NoError(t, err)
			var payload map[string]any
			require.NoError(t, common.Unmarshal(encoded, &payload))
			assert.Equal(t, test.want, payload["max_tokens"])
		})
	}
}
