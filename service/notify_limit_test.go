package service

import (
	"math"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func resetNotificationLimitStore() {
	notifyLimitMu.Lock()
	defer notifyLimitMu.Unlock()
	notifyLimitStore.Range(func(key, _ any) bool {
		notifyLimitStore.Delete(key)
		return true
	})
}

func TestMemoryNotificationLimitIsAtomicAcrossConcurrentRequests(t *testing.T) {
	originalLimit := constant.NotifyLimitCount
	originalDuration := constant.NotificationLimitDurationMinute
	t.Cleanup(func() {
		constant.NotifyLimitCount = originalLimit
		constant.NotificationLimitDurationMinute = originalDuration
		resetNotificationLimitStore()
	})
	constant.NotifyLimitCount = 3
	constant.NotificationLimitDurationMinute = 10
	resetNotificationLimitStore()

	const requestCount = 24
	now := time.Date(2026, time.July, 28, 10, 15, 0, 0, time.UTC)
	start := make(chan struct{})
	var ready sync.WaitGroup
	var done sync.WaitGroup
	var allowed atomic.Int64
	ready.Add(requestCount)
	done.Add(requestCount)
	for range requestCount {
		go func() {
			defer done.Done()
			ready.Done()
			<-start
			if checkMemoryLimitAt(42, "email", now) {
				allowed.Add(1)
			}
		}()
	}
	ready.Wait()
	close(start)
	done.Wait()

	assert.EqualValues(t, constant.NotifyLimitCount, allowed.Load())
	value, ok := notifyLimitStore.Load("42:email:2026072810")
	require.True(t, ok)
	assert.Equal(t, requestCount, value.(limitCount).Count)
}

func TestMemoryNotificationLimitResetsAfterConfiguredDuration(t *testing.T) {
	originalLimit := constant.NotifyLimitCount
	originalDuration := constant.NotificationLimitDurationMinute
	t.Cleanup(func() {
		constant.NotifyLimitCount = originalLimit
		constant.NotificationLimitDurationMinute = originalDuration
		resetNotificationLimitStore()
	})
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 10
	resetNotificationLimitStore()

	now := time.Date(2026, time.July, 28, 10, 15, 0, 0, time.UTC)
	assert.True(t, checkMemoryLimitAt(7, "webhook", now))
	assert.False(t, checkMemoryLimitAt(7, "webhook", now.Add(time.Minute)))
	assert.True(t, checkMemoryLimitAt(7, "webhook", now.Add(10*time.Minute)))
}

func TestNotificationLimitDurationRejectsOverflow(t *testing.T) {
	originalDuration := constant.NotificationLimitDurationMinute
	t.Cleanup(func() {
		constant.NotificationLimitDurationMinute = originalDuration
	})

	constant.NotificationLimitDurationMinute = -1
	assert.Equal(t, 10*time.Minute, getDuration())

	if strconv.IntSize == 64 {
		constant.NotificationLimitDurationMinute = int(math.MaxInt64/int64(time.Minute) + 1)
		assert.Equal(t, 10*time.Minute, getDuration())
	}
}

func TestMemoryNotificationLimitRepairsInvalidCachedState(t *testing.T) {
	originalLimit := constant.NotifyLimitCount
	originalDuration := constant.NotificationLimitDurationMinute
	t.Cleanup(func() {
		constant.NotifyLimitCount = originalLimit
		constant.NotificationLimitDurationMinute = originalDuration
		resetNotificationLimitStore()
	})
	constant.NotifyLimitCount = 1
	constant.NotificationLimitDurationMinute = 10
	resetNotificationLimitStore()

	now := time.Date(2026, time.July, 28, 10, 15, 0, 0, time.UTC)
	key := "8:webhook:2026072810"
	notifyLimitStore.Store(key, "invalid")

	require.NotPanics(t, func() {
		assert.True(t, checkMemoryLimitAt(8, "webhook", now))
	})
	value, ok := notifyLimitStore.Load(key)
	require.True(t, ok)
	assert.Equal(t, limitCount{Count: 1, Timestamp: now}, value)
}
