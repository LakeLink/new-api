package common

import (
	"math"
	"strconv"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiskCacheByteConversionsDoNotWrap(t *testing.T) {
	originalConfig := GetDiskCacheConfig()
	t.Cleanup(func() {
		SetDiskCacheConfig(originalConfig)
	})

	SetDiskCacheConfig(DiskCacheConfig{
		Enabled:     true,
		ThresholdMB: -1,
		MaxSizeMB:   -1,
	})
	assert.Zero(t, GetDiskCacheThresholdBytes())
	assert.Zero(t, GetDiskCacheMaxSizeBytes())

	if strconv.IntSize == 64 {
		overflowingMB := int((int64(math.MaxInt64) >> 20) + 1)
		SetDiskCacheConfig(DiskCacheConfig{
			Enabled:     true,
			ThresholdMB: overflowingMB,
			MaxSizeMB:   overflowingMB,
		})
		assert.Equal(t, int64(math.MaxInt64), GetDiskCacheThresholdBytes())
		assert.Equal(t, int64(math.MaxInt64), GetDiskCacheMaxSizeBytes())
	}
}

func TestDiskCacheCapacityCheckRejectsAdditionOverflow(t *testing.T) {
	originalConfig := GetDiskCacheConfig()
	originalUsage := atomic.LoadInt64(&diskCacheStats.CurrentDiskUsageBytes)
	t.Cleanup(func() {
		SetDiskCacheConfig(originalConfig)
		atomic.StoreInt64(&diskCacheStats.CurrentDiskUsageBytes, originalUsage)
	})

	var maxSizeMB int64 = math.MaxInt64 >> 20
	if strconv.IntSize == 32 {
		maxSizeMB = math.MaxInt32
	}
	require.Greater(t, maxSizeMB, int64(0))
	SetDiskCacheConfig(DiskCacheConfig{
		Enabled:   true,
		MaxSizeMB: int(maxSizeMB),
	})
	atomic.StoreInt64(&diskCacheStats.CurrentDiskUsageBytes, GetDiskCacheMaxSizeBytes()-1)

	assert.True(t, IsDiskCacheAvailable(1))
	assert.False(t, IsDiskCacheAvailable(2))
	assert.False(t, IsDiskCacheAvailable(-1))
}
