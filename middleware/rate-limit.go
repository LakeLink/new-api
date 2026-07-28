package middleware

import (
	"fmt"
	"net/http"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

var inMemoryRateLimiter common.InMemoryRateLimiter

var defNext = func(c *gin.Context) {
	c.Next()
}

var slidingWindowRateLimitScript = redis.NewScript(`
local max_requests = tonumber(ARGV[1])
local duration = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local expiration = tonumber(ARGV[4])
local count = redis.call("LLEN", KEYS[1])

if count > 0 then
	local oldest = redis.call("LINDEX", KEYS[1], -1)
	if tonumber(oldest) == nil then
		redis.call("DEL", KEYS[1])
		count = 0
	end
end

if count < max_requests then
	redis.call("LPUSH", KEYS[1], now)
	redis.call("EXPIRE", KEYS[1], expiration)
	return 1
end

local oldest = tonumber(redis.call("LINDEX", KEYS[1], -1))
if now - oldest >= duration then
	redis.call("LPUSH", KEYS[1], now)
	redis.call("LTRIM", KEYS[1], 0, max_requests - 1)
	redis.call("EXPIRE", KEYS[1], expiration)
	return 1
end

redis.call("EXPIRE", KEYS[1], expiration)
return 0
`)

func applyRedisRateLimit(c *gin.Context, maxRequestNum int, duration int64, key string) {
	if maxRequestNum <= 0 || duration <= 0 {
		common.SysError(fmt.Sprintf(
			"invalid rate limit configuration: max_requests=%d duration_seconds=%d",
			maxRequestNum,
			duration,
		))
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}

	expiration := int64(common.RateLimitKeyExpirationDuration / time.Second)
	if duration > expiration {
		expiration = duration
	}
	allowed, err := slidingWindowRateLimitScript.Run(
		c.Request.Context(),
		common.RDB,
		[]string{key},
		maxRequestNum,
		duration,
		time.Now().Unix(),
		expiration,
	).Int64()
	if err != nil {
		common.SysError("Redis rate limit check failed: " + err.Error())
		c.AbortWithStatus(http.StatusInternalServerError)
		return
	}
	if allowed != 1 {
		c.AbortWithStatus(http.StatusTooManyRequests)
	}
}

func redisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	applyRedisRateLimit(c, maxRequestNum, duration, "rateLimit:"+mark+c.ClientIP())
}

func memoryRateLimiter(c *gin.Context, maxRequestNum int, duration int64, mark string) {
	key := mark + c.ClientIP()
	if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
		c.Status(http.StatusTooManyRequests)
		c.Abort()
		return
	}
}

func rateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			redisRateLimiter(c, maxRequestNum, duration, mark)
		}
	} else {
		// It's safe to call multi times.
		inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
		return func(c *gin.Context) {
			memoryRateLimiter(c, maxRequestNum, duration, mark)
		}
	}
}

func GlobalWebRateLimit() func(c *gin.Context) {
	if common.GlobalWebRateLimitEnable {
		return rateLimitFactory(common.GlobalWebRateLimitNum, common.GlobalWebRateLimitDuration, "GW")
	}
	return defNext
}

func GlobalAPIRateLimit() func(c *gin.Context) {
	if common.GlobalApiRateLimitEnable {
		return rateLimitFactory(common.GlobalApiRateLimitNum, common.GlobalApiRateLimitDuration, "GA")
	}
	return defNext
}

func CriticalRateLimit() func(c *gin.Context) {
	if common.CriticalRateLimitEnable {
		return rateLimitFactory(common.CriticalRateLimitNum, common.CriticalRateLimitDuration, "CT")
	}
	return defNext
}

func DownloadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.DownloadRateLimitNum, common.DownloadRateLimitDuration, "DW")
}

func UploadRateLimit() func(c *gin.Context) {
	return rateLimitFactory(common.UploadRateLimitNum, common.UploadRateLimitDuration, "UP")
}

// userRateLimitFactory creates a rate limiter keyed by authenticated user ID
// instead of client IP, making it resistant to proxy rotation attacks.
// Must be used AFTER authentication middleware (UserAuth).
func userRateLimitFactory(maxRequestNum int, duration int64, mark string) func(c *gin.Context) {
	if common.RedisEnabled {
		return func(c *gin.Context) {
			userId := c.GetInt("id")
			if userId == 0 {
				c.Status(http.StatusUnauthorized)
				c.Abort()
				return
			}
			key := fmt.Sprintf("rateLimit:%s:user:%d", mark, userId)
			userRedisRateLimiter(c, maxRequestNum, duration, key)
		}
	}
	// It's safe to call multi times.
	inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
	return func(c *gin.Context) {
		userId := c.GetInt("id")
		if userId == 0 {
			c.Status(http.StatusUnauthorized)
			c.Abort()
			return
		}
		key := fmt.Sprintf("%s:user:%d", mark, userId)
		if !inMemoryRateLimiter.Request(key, maxRequestNum, duration) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}
	}
}

// userRedisRateLimiter is like redisRateLimiter but accepts a pre-built key
// (to support user-ID-based keys).
func userRedisRateLimiter(c *gin.Context, maxRequestNum int, duration int64, key string) {
	applyRedisRateLimit(c, maxRequestNum, duration, key)
}

// SearchRateLimit returns a per-user rate limiter for search endpoints.
// Configurable via SEARCH_RATE_LIMIT_ENABLE / SEARCH_RATE_LIMIT / SEARCH_RATE_LIMIT_DURATION.
func SearchRateLimit() func(c *gin.Context) {
	if !common.SearchRateLimitEnable {
		return defNext
	}
	return userRateLimitFactory(common.SearchRateLimitNum, common.SearchRateLimitDuration, "SR")
}
