package baidu

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBaiduRequestPreservesExplicitZeroAndFalseOptions(t *testing.T) {
	zero := 0.0
	zeroTokens := uint(0)
	stream := false
	converted := requestOpenAI2Baidu(dto.GeneralOpenAIRequest{
		TopP:             &zero,
		FrequencyPenalty: &zero,
		MaxTokens:        &zeroTokens,
		Stream:           &stream,
		Messages:         []dto.Message{{Role: "user", Content: "hello"}},
	})

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Equal(t, float64(0), payload["top_p"])
	assert.Equal(t, float64(0), payload["penalty_score"])
	assert.Equal(t, float64(0), payload["max_output_tokens"])
	assert.Equal(t, false, payload["stream"])
}
