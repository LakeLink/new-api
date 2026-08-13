package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenAIToolChoiceToConfigModes(t *testing.T) {
	for _, test := range []struct {
		openAI string
		gemini dto.FunctionCallingConfigMode
	}{
		{openAI: "auto", gemini: "AUTO"},
		{openAI: "required", gemini: "ANY"},
		{openAI: "none", gemini: "NONE"},
	} {
		t.Run(test.openAI, func(t *testing.T) {
			config, err := OpenAIToolChoiceToConfig(test.openAI)
			require.NoError(t, err)
			require.NotNil(t, config)
			require.NotNil(t, config.FunctionCallingConfig)
			assert.Equal(t, test.gemini, config.FunctionCallingConfig.Mode)
		})
	}
}

func TestOpenAIToolChoiceToConfigForcedFunction(t *testing.T) {
	config, err := OpenAIToolChoiceToConfig(map[string]interface{}{
		"type": "function",
		"function": map[string]interface{}{
			"name": "lookup",
		},
	})

	require.NoError(t, err)
	require.NotNil(t, config)
	require.NotNil(t, config.FunctionCallingConfig)
	assert.Equal(t, dto.FunctionCallingConfigMode("ANY"), config.FunctionCallingConfig.Mode)
	assert.Equal(t, []string{"lookup"}, config.FunctionCallingConfig.AllowedFunctionNames)
}

func TestOpenAIToolChoiceToConfigAllowedTools(t *testing.T) {
	config, err := OpenAIToolChoiceToConfig(map[string]interface{}{
		"type": "allowed_tools",
		"mode": "auto",
		"tools": []interface{}{
			map[string]interface{}{"type": "function", "name": "lookup"},
			map[string]interface{}{"type": "function", "name": "search"},
		},
	})

	require.NoError(t, err)
	require.NotNil(t, config)
	require.NotNil(t, config.FunctionCallingConfig)
	assert.Equal(t, dto.FunctionCallingConfigMode("VALIDATED"), config.FunctionCallingConfig.Mode)
	assert.Equal(t, []string{"lookup", "search"}, config.FunctionCallingConfig.AllowedFunctionNames)
}

func TestOpenAIToolChoiceToConfigRejectsInvalidChoice(t *testing.T) {
	_, err := OpenAIToolChoiceToConfig("sometimes")
	require.ErrorContains(t, err, "unsupported")

	_, err = OpenAIToolChoiceToConfig(map[string]interface{}{
		"type": "function",
	})
	require.ErrorContains(t, err, "requires a name")
}
