package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestModelListTracksSupportedCurrentGeminiModels(t *testing.T) {
	for _, model := range []string{
		"gemini-2.0-flash",
		"gemini-2.0-flash-001",
		"gemini-2.0-flash-lite",
		"gemini-2.0-flash-lite-001",
		"gemini-2.5-flash-lite-preview-09-2025",
		"gemini-3-pro-preview",
		"gemini-3.1-flash-lite-preview",
		"gemini-3.1-flash-image-preview",
		"gemini-3-pro-image-preview",
		"gemini-robotics-er-1.5-preview",
		"gemini-embedding-001",
		"gemini-embedding-2-preview",
		"embedding-2-preview",
		"veo-2.0-generate-001",
		"veo-3.0-generate-001",
		"veo-3.0-fast-generate-001",
		// Live models require Google's bidirectional WebSocket protocol, and
		// Deep Research requires the asynchronous Interactions API. This
		// adapter only exposes generateContent/predict/embed endpoints.
		"gemini-2.5-flash-native-audio-latest",
		"gemini-2.5-flash-native-audio-preview-09-2025",
		"gemini-2.5-flash-native-audio-preview-12-2025",
		"gemini-3.1-flash-live-preview",
		"deep-research-pro-preview-12-2025",
	} {
		assert.NotContains(t, ModelList, model)
	}

	assert.Contains(t, ModelList, "gemini-3.1-pro-preview")
	assert.Contains(t, ModelList, "gemini-3.1-pro-preview-customtools")
	for _, model := range []string{
		"gemini-3.6-flash",
		"gemini-3.5-flash",
		"gemini-3.5-flash-lite",
		"gemini-3.1-flash-lite",
		"gemini-3.1-flash-image",
		"gemini-3.1-flash-lite-image",
		"gemini-3-pro-image",
		"gemini-3.1-flash-tts-preview",
		"gemini-embedding-2",
		"gemma-4-31b-it",
		"gemma-4-26b-a4b-it",
		"veo-3.1-lite-generate-preview",
		"gemini-robotics-er-1.6-preview",
	} {
		assert.Contains(t, ModelList, model)
	}
}
