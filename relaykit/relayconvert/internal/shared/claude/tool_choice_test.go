package claude

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapOpenAIToolChoicePreservesExplicitParallelSetting(t *testing.T) {
	parallel := true
	toolChoice := MapOpenAIToolChoice("auto", &parallel)
	require.NotNil(t, toolChoice)
	require.NotNil(t, toolChoice.DisableParallelToolUse)
	assert.False(t, *toolChoice.DisableParallelToolUse)

	parallel = false
	toolChoice = MapOpenAIToolChoice("required", &parallel)
	require.NotNil(t, toolChoice)
	require.NotNil(t, toolChoice.DisableParallelToolUse)
	assert.True(t, *toolChoice.DisableParallelToolUse)

	toolChoice = MapOpenAIToolChoice("auto", nil)
	require.NotNil(t, toolChoice)
	assert.Nil(t, toolChoice.DisableParallelToolUse)
}
