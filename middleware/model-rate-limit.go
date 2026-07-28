package middleware

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/common/limiter"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting"

	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
)

const (
	ModelRequestRateLimitCountMark        = "MRRL"
	ModelRequestRateLimitSuccessCountMark = "MRRLS"
)

var reserveSuccessfulRequestScript = redis.NewScript(`
local key_type = redis.call("TYPE", KEYS[1])
if type(key_type) == "table" then
	key_type = key_type["ok"]
end
if key_type ~= "none" and key_type ~= "zset" then
	redis.call("DEL", KEYS[1])
end

local now = tonumber(ARGV[1])
local window = tonumber(ARGV[2])
local max_requests = tonumber(ARGV[3])
redis.call("ZREMRANGEBYSCORE", KEYS[1], "-inf", now - window)
if redis.call("ZCARD", KEYS[1]) >= max_requests then
	redis.call("EXPIRE", KEYS[1], window)
	return 0
end

redis.call("ZADD", KEYS[1], now, ARGV[4])
redis.call("EXPIRE", KEYS[1], window)
return 1
`)

type successfulRequestReservations struct {
	mu      sync.Mutex
	entries map[string]map[string]int64
}

func (r *successfulRequestReservations) reserve(
	key string,
	requestID string,
	maxCount int,
	duration int64,
	now int64,
) bool {
	if maxCount == 0 {
		return true
	}
	if maxCount < 0 || duration <= 0 {
		return false
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.entries == nil {
		r.entries = make(map[string]map[string]int64)
	}
	window := r.entries[key]
	if window == nil {
		window = make(map[string]int64)
		r.entries[key] = window
	}
	for existingID, timestamp := range window {
		if now-timestamp >= duration {
			delete(window, existingID)
		}
	}
	if len(window) >= maxCount {
		return false
	}
	window[requestID] = now
	return true
}

func (r *successfulRequestReservations) release(key string, requestID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	window := r.entries[key]
	delete(window, requestID)
	if len(window) == 0 {
		delete(r.entries, key)
	}
}

var inMemorySuccessfulRequestReservations successfulRequestReservations

func reserveRedisSuccessfulRequest(
	ctx context.Context,
	rdb *redis.Client,
	key string,
	requestID string,
	maxCount int,
	duration int64,
) (bool, error) {
	if maxCount == 0 {
		return true, nil
	}
	if maxCount < 0 || duration <= 0 {
		return false, fmt.Errorf(
			"invalid successful request limit: max=%d duration=%d",
			maxCount,
			duration,
		)
	}
	result, err := reserveSuccessfulRequestScript.Run(
		ctx,
		rdb,
		[]string{key},
		time.Now().Unix(),
		duration,
		maxCount,
		requestID,
	).Int64()
	return result == 1, err
}

func releaseRedisSuccessfulRequest(rdb *redis.Client, key string, requestID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := rdb.ZRem(ctx, key, requestID).Err(); err != nil {
		common.SysError("failed to release unsuccessful request rate-limit reservation: " + err.Error())
	}
}

// Redis限流处理器
func redisRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userId := strconv.Itoa(c.GetInt("id"))
		ctx := c.Request.Context()
		rdb := common.RDB

		// 1. 检查总请求数限制并记录总请求（当 totalMaxCount 为 0 时跳过）。
		if totalMaxCount > 0 {
			totalKey := fmt.Sprintf("rateLimit:%s", userId)
			tb := limiter.New(ctx, rdb)
			allowed, err := tb.Allow(
				ctx,
				totalKey,
				limiter.WithCapacity(int64(totalMaxCount)*duration),
				limiter.WithRate(int64(totalMaxCount)),
				limiter.WithRequested(duration),
			)

			if err != nil {
				common.SysError("failed to check total model request rate limit: " + err.Error())
				abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
				return
			}

			if !allowed {
				abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("您已达到总请求数限制：%d分钟内最多请求%d次，包括失败次数，请检查您的请求是否正确", duration/60, totalMaxCount))
				return
			}
		}

		// 2. Reserve a successful-request slot before dispatch so concurrent
		// in-flight requests cannot all pass the same stale count.
		successKey := fmt.Sprintf("rateLimit:%s:%s", ModelRequestRateLimitSuccessCountMark, userId)
		requestID := common.GetUUID()
		allowed, err := reserveRedisSuccessfulRequest(
			ctx,
			rdb,
			successKey,
			requestID,
			successMaxCount,
			duration,
		)
		if err != nil {
			common.SysError("failed to reserve successful model request rate limit: " + err.Error())
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_check_failed")
			return
		}
		if !allowed {
			abortWithOpenAiMessage(c, http.StatusTooManyRequests, fmt.Sprintf("您已达到请求数限制：%d分钟内最多请求%d次", duration/60, successMaxCount))
			return
		}

		// 3. Process the request. A failure releases its provisional success slot.
		c.Next()
		if c.Writer.Status() >= http.StatusBadRequest && successMaxCount > 0 {
			releaseRedisSuccessfulRequest(rdb, successKey, requestID)
		}
	}
}

