package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/bytedance/gopkg/util/gopool"
)

// notifyLimitStore is used for in-memory rate limiting when Redis is disabled
var (
	notifyLimitStore sync.Map
	notifyLimitMu    sync.Mutex
	cleanupOnce      sync.Once
)

type limitCount struct {
	Count     int
	Timestamp time.Time
}

func getDuration() time.Duration {
	minute := constant.NotificationLimitDurationMinute
	return common.SafeIntervalDuration(
		minute,
		time.Minute,
		10*time.Minute,
		"notification rate limit",
	)
}

// startCleanupTask starts a background task to clean up expired entries
func startCleanupTask() {
	gopool.Go(func() {
		for {
			time.Sleep(time.Hour)
			now := time.Now()
			notifyLimitMu.Lock()
			notifyLimitStore.Range(func(key, value interface{}) bool {
				if limit, ok := value.(limitCount); ok {
					if now.Sub(limit.Timestamp) >= getDuration() {
						notifyLimitStore.Delete(key)
					}
				}
				return true
			})
			notifyLimitMu.Unlock()
		}
	})
}

// CheckNotificationLimit checks if the user has exceeded their notification limit
// Returns true if the user can send notification, false if limit exceeded
func CheckNotificationLimit(userId int, notifyType string) (bool, error) {
	if common.RedisEnabled {
		return checkRedisLimit(userId, notifyType)
	}
	return checkMemoryLimit(userId, notifyType)
}

func checkRedisLimit(userId int, notifyType string) (bool, error) {
	key := fmt.Sprintf("notify_limit:%d:%s:%s", userId, notifyType, time.Now().Format("2006010215"))
	limit := constant.NotifyLimitCount
	duration := getDuration()
	if limit <= 0 || duration <= 0 {
		return false, nil
	}
	if common.RDB == nil {
		return false, errors.New("Redis is enabled but not initialized")
	}

	// The check, increment, and first-write expiry must be one Redis operation.
	// A GET followed by INCR lets concurrent requests all observe the same old
	// count and pass the limit. The script also repairs a legacy counter that
	// was accidentally created without a TTL.
	const incrementWithinLimit = `
local current = tonumber(redis.call('GET', KEYS[1]) or '0')
local limit = tonumber(ARGV[1])
if current >= limit then
  return 0
end
current = redis.call('INCR', KEYS[1])
local ttl = redis.call('PTTL', KEYS[1])
if ttl < 0 then
  redis.call('PEXPIRE', KEYS[1], ARGV[2])
end
if current <= limit then
  return 1
end
return 0`
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	allowed, err := common.RDB.Eval(
		ctx,
		incrementWithinLimit,
		[]string{key},
		limit,
		duration.Milliseconds(),
	).Int64()
	if err != nil {
		return false, fmt.Errorf("failed to update notification count: %w", err)
	}
	return allowed == 1, nil
}

func checkMemoryLimit(userId int, notifyType string) (bool, error) {
	// Ensure cleanup task is started
	cleanupOnce.Do(startCleanupTask)
	return checkMemoryLimitAt(userId, notifyType, time.Now()), nil
}

func checkMemoryLimitAt(userId int, notifyType string, now time.Time) bool {
	key := fmt.Sprintf("%d:%s:%s", userId, notifyType, now.Format("2006010215"))
	limit := constant.NotifyLimitCount
	duration := getDuration()
	if limit <= 0 || duration <= 0 {
		return false
	}

	// Load/update/store is a single critical section. sync.Map only makes each
	// individual operation safe; without this lock concurrent notifications can
	// all read the same count and overwrite one another with the same increment.
	notifyLimitMu.Lock()
	defer notifyLimitMu.Unlock()

	// Get current limit count or initialize new one
	var currentLimit limitCount
	if value, ok := notifyLimitStore.Load(key); ok {
		storedLimit, valid := value.(limitCount)
		if valid {
			currentLimit = storedLimit
		} else {
			notifyLimitStore.Delete(key)
			currentLimit = limitCount{Count: 0, Timestamp: now}
		}
		// Check if the entry has expired. A future timestamp is treated as
		// invalid state so clock skew cannot extend a rate-limit window forever.
		elapsed := now.Sub(currentLimit.Timestamp)
		if elapsed < 0 || elapsed >= duration {
			currentLimit = limitCount{Count: 0, Timestamp: now}
		}
	} else {
		currentLimit = limitCount{Count: 0, Timestamp: now}
	}

	// Increment count
	currentLimit.Count++

	// Check against limits
	// Store updated count
	notifyLimitStore.Store(key, currentLimit)

	return currentLimit.Count <= limit
}
