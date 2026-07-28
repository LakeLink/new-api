package middleware

import (
	"fmt"
	"net/http"

	"github.com/QuantumNous/new-api/common"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	EmailVerificationRateLimitMark = "EV"
	EmailVerificationMaxRequests   = 2  // 30秒内最多2次
	EmailVerificationDuration      = 30 // 30秒时间窗口
)

var emailVerificationRateLimitScript = redis.NewScript(`
local count = redis.call("INCR", KEYS[1])
local ttl = redis.call("TTL", KEYS[1])
if count == 1 or ttl < 0 then
	redis.call("EXPIRE", KEYS[1], ARGV[1])
end
return count
`)

func redisEmailVerificationRateLimiter(c *gin.Context) {
	rdb := common.RDB
	key := "emailVerification:" + EmailVerificationRateLimitMark + ":" + c.ClientIP()

	count, err := emailVerificationRateLimitScript.Run(
		c.Request.Context(),
		rdb,
		[]string{key},
		EmailVerificationDuration,
	).Int64()
	if err != nil {
		// fallback
		memoryEmailVerificationRateLimiter(c)
		return
	}

	// 检查是否超出限制
	if count <= int64(EmailVerificationMaxRequests) {
		c.Next()
		return
	}

	// 获取剩余等待时间
	ttl, err := rdb.TTL(c.Request.Context(), key).Result()
	waitSeconds := int64(EmailVerificationDuration)
	if err == nil && ttl > 0 {
		waitSeconds = int64(ttl.Seconds())
	}

	c.JSON(http.StatusTooManyRequests, gin.H{
		"success": false,
		"message": fmt.Sprintf("发送过于频繁，请等待 %d 秒后再试", waitSeconds),
	})
	c.Abort()
}

func memoryEmailVerificationRateLimiter(c *gin.Context) {
	key := EmailVerificationRateLimitMark + ":" + c.ClientIP()

	if !inMemoryRateLimiter.Request(key, EmailVerificationMaxRequests, EmailVerificationDuration) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success": false,
			"message": "发送过于频繁，请稍后再试",
		})
		c.Abort()
		return
	}

	c.Next()
}

func EmailVerificationRateLimit() gin.HandlerFunc {
	return func(c *gin.Context) {
		if common.RedisEnabled {
			redisEmailVerificationRateLimiter(c)
		} else {
			inMemoryRateLimiter.Init(common.RateLimitKeyExpirationDuration)
			memoryEmailVerificationRateLimiter(c)
		}
	}
}
