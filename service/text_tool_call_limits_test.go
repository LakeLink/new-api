package service

import (
	"math"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateTextQuotaBillingFactorsRejectsNegativeAndNonFiniteValues(t *testing.T) {
	base := textQuotaSummary{
		CompletionRatio:      1,
		CacheRatio:           1,
		ImageRatio:           1,
		ModelRatio:           1,
		GroupRatio:           1,
		ModelPrice:           1,
		CacheCreationRatio:   1,
		CacheCreationRatio5m: 1,
		CacheCreationRatio1h: 1,
	}
	tests := []struct {
		name  string
		value float64
	}{
		{name: "negative", value: -1},
		{name: "nan", value: math.NaN()},
		{name: "positive infinity", value: math.Inf(1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			summary := base
			summary.GroupRatio = test.value
			require.ErrorContains(t, validateTextQuotaBillingFactors(summary), "group_ratio")
		})
	}
}

func TestValidateTextToolCallCountersUsesEffectiveConvertedClaudeLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	maxUses := uint(1)
	info := &relaycommon.RelayInfo{
		Request:                         &dto.GeneralOpenAIRequest{Model: "gpt-test"},
		EffectiveClaudeWebSearchMaxUses: &maxUses,
	}

	ctx.Set("claude_web_search_requests", 1)
	require.NoError(t, validateTextToolCallCounters(ctx, info))

	ctx.Set("claude_web_search_requests", 2)
	require.ErrorContains(t, validateTextToolCallCounters(ctx, info), "effective outbound max_uses 1")
}

func TestResponsesWebSearchUsesConfiguredCurrentOrPreviewPriceFamily(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		toolType      string
		expectedPrice float64
	}{
		{toolType: dto.BuildInToolWebSearch, expectedPrice: 10},
		{toolType: dto.BuildInToolWebSearch20250826, expectedPrice: 10},
		{toolType: dto.BuildInToolWebSearchPreview, expectedPrice: 25},
		{toolType: dto.BuildInToolWebSearchPreview20250311, expectedPrice: 25},
	}

	for _, test := range tests {
		t.Run(test.toolType, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			info := &relaycommon.RelayInfo{ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
				BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
					test.toolType: {ToolName: test.toolType, CallCount: 2},
				},
			}}
			summary := textQuotaSummary{ModelName: "gpt-4o", GroupRatio: 1}

			surcharge := calculateTextToolCallSurcharge(ctx, info, &summary)

			assert.Equal(t, 2, summary.WebSearchCallCount)
			assert.Equal(t, test.expectedPrice, summary.WebSearchPrice)
			assert.False(t, surcharge.IsZero())
		})
	}
}

func TestValidateTextToolCallCountersRejectsAmbiguousResponsesWebSearchTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	info := &relaycommon.RelayInfo{ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{
		BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
			dto.BuildInToolWebSearch:        {ToolName: dto.BuildInToolWebSearch},
			dto.BuildInToolWebSearchPreview: {ToolName: dto.BuildInToolWebSearchPreview},
		},
	}}

	err := validateTextToolCallCounters(ctx, info)

	require.ErrorContains(t, err, "ambiguous")
}

func TestValidateTextBillingUsagePresenceAllowsZeroTokenFixedPrice(t *testing.T) {
	fixedPriceInfo := &relaycommon.RelayInfo{PriceData: types.PriceData{
		UsePrice:   true,
		ModelPrice: 0.12,
	}}

	require.NoError(t, validateTextBillingUsagePresence(fixedPriceInfo, &dto.Usage{}, false))
	require.NoError(t, validateTextBillingUsagePresence(fixedPriceInfo, nil, false))

	tokenPriceInfo := &relaycommon.RelayInfo{PriceData: types.PriceData{ModelRatio: 1}}
	require.ErrorContains(t, validateTextBillingUsagePresence(tokenPriceInfo, &dto.Usage{}, false), "no positive counters")
}
