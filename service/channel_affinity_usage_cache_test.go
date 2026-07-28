package service

import (
	"fmt"
	"math"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP string) *gin.Context {
	rec := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(rec)
	setChannelAffinityContext(ctx, channelAffinityMeta{
		CacheKey:       fmt.Sprintf("test:%s:%s:%s", ruleName, usingGroup, keyFP),
		TTLSeconds:     600,
		RuleName:       ruleName,
		UsingGroup:     usingGroup,
		KeyFingerprint: keyFP,
	})
	return ctx
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_ClaudeMode(t *testing.T) {
	ruleName := fmt.Sprintf("rule_%d", time.Now().UnixNano())
	usingGroup := "default"
	keyFP := fmt.Sprintf("fp_%d", time.Now().UnixNano())
	ctx := buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP)

	usage := &dto.Usage{
		PromptTokens:     100,
		CompletionTokens: 40,
		TotalTokens:      140,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 30,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, usage, types.RelayFormatClaude)
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	require.EqualValues(t, 1, stats.Total)
	require.EqualValues(t, 1, stats.Hit)
	require.EqualValues(t, 100, stats.PromptTokens)
	require.EqualValues(t, 40, stats.CompletionTokens)
	require.EqualValues(t, 140, stats.TotalTokens)
	require.EqualValues(t, 30, stats.CachedTokens)
	require.Equal(t, cacheTokenRateModeCachedOverPromptPlusCached, stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_MixedMode(t *testing.T) {
	ruleName := fmt.Sprintf("rule_%d", time.Now().UnixNano())
	usingGroup := "default"
	keyFP := fmt.Sprintf("fp_%d", time.Now().UnixNano())
	ctx := buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP)

	openAIUsage := &dto.Usage{
		PromptTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 10,
		},
	}
	claudeUsage := &dto.Usage{
		PromptTokens: 80,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 20,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, openAIUsage, types.RelayFormatOpenAI)
	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, claudeUsage, types.RelayFormatClaude)
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	require.EqualValues(t, 2, stats.Total)
	require.EqualValues(t, 2, stats.Hit)
	require.EqualValues(t, 180, stats.PromptTokens)
	require.EqualValues(t, 30, stats.CachedTokens)
	require.Equal(t, cacheTokenRateModeMixed, stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCacheByRelayFormat_UnsupportedModeKeepsEmpty(t *testing.T) {
	ruleName := fmt.Sprintf("rule_%d", time.Now().UnixNano())
	usingGroup := "default"
	keyFP := fmt.Sprintf("fp_%d", time.Now().UnixNano())
	ctx := buildChannelAffinityStatsContextForTest(ruleName, usingGroup, keyFP)

	usage := &dto.Usage{
		PromptTokens: 100,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 25,
		},
	}

	ObserveChannelAffinityUsageCacheByRelayFormat(ctx, usage, types.RelayFormatGemini)
	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)

	require.EqualValues(t, 1, stats.Total)
	require.EqualValues(t, 1, stats.Hit)
	require.EqualValues(t, 25, stats.CachedTokens)
	require.Equal(t, "", stats.CachedTokenRateMode)
}

func TestObserveChannelAffinityUsageCacheSaturatesCounters(t *testing.T) {
	ruleName := fmt.Sprintf("rule_%d", time.Now().UnixNano())
	usingGroup := "default"
	keyFP := fmt.Sprintf("fp_%d", time.Now().UnixNano())
	entryKey := channelAffinityUsageCacheEntryKey(ruleName, usingGroup, keyFP)
	cache := getChannelAffinityUsageCacheStatsCache()

	require.NoError(t, cache.SetWithTTL(entryKey, ChannelAffinityUsageCacheCounters{
		Hit:                  math.MaxInt64 - 1,
		Total:                math.MaxInt64 - 1,
		PromptTokens:         math.MaxInt64 - 1,
		CompletionTokens:     math.MaxInt64 - 1,
		TotalTokens:          math.MaxInt64 - 1,
		CachedTokens:         math.MaxInt64 - 1,
		PromptCacheHitTokens: math.MaxInt64 - 1,
	}, time.Minute))
	t.Cleanup(func() {
		_, _ = cache.DeleteMany([]string{entryKey})
	})

	observeChannelAffinityUsageCache(ChannelAffinityStatsContext{
		RuleName:       ruleName,
		UsingGroup:     usingGroup,
		KeyFingerprint: keyFP,
		TTLSeconds:     60,
	}, &dto.Usage{
		PromptTokens:         math.MaxInt,
		CompletionTokens:     10,
		PromptCacheHitTokens: 10,
		PromptTokensDetails: dto.InputTokenDetails{
			CachedTokens: 10,
		},
	}, cacheTokenRateModeCachedOverPrompt)

	stats := GetChannelAffinityUsageCacheStats(ruleName, usingGroup, keyFP)
	require.Equal(t, int64(math.MaxInt64), stats.Hit)
	require.Equal(t, int64(math.MaxInt64), stats.Total)
	require.Equal(t, int64(math.MaxInt64), stats.PromptTokens)
	require.Equal(t, int64(math.MaxInt64), stats.CompletionTokens)
	require.Equal(t, int64(math.MaxInt64), stats.TotalTokens)
	require.Equal(t, int64(math.MaxInt64), stats.CachedTokens)
	require.Equal(t, int64(math.MaxInt64), stats.PromptCacheHitTokens)
}
