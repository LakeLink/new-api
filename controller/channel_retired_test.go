package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateChannelRejectsRetiredProviderTypes(t *testing.T) {
	tests := []struct {
		name        string
		channelType int
		migration   string
	}{
		{name: "PaLM", channelType: constant.ChannelTypePaLM, migration: "Gemini"},
		{name: "Tencent Hunyuan", channelType: constant.ChannelTypeTencent, migration: "OpenAI-compatible"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateChannel(&model.Channel{Type: tt.channelType, Key: "provider-key"}, true)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "retired")
			assert.Contains(t, err.Error(), tt.migration)
		})
	}
}

func TestValidateChannelHandlesNilInput(t *testing.T) {
	err := validateChannel(nil, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be empty")
}
