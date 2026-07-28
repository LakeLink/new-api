package common

import (
	"math"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConfiguredByteLimitConversionsDoNotWrap(t *testing.T) {
	assert.Zero(t, BytesFromMegabytes(-1))
	assert.Zero(t, BytesFromMegabytes(0))
	assert.Equal(t, int64(3<<20), BytesFromMegabytes(3))
	assert.Zero(t, BytesFromKilobytes(-1))
	assert.Zero(t, BytesFromKilobytes(0))
	assert.Equal(t, int64(3<<10), BytesFromKilobytes(3))
	assert.Zero(t, ReadLimitWithOverrunByte(-1))
	assert.Equal(t, int64(1), ReadLimitWithOverrunByte(0))
	assert.Equal(t, int64(11), ReadLimitWithOverrunByte(10))
	assert.Equal(t, int64(math.MaxInt64), ReadLimitWithOverrunByte(math.MaxInt64))

	if strconv.IntSize == 64 {
		overflowingMB := int(math.MaxInt64/(1<<20) + 1)
		overflowingKB := int(math.MaxInt64/(1<<10) + 1)
		assert.Equal(t, int64(math.MaxInt64), BytesFromMegabytes(overflowingMB))
		assert.Equal(t, int64(math.MaxInt64), BytesFromKilobytes(overflowingKB))
	}
}
