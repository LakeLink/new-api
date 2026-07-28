package common

import (
	"math"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestSafeIntervalDurationRejectsBusyLoopAndOverflowValues(t *testing.T) {
	assert.Equal(t, 15*time.Second, SafeIntervalDuration(15, time.Second, time.Minute, "test"))
	assert.Equal(t, time.Minute, SafeIntervalDuration(0, time.Second, time.Minute, "test"))
	assert.Equal(t, time.Minute, SafeIntervalDuration(-1, time.Second, time.Minute, "test"))

	if strconv.IntSize == 64 {
		maxMinutes := int64(math.MaxInt64 / int64(time.Minute))
		overflowingMinutes := int(maxMinutes + 1)
		assert.Equal(
			t,
			5*time.Minute,
			SafeIntervalDuration(overflowingMinutes, time.Minute, 5*time.Minute, "test"),
		)
	}
}

func TestSafeOptionalDurationPreservesDisabledStateAndRejectsOverflow(t *testing.T) {
	duration, ok := SafeOptionalDuration(15, time.Second, "test")
	assert.True(t, ok)
	assert.Equal(t, 15*time.Second, duration)

	duration, ok = SafeOptionalDuration(0, time.Second, "test")
	assert.False(t, ok)
	assert.Zero(t, duration)

	if strconv.IntSize == 64 {
		overflowingSeconds := int(int64(math.MaxInt64/int64(time.Second)) + 1)
		duration, ok = SafeOptionalDuration(overflowingSeconds, time.Second, "test")
		assert.False(t, ok)
		assert.Zero(t, duration)
	}
}

func TestSafeOptionalDuration64RejectsOverflow(t *testing.T) {
	duration, ok := SafeOptionalDuration64(15, time.Second, "test")
	assert.True(t, ok)
	assert.Equal(t, 15*time.Second, duration)

	duration, ok = SafeOptionalDuration64(math.MaxInt64, time.Second, "test")
	assert.False(t, ok)
	assert.Zero(t, duration)
}

func TestSafeFloatIntervalDurationRejectsNonFiniteAndOverflowValues(t *testing.T) {
	assert.Equal(t, 30*time.Second, SafeFloatIntervalDuration(0.5, time.Minute, time.Minute, "test"))
	assert.Equal(t, time.Minute, SafeFloatIntervalDuration(0, time.Minute, time.Minute, "test"))
	assert.Equal(t, time.Minute, SafeFloatIntervalDuration(math.NaN(), time.Minute, time.Minute, "test"))
	assert.Equal(t, time.Minute, SafeFloatIntervalDuration(math.Inf(1), time.Minute, time.Minute, "test"))
	assert.Equal(t, time.Minute, SafeFloatIntervalDuration(float64(math.MaxInt64), time.Minute, time.Minute, "test"))
	assert.Equal(t, time.Minute, SafeFloatIntervalDuration(0.0000000001, time.Nanosecond, time.Minute, "test"))
}
