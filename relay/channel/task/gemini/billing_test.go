package gemini

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVeoResolutionRatioMatchesCurrentStandardPricing(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		want       float64
	}{
		{model: "veo-3.1-generate-preview", resolution: "720p", want: 1},
		{model: "veo-3.1-generate-preview", resolution: "1080p", want: 1},
		{model: "veo-3.1-generate-preview", resolution: "4K", want: 1.5},
		{model: "veo-3.1-fast-generate-preview", resolution: "720p", want: 1},
		{model: "veo-3.1-fast-generate-preview", resolution: "1080p", want: 1.2},
		{model: "veo-3.1-fast-generate-preview", resolution: "4k", want: 3},
		{model: "veo-3.1-lite-generate-preview", resolution: "720p", want: 1},
		{model: "veo-3.1-lite-generate-preview", resolution: "1080p", want: 1.6},
	}

	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.resolution, func(t *testing.T) {
			assert.Equal(t, tt.want, VeoResolutionRatio(tt.model, tt.resolution))
		})
	}
}

func TestVeoSupports4KTracksModelCapabilities(t *testing.T) {
	tests := []struct {
		model string
		want  bool
	}{
		{model: "veo-3.1-generate-preview", want: true},
		{model: "veo-3.1-fast-generate-preview", want: true},
		{model: "veo-3.1-lite-generate-preview", want: false},
		{model: "veo-3.1-generate-001", want: true},
		{model: "veo-3.1-fast-generate-001", want: false},
		{model: "veo-3.1-lite-generate-001", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			assert.Equal(t, tt.want, VeoSupports4K(tt.model))
		})
	}
}

func TestParseVeoSampleCountAppliesGlobalOutputBound(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		want     int
	}{
		{name: "omitted", metadata: nil, want: 1},
		{name: "maximum", metadata: map[string]any{"sampleCount": float64(MaxVeoSampleCount)}, want: MaxVeoSampleCount},
		{name: "above maximum", metadata: map[string]any{"sampleCount": float64(MaxVeoSampleCount + 1)}, want: 1},
		{name: "duration-sized bypass", metadata: map[string]any{"sampleCount": float64(3600)}, want: 1},
		{name: "fractional", metadata: map[string]any{"sampleCount": 1.5}, want: 1},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, ParseVeoSampleCount(test.metadata))
		})
	}
}
