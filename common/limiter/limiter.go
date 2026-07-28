package limiter

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/go-redis/redis/v8"
)

//go:embed lua/rate_limit.lua
var rateLimitScript string

type RedisLimiter struct {
	client *redis.Client
}

var limitScript = redis.NewScript(rateLimitScript)

func New(_ context.Context, r *redis.Client) *RedisLimiter {
	return &RedisLimiter{client: r}
}

func (rl *RedisLimiter) Allow(ctx context.Context, key string, opts ...Option) (bool, error) {
	if rl == nil || rl.client == nil {
		return false, fmt.Errorf("rate limiter Redis client is not initialized")
	}

	// 默认配置
	config := &Config{
		Capacity:  10,
		Rate:      1,
		Requested: 1,
	}

	// 应用选项模式
	for _, opt := range opts {
		opt(config)
	}

	// 执行限流
	// redis.Script.Run uses EVALSHA first and automatically falls back to EVAL
	// after NOSCRIPT. A transient preload failure or Redis script-cache flush
	// therefore cannot permanently break the process-wide limiter.
	result, err := limitScript.Run(
		ctx,
		rl.client,
		[]string{key},
		config.Requested,
		config.Rate,
		config.Capacity,
	).Int()

	if err != nil {
		return false, fmt.Errorf("rate limit failed: %w", err)
	}
	return result == 1, nil
}

// Config 配置选项模式
type Config struct {
	Capacity  int64
	Rate      int64
	Requested int64
}

type Option func(*Config)

func WithCapacity(c int64) Option {
	return func(cfg *Config) { cfg.Capacity = c }
}

func WithRate(r int64) Option {
	return func(cfg *Config) { cfg.Rate = r }
}

func WithRequested(n int64) Option {
	return func(cfg *Config) { cfg.Requested = n }
}
