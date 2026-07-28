package common

import (
	"fmt"
	"math"
	"time"
)

// SafeIntervalDuration converts an operator-configured positive interval into
// a time.Duration without allowing zero, negative, or multiplication-overflow
// values to turn a background worker into a busy loop.
func SafeIntervalDuration(value int, unit time.Duration, fallback time.Duration, name string) time.Duration {
	if fallback <= 0 {
		fallback = time.Second
	}
	if unit <= 0 || value <= 0 || int64(value) > math.MaxInt64/int64(unit) {
		SysError(fmt.Sprintf("%s interval %d is invalid; using %s", name, value, fallback))
		return fallback
	}
	return time.Duration(value) * unit
}

// SafeOptionalDuration converts a positive integer duration while preserving
// zero and negative values as the caller's "disabled" state. The boolean is
// false for disabled or overflowing values.
func SafeOptionalDuration(value int, unit time.Duration, name string) (time.Duration, bool) {
	return SafeOptionalDuration64(int64(value), unit, name)
}

// SafeOptionalDuration64 is SafeOptionalDuration for protocol and database
// fields represented as int64.
func SafeOptionalDuration64(value int64, unit time.Duration, name string) (time.Duration, bool) {
	if value <= 0 {
		return 0, false
	}
	if unit <= 0 || value > math.MaxInt64/int64(unit) {
		SysError(fmt.Sprintf("%s duration %d is invalid; disabling it", name, value))
		return 0, false
	}
	return time.Duration(value) * unit, true
}

// SafeFloatIntervalDuration is the fractional counterpart to
// SafeIntervalDuration. It preserves sub-unit intervals while rejecting
// non-finite values and conversions that would wrap time.Duration.
func SafeFloatIntervalDuration(value float64, unit time.Duration, fallback time.Duration, name string) time.Duration {
	if fallback <= 0 {
		fallback = time.Second
	}
	scaled := value * float64(unit)
	if unit <= 0 || math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 ||
		math.IsNaN(scaled) || math.IsInf(scaled, 0) || scaled < 1 ||
		scaled >= float64(math.MaxInt64) {
		SysError(fmt.Sprintf("%s interval %v is invalid; using %s", name, value, fallback))
		return fallback
	}
	return time.Duration(scaled)
}
