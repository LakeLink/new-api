package common

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAudioDurationHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := GetAudioDuration(ctx, bytes.NewReader(nil), ".aac")
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestOpusDurationRejectsWrappedGranulePosition(t *testing.T) {
	page := make([]byte, 27)
	copy(page, "OggS")
	binary.LittleEndian.PutUint64(page[6:14], math.MaxUint64)

	_, err := getOpusDuration(bytes.NewReader(page))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "out of range")
}
