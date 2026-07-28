package taskcommon

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalMetadataFiltersModelWithoutMutatingCaller(t *testing.T) {
	metadata := map[string]any{
		"model":    "unpriced-model",
		"duration": 10,
	}
	target := struct {
		Model    string `json:"model"`
		Duration int    `json:"duration"`
	}{}

	require.NoError(t, UnmarshalMetadata(metadata, &target))

	assert.Empty(t, target.Model)
	assert.Equal(t, 10, target.Duration)
	assert.Equal(t, "unpriced-model", metadata["model"])
}
