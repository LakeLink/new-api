package ratio_setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestXAILongContextModels(t *testing.T) {
	for _, model := range []string{
		"grok-4.5",
		"grok-4.3-latest",
		"grok-4.20-0309-reasoning",
		"grok-latest",
		"grok-build-0.1",
		"grok-code-fast-1",
	} {
		assert.True(t, IsXAILongContextModel(model), model)
	}

	assert.False(t, IsXAILongContextModel("grok-imagine-image"))
}
