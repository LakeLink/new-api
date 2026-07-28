package vertex

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVertexVeoAudioRatioMatchesCurrentPricing(t *testing.T) {
	tests := []struct {
		model         string
		resolution    string
		generateAudio bool
		want          float64
	}{
		{model: "veo-3.1-generate-001", resolution: "720p", generateAudio: true, want: 1},
		{model: "veo-3.1-generate-001", resolution: "720p", want: 0.5},
		{model: "veo-3.1-generate-001", resolution: "4k", want: 2.0 / 3.0},
		{model: "veo-3.1-fast-generate-001", resolution: "720p", want: 0.8},
		{model: "veo-3.1-fast-generate-001", resolution: "1080p", want: 5.0 / 6.0},
		{model: "veo-3.1-lite-generate-001", resolution: "720p", want: 0.6},
		{model: "veo-3.1-lite-generate-001", resolution: "1080p", want: 0.625},
	}

	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.resolution, func(t *testing.T) {
			assert.InDelta(t, tt.want, vertexVeoAudioRatio(tt.model, tt.resolution, tt.generateAudio), 1e-12)
		})
	}
}
