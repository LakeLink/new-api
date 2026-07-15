package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestChannelWeightClampsLegacyOutOfRangeValues(t *testing.T) {
	tooLarge := MaxChannelWeight + 1
	channel := Channel{Weight: &tooLarge}

	assert.Equal(t, int(MaxChannelWeight), channel.GetWeight())
}
