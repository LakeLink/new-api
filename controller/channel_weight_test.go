package controller

import (
	"testing"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateChannelRejectsUnsafeWeight(t *testing.T) {
	tooLarge := model.MaxChannelWeight + 1
	err := validateChannel(&model.Channel{Weight: &tooLarge}, false)
	require.Error(t, err)
	assert.ErrorContains(t, err, "weight cannot exceed")
}
