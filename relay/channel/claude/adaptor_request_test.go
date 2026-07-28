package claude

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertClaudeRequestNormalizesLegacyMaxTokens(t *testing.T) {
	legacyZero := uint(0)
	request := &dto.ClaudeRequest{MaxTokensToSample: &legacyZero}

	convertedValue, err := (&Adaptor{}).ConvertClaudeRequest(nil, nil, request)

	require.NoError(t, err)
	converted, ok := convertedValue.(*dto.ClaudeRequest)
	require.True(t, ok)
	require.False(t, request == converted)
	require.NotNil(t, converted.MaxTokens)
	assert.Zero(t, *converted.MaxTokens)
	assert.Nil(t, converted.MaxTokensToSample)
	require.NotNil(t, request.MaxTokensToSample, "conversion must not mutate the parsed client request")
}

func TestConvertClaudeRequestCurrentMaxTokensWins(t *testing.T) {
	current := uint(1)
	legacy := uint(2)

	convertedValue, err := (&Adaptor{}).ConvertClaudeRequest(nil, nil, &dto.ClaudeRequest{
		MaxTokens:         &current,
		MaxTokensToSample: &legacy,
	})

	require.NoError(t, err)
	converted := convertedValue.(*dto.ClaudeRequest)
	require.Same(t, &current, converted.MaxTokens)
	assert.Nil(t, converted.MaxTokensToSample)
}
