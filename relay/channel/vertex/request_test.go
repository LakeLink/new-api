package vertex

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVertexClaudeRequestMapsLegacyMaxTokensAndPreservesZero(t *testing.T) {
	explicitZero := uint(0)
	converted := copyRequest(&dto.ClaudeRequest{
		MaxTokensToSample: &explicitZero,
	}, anthropicVersion)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Equal(t, float64(0), payload["max_tokens"])
	assert.NotContains(t, payload, "max_tokens_to_sample")
}

func TestGetModelRegionRejectsNonStringMapValues(t *testing.T) {
	assert.Equal(t, "us-central1", GetModelRegion(
		`{"gemini-2.5-pro":"us-central1","default":"global"}`,
		"gemini-2.5-pro",
	))
	assert.Equal(t, "europe-west4", GetModelRegion(
		`{"gemini-2.5-pro":42,"default":"europe-west4"}`,
		"gemini-2.5-pro",
	))
	assert.Equal(t, "global", GetModelRegion(
		`{"gemini-2.5-pro":42,"default":false}`,
		"gemini-2.5-pro",
	))
}