// 内存限流处理器
func memoryRateLimitHandler(duration int64, totalMaxCount, successMaxCount int) gin.HandlerFunc {
	durationMinutes := int(duration / 60)
	inMemoryRateLimiter.Init(common.SafeIntervalDuration(durationMinutes, time.Minute, time.Minute, "model request rate limit"))

	return func(c *gin.Context) {
		userId := strconv.Itoa(c.GetInt("id"))
		totalKey := ModelRequestRateLimitCountMark + userId
		successKey := ModelRequestRateLimitSuccessCountMark + userId
		requestID := common.GetUUID()

		// 1. 检查总请求数限制（当totalMaxCount为0时跳过）
		if totalMaxCount > 0 && !inMemoryRateLimiter.Request(totalKey, totalMaxCount, duration) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}

		// 2. Reserve a successful-request slot before dispatch.
		if !inMemorySuccessfulRequestReservations.reserve(
			successKey,
			requestID,
			successMaxCount,
			duration,
			time.Now().Unix(),
		) {
			c.Status(http.StatusTooManyRequests)
			c.Abort()
			return
		}

		// 3. 处理请求
		c.Next()

		// 4. Failed requests do not consume the successful-request budget.
		if c.Writer.Status() >= http.StatusBadRequest && successMaxCount > 0 {
			inMemorySuccessfulRequestReservations.release(successKey, requestID)
		}
	}
}

// ModelRequestRateLimit 模型请求限流中间件
func ModelRequestRateLimit() func(c *gin.Context) {
	return func(c *gin.Context) {
		// 在每个请求时检查是否启用限流
		rateLimitSetting := setting.GetModelRequestRateLimitSetting()
		if !rateLimitSetting.Enabled {
			c.Next()
			return
		}

		// 计算限流参数
		durationMinutes := rateLimitSetting.DurationMinutes
		totalMaxCount := rateLimitSetting.Count
		successMaxCount := rateLimitSetting.SuccessCount
		if durationMinutes <= 0 ||
			int64(durationMinutes) > math.MaxInt64/60 ||
			int64(durationMinutes) > math.MaxInt64/int64(time.Minute) ||
			totalMaxCount < 0 ||
			successMaxCount < 0 {
			common.SysError(fmt.Sprintf(
				"invalid model request rate limit configuration: duration_minutes=%d total=%d success=%d",
				durationMinutes,
				totalMaxCount,
				successMaxCount,
			))
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_configuration_invalid")
			return
		}
		duration := int64(durationMinutes) * 60
		if totalMaxCount > 0 && int64(totalMaxCount) > math.MaxInt64/duration {
			common.SysError("model request rate limit capacity overflows int64")
			abortWithOpenAiMessage(c, http.StatusInternalServerError, "rate_limit_configuration_invalid")
			return
		}

		// 获取分组
		group := common.GetContextKeyString(c, constant.ContextKeyTokenGroup)
		if group == "" {
			group = common.GetContextKeyString(c, constant.ContextKeyUserGroup)
		}

		//获取分组的限流配置
		groupTotalCount, groupSuccessCount, found := setting.GetGroupRateLimit(group)
		if found {
			totalMaxCount = groupTotalCount
			successMaxCount = groupSuccessCount
		}

		// 根据存储类型选择并执行限流处理器
		if common.RedisEnabled {
			redisRateLimitHandler(duration, totalMaxCount, successMaxCount)(c)
		} else {
			memoryRateLimitHandler(duration, totalMaxCount, successMaxCount)(c)
		}
	}
}
