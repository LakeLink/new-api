package common

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewResponsesUsageInfoFreezesImageToolDefaultsAndOverrides(t *testing.T) {
	tests := []struct {
		name         string
		tools        string
		wantModel    string
		wantQuality  string
		wantSize     string
		wantPartials int
	}{
		{
			name:         "omitted values use OpenAI defaults",
			tools:        `[{"type":"image_generation"}]`,
			wantModel:    "gpt-image-1",
			wantQuality:  "auto",
			wantSize:     "auto",
			wantPartials: 0,
		},
		{
			name:         "explicit zero partials remain zero",
			tools:        `[{"type":"image_generation","partial_images":0}]`,
			wantModel:    "gpt-image-1",
			wantQuality:  "auto",
			wantSize:     "auto",
			wantPartials: 0,
		},
		{
			name:         "outbound overrides are frozen",
			tools:        `[{"type":"image_generation","model":"gpt-image-2","quality":"medium","size":"2048x1024","partial_images":3}]`,
			wantModel:    "gpt-image-2",
			wantQuality:  "medium",
			wantSize:     "2048x1024",
			wantPartials: 3,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			info := NewResponsesUsageInfo(&dto.OpenAIResponsesRequest{Tools: []byte(test.tools)})
			require.NotNil(t, info)
			tool := info.BuiltInTools["image_generation"]
			require.NotNil(t, tool)
			assert.Equal(t, test.wantModel, tool.ImageModel)
			assert.Equal(t, test.wantQuality, tool.ImageQuality)
			assert.Equal(t, test.wantSize, tool.ImageSize)
			assert.Equal(t, test.wantPartials, tool.ImagePartialImages)
			assert.NotNil(t, info.SeenPartialImages)
		})
	}
}
