package common

import "math"

// BytesFromMegabytes converts a positive MiB limit to bytes without allowing
// an oversized configuration value to wrap into a small or negative limit.
func BytesFromMegabytes(value int) int64 {
	return bytesFromUnits(value, 1<<20)
}

// BytesFromKilobytes converts a positive KiB limit to bytes without allowing
// an oversized configuration value to wrap into a small or negative limit.
func BytesFromKilobytes(value int) int64 {
	return bytesFromUnits(value, 1<<10)
}

// ReadLimitWithOverrunByte returns the number of bytes a bounded reader should
// accept when it needs one extra byte to distinguish an exact-limit body from
// an oversized body.
func ReadLimitWithOverrunByte(limit int64) int64 {
	if limit < 0 {
		return 0
	}
	if limit == math.MaxInt64 {
		return math.MaxInt64
	}
	return limit + 1
}

func bytesFromUnits(value int, unit int64) int64 {
	if value <= 0 {
		return 0
	}
	if int64(value) > math.MaxInt64/unit {
		return math.MaxInt64
	}
	return int64(value) * unit
}
