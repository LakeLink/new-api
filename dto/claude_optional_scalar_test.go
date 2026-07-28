package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestClaudeToolChoicePreservesExplicitParallelFalse(t *testing.T) {
	var toolChoice ClaudeToolChoice
	require.NoError(t, common.Unmarshal(
		[]byte(`{"type":"auto","disable_parallel_tool_use":false}`),
		&toolChoice,
	))
	require.NotNil(t, toolChoice.DisableParallelToolUse)
	assert.False(t, *toolChoice.DisableParallelToolUse)

	encoded, err := common.Marshal(toolChoice)
	require.NoError(t, err)
	assert.True(t, gjson.GetBytes(encoded, "disable_parallel_tool_use").Exists())
}
