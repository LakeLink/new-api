package service

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/pkg/billingexpr"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

type invalidUsageBillingStub struct {
	preConsumed int
}

func (b *invalidUsageBillingStub) Settle(int) error         { return nil }
func (b *invalidUsageBillingStub) Refund(*gin.Context)      {}
func (b *invalidUsageBillingStub) NeedsRefund() bool        { return false }
func (b *invalidUsageBillingStub) GetPreConsumedQuota() int { return b.preConsumed }
func (b *invalidUsageBillingStub) Reserve(target int) error { b.preConsumed = target; return nil }

func TestEffectiveBillingUsageRejectsInvalidTopLevelCounters(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	tests := []struct {
		name        string
		usage       *dto.Usage
		errorDetail string
	}{
		{
			name:        "negative completion",
			usage:       &dto.Usage{PromptTokens: 10, CompletionTokens: -1},
			errorDetail: "completion_tokens cannot be negative",
		},
		{
			name: "negative cache detail",
			usage: &dto.Usage{
				PromptTokens:        10,
				PromptTokensDetails: dto.InputTokenDetails{CachedTokens: -1},
			},
			errorDetail: "cached_tokens cannot be negative",
		},
		{
			name:        "integer overflow payload",
			usage:       &dto.Usage{PromptTokens: maxInt, CompletionTokens: 1},
			errorDetail: "prompt_tokens exceeds limit",
		},
		{
			name: "aggregate exceeds limit",
			usage: &dto.Usage{
				PromptTokens:     common.MaxTokensLimit/2 + 1,
				CompletionTokens: common.MaxTokensLimit/2 + 1,
			},
			errorDetail: "prompt_tokens+completion_tokens exceeds limit",
		},
		{
			name: "aliases make canonical aggregate exceed limit",
			usage: &dto.Usage{
				PromptTokens: common.MaxTokensLimit,
				OutputTokens: common.MaxTokensLimit,
			},
			errorDetail: "effective_usage.total_tokens exceeds limit",
		},
		{
			name: "cache detail exceeds authoritative input",
			usage: &dto.Usage{
				PromptTokens: 1,
				InputTokensDetails: &dto.InputTokenDetails{
					CachedTokens: common.MaxTokensLimit,
				},
			},
			errorDetail: "cached_tokens exceeds authoritative input total 1",
		},
		{
			name: "reasoning detail exceeds authoritative output",
			usage: &dto.Usage{
				CompletionTokens: 1,
				CompletionTokenDetails: dto.OutputTokenDetails{
					ReasoningTokens: common.MaxTokensLimit,
				},
			},
			errorDetail: "reasoning_tokens exceeds authoritative output total 1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effective, err := effectiveBillingUsage(test.usage)

			require.ErrorContains(t, err, test.errorDetail)
			require.Nil(t, effective)
		})
	}
}

func TestEffectiveBillingUsageRejectsInvalidProviderMetadata(t *testing.T) {
	tests := []struct {
		name        string
		usage       *dto.Usage
		errorDetail string
	}{
		{
			name: "negative Claude cache counter",
			usage: &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceClaudeMessages,
				Semantic: dto.BillingUsageSemanticAnthropic,
				ClaudeUsage: &dto.ClaudeUsage{
					InputTokens:          10,
					CacheReadInputTokens: -1,
				},
			}},
			errorDetail: "cache_read_input_tokens cannot be negative",
		},
		{
			name: "Gemini derived prompt overflow",
			usage: &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceGeminiChat,
				Semantic: dto.BillingUsageSemanticGemini,
				GeminiUsageMetadata: &dto.GeminiUsageMetadata{
					PromptTokenCount:        common.MaxTokensLimit/2 + 1,
					ToolUsePromptTokenCount: common.MaxTokensLimit/2 + 1,
				},
			}},
			errorDetail: "prompt_tokens exceeds limit",
		},
		{
			name: "Gemini negative modality detail",
			usage: &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceGeminiChat,
				Semantic: dto.BillingUsageSemanticGemini,
				GeminiUsageMetadata: &dto.GeminiUsageMetadata{
					PromptTokenCount: 1,
					PromptTokensDetails: []dto.GeminiPromptTokensDetails{
						{Modality: "AUDIO", TokenCount: -1},
					},
				},
			}},
			errorDetail: "promptTokensDetails[0].tokenCount cannot be negative",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effective, err := effectiveBillingUsage(test.usage)

			require.ErrorContains(t, err, test.errorDetail)
			require.Nil(t, effective)
		})
	}
}

func TestEffectiveBillingUsageAcceptsClaudeCacheDetailsWithinTotalInput(t *testing.T) {
	usage := &dto.Usage{BillingUsage: &dto.BillingUsage{
		Source:   dto.BillingUsageSourceClaudeMessages,
		Semantic: dto.BillingUsageSemanticAnthropic,
		ClaudeUsage: &dto.ClaudeUsage{
			InputTokens:          1,
			OutputTokens:         2,
			CacheReadInputTokens: 100,
		},
	}}

	effective, err := effectiveBillingUsage(usage)

	require.NoError(t, err)
	require.Equal(t, 1, effective.PromptTokens)
	require.Equal(t, 101, effective.InputTokens)
	require.Equal(t, 100, effective.PromptTokensDetails.CachedTokens)
}

func TestEffectiveBillingUsageValidatesProviderBreakdownTotals(t *testing.T) {
	tests := []struct {
		name        string
		usage       *dto.Usage
		errorDetail string
	}{
		{
			name: "Gemini input modalities are additive",
			usage: &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceGeminiChat,
				Semantic: dto.BillingUsageSemanticGemini,
				GeminiUsageMetadata: &dto.GeminiUsageMetadata{
					PromptTokenCount: 100,
					PromptTokensDetails: []dto.GeminiPromptTokensDetails{
						{Modality: "AUDIO", TokenCount: 100},
						{Modality: "IMAGE", TokenCount: 100},
					},
				},
			}},
			errorDetail: "Gemini input modality breakdown exceeds authoritative total 100: 200",
		},
		{
			name: "Gemini output modalities and reasoning are additive",
			usage: &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceGeminiChat,
				Semantic: dto.BillingUsageSemanticGemini,
				GeminiUsageMetadata: &dto.GeminiUsageMetadata{
					PromptTokenCount:     1,
					CandidatesTokenCount: 100,
					ThoughtsTokenCount:   50,
					CandidatesTokensDetails: []dto.GeminiPromptTokensDetails{
						{Modality: "TEXT", TokenCount: 100},
						{Modality: "AUDIO", TokenCount: 50},
					},
				},
			}},
			errorDetail: "Gemini output modality breakdown exceeds authoritative total 150: 200",
		},
		{
			name: "Gemini cached modalities are additive",
			usage: &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceGeminiChat,
				Semantic: dto.BillingUsageSemanticGemini,
				GeminiUsageMetadata: &dto.GeminiUsageMetadata{
					PromptTokenCount:        200,
					CachedContentTokenCount: 100,
					CacheTokensDetails: []dto.GeminiPromptTokensDetails{
						{Modality: "AUDIO", TokenCount: 100},
						{Modality: "IMAGE", TokenCount: 100},
					},
				},
			}},
			errorDetail: "Gemini cached input modality breakdown exceeds authoritative total 100: 200",
		},
		{
			name: "Anthropic cache creation split is bounded by aggregate",
			usage: &dto.Usage{BillingUsage: &dto.BillingUsage{
				Source:   dto.BillingUsageSourceClaudeMessages,
				Semantic: dto.BillingUsageSemanticAnthropic,
				ClaudeUsage: &dto.ClaudeUsage{
					InputTokens:                 10,
					CacheCreationInputTokens:    100,
					ClaudeCacheCreation5mTokens: 100,
					ClaudeCacheCreation1hTokens: 100,
				},
			}},
			errorDetail: "Anthropic cache creation split breakdown exceeds authoritative total 100: 200",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effective, err := effectiveBillingUsage(test.usage)

			require.ErrorContains(t, err, test.errorDetail)
			require.Nil(t, effective)
		})
	}
}

func TestEffectiveBillingUsageNormalizesLegacyClaudeCacheSplit(t *testing.T) {
	usage := &dto.Usage{BillingUsage: &dto.BillingUsage{
		Source:   dto.BillingUsageSourceClaudeMessages,
		Semantic: dto.BillingUsageSemanticAnthropic,
		ClaudeUsage: &dto.ClaudeUsage{
			InputTokens:                 10,
			CacheReadInputTokens:        5,
			ClaudeCacheCreation5mTokens: 30,
			ClaudeCacheCreation1hTokens: 20,
		},
	}}

	effective, err := effectiveBillingUsage(usage)

	require.NoError(t, err)
	require.Equal(t, 65, effective.InputTokens)
	require.Equal(t, 50, effective.PromptTokensDetails.CachedCreationTokens)
	require.Equal(t, 30, effective.ClaudeCacheCreation5mTokens)
	require.Equal(t, 20, effective.ClaudeCacheCreation1hTokens)
}

func TestEffectiveBillingUsageDoesNotInventGeminiTextForImageOnlyInput(t *testing.T) {
	usage := &dto.Usage{BillingUsage: &dto.BillingUsage{
		Source:   dto.BillingUsageSourceGeminiChat,
		Semantic: dto.BillingUsageSemanticGemini,
		GeminiUsageMetadata: &dto.GeminiUsageMetadata{
			PromptTokenCount: 100,
			PromptTokensDetails: []dto.GeminiPromptTokensDetails{
				{Modality: "IMAGE", TokenCount: 100},
			},
		},
	}}

	effective, err := effectiveBillingUsage(usage)

	require.NoError(t, err)
	require.Zero(t, effective.PromptTokensDetails.TextTokens)
	require.Equal(t, 100, effective.PromptTokensDetails.ImageTokens)
}

func TestEffectiveBillingUsageNormalizesTopLevelTokenAliases(t *testing.T) {
	tests := []struct {
		name             string
		usage            *dto.Usage
		wantPromptTokens int
		wantOutputTokens int
	}{
		{name: "input tokens", usage: &dto.Usage{InputTokens: 17}, wantPromptTokens: 17},
		{name: "output tokens", usage: &dto.Usage{OutputTokens: 9}, wantOutputTokens: 9},
		{
			name: "both aliases",
			usage: &dto.Usage{
				InputTokens:  17,
				OutputTokens: 9,
				InputTokensDetails: &dto.InputTokenDetails{
					CachedTokens:     5,
					CacheWriteTokens: 3,
				},
			},
			wantPromptTokens: 17,
			wantOutputTokens: 9,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effective, err := effectiveBillingUsage(test.usage)

			require.NoError(t, err)
			require.Equal(t, test.wantPromptTokens, effective.PromptTokens)
			require.Equal(t, test.wantOutputTokens, effective.CompletionTokens)
			require.Equal(t, test.wantPromptTokens+test.wantOutputTokens, effective.TotalTokens)
			require.True(t, hasPositiveBillingUsage(effective))
			if test.usage.InputTokensDetails != nil {
				require.Equal(t, test.usage.InputTokensDetails.CachedTokens, effective.PromptTokensDetails.CachedTokens)
				require.Equal(t, test.usage.InputTokensDetails.CacheWriteTokens, effective.PromptTokensDetails.CacheWriteTokens)
			}
			require.Zero(t, test.usage.PromptTokens, "normalization must not mutate the response DTO")
			require.Zero(t, test.usage.CompletionTokens, "normalization must not mutate the response DTO")
		})
	}
}

func TestDetailOnlyUsageIsRejected(t *testing.T) {
	tests := []struct {
		name  string
		usage *dto.Usage
	}{
		{name: "cache only", usage: &dto.Usage{PromptTokensDetails: dto.InputTokenDetails{CachedTokens: 100}}},
		{name: "input detail only", usage: &dto.Usage{PromptTokensDetails: dto.InputTokenDetails{TextTokens: 100}}},
		{name: "output detail only", usage: &dto.Usage{CompletionTokenDetails: dto.OutputTokenDetails{ReasoningTokens: 100}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			effective, err := effectiveBillingUsage(test.usage)

			require.ErrorContains(t, err, "exceeds authoritative")
			require.Nil(t, effective)
		})
	}
}

func TestTextToolCallCounterBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("claude_web_search_requests", common.MaxTextToolCallCount)
	maxUses := uint(common.MaxTextToolCallCount)
	info := &relaycommon.RelayInfo{Request: &dto.ClaudeRequest{Tools: []any{
		dto.ClaudeWebSearchTool{Type: "web_search_20250305", Name: "web_search", MaxUses: &maxUses},
	}}}

	require.NoError(t, validateTextToolCallCounters(ctx, info))
	ctx.Set("claude_web_search_requests", common.MaxTextToolCallCount+1)
	require.ErrorContains(t, validateTextToolCallCounters(ctx, info), "exceeds limit")

	ctx.Set("claude_web_search_requests", 0)
	maxToolCalls := uint(1)
	info = &relaycommon.RelayInfo{
		Request: &dto.OpenAIResponsesRequest{MaxToolCalls: &maxToolCalls},
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
			dto.BuildInToolWebSearchPreview: {CallCount: 2},
		}},
	}
	require.ErrorContains(t, validateTextToolCallCounters(ctx, info), "effective max_tool_calls")

	clientMaxToolCalls := uint(10)
	outboundMaxToolCalls := uint(1)
	info.Request = &dto.OpenAIResponsesRequest{MaxToolCalls: &clientMaxToolCalls}
	info.ResponsesUsageInfo.MaxToolCalls = &outboundMaxToolCalls
	require.ErrorContains(t, validateTextToolCallCounters(ctx, info), "effective max_tool_calls 1")
}

func TestInvalidUsageSettlementQuotaNeverRefundsPreConsume(t *testing.T) {
	tests := []struct {
		name          string
		finalReserved int
		session       *invalidUsageBillingStub
		estimated     int
		tiered        int
		toolReserved  int
		finalEstimate bool
		want          int
	}{
		{name: "legacy reservation", finalReserved: 700, want: 700},
		{name: "billing session reservation", finalReserved: 700, session: &invalidUsageBillingStub{preConsumed: 900}, want: 900},
		{name: "larger recorded reservation", finalReserved: 1_100, session: &invalidUsageBillingStub{preConsumed: 900}, want: 1_100},
		{name: "trust bypass keeps estimated pre-consume", session: &invalidUsageBillingStub{}, estimated: 650, want: 650},
		{name: "tiered estimate is preserved", tiered: 999, want: 999},
		{name: "server tool reservation is excluded from legacy reservation", finalReserved: 900, estimated: 650, toolReserved: 300, want: 650},
		{name: "final request estimate wins over a larger retry reservation", finalReserved: 700, session: &invalidUsageBillingStub{preConsumed: 900}, estimated: 500, toolReserved: 300, finalEstimate: true, want: 500},
		{name: "final free request does not inherit a retry reservation", finalReserved: 700, session: &invalidUsageBillingStub{preConsumed: 900}, toolReserved: 300, finalEstimate: true, want: 0},
		{name: "server tool reservation subtraction is bounded", finalReserved: 200, session: &invalidUsageBillingStub{preConsumed: 100}, toolReserved: 300, want: 0},
		{name: "oversized internal reservation is saturated", estimated: common.MaxQuota + 1, want: common.MaxQuota},
		{name: "negative corrupted reservation", finalReserved: -1, session: &invalidUsageBillingStub{preConsumed: -2}, want: 0},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			relayInfo := &relaycommon.RelayInfo{
				FinalPreConsumedQuota:         test.finalReserved,
				MaxServerToolReservationQuota: test.toolReserved,
				FinalRequestEstimateReady:     test.finalEstimate,
				PriceData:                     types.PriceData{QuotaToPreConsume: test.estimated},
			}
			if test.session != nil {
				relayInfo.Billing = test.session
			}
			if test.tiered > 0 {
				relayInfo.TieredBillingSnapshot = &billingexpr.BillingSnapshot{EstimatedQuotaAfterGroup: test.tiered}
			}

			require.Equal(t, test.want, invalidUsageSettlementQuota(relayInfo))
		})
	}
}

func TestInvalidUsageSettlementAuditsReservationSaturation(t *testing.T) {
	info := &relaycommon.RelayInfo{PriceData: types.PriceData{QuotaToPreConsume: common.MaxQuota + 1}}

	require.Equal(t, common.MaxQuota, invalidUsageSettlementQuota(info))
	require.NotNil(t, info.QuotaClamp)
	require.Equal(t, common.QuotaClampOverflow, info.QuotaClamp.Kind)
}

func TestInvalidUsageSettlementAddsVerifiedToolUsageWithoutDuplicatingRequestFee(t *testing.T) {
	info := &relaycommon.RelayInfo{
		FinalPreConsumedQuota: 100,
		PriceData: types.PriceData{
			GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	summary := textQuotaSummary{
		ToolCallSurchargeQuota:  decimal.NewFromInt(25),
		ProviderRequestFeeQuota: decimal.NewFromInt(20),
	}

	require.Equal(t, 125, invalidUsageSettlementWithVerifiedCharges(info, summary, true))
	require.Equal(t, 100, invalidUsageSettlementWithVerifiedCharges(info, summary, false))

	info.FinalPreConsumedQuota = 0
	summary.ToolCallSurchargeQuota = decimal.Zero
	require.Equal(t, 20, invalidUsageSettlementWithVerifiedCharges(info, summary, true))
}

func TestInvalidUsageSettlementReplacesReservedToolsWithVerifiedUsage(t *testing.T) {
	info := &relaycommon.RelayInfo{
		FinalPreConsumedQuota:         150,
		MaxServerToolReservationQuota: 50,
		PriceData: types.PriceData{
			QuotaToPreConsume: 100,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	summary := textQuotaSummary{ToolCallSurchargeQuota: decimal.NewFromInt(20)}

	require.Equal(t, 100, invalidUsageSettlementQuota(info))
	require.Equal(t, 120, invalidUsageSettlementWithVerifiedCharges(info, summary, true))
}

func TestPostTextConsumeQuotaInvalidUsageKeepsPreConsumedQuota(t *testing.T) {
	truncate(t)
	const (
		userID        = 91_001
		channelID     = 91_002
		reservedQuota = 777
	)
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "invalid_usage_test")
	relayInfo := &relaycommon.RelayInfo{
		RequestId:             "invalid-usage-request",
		UserId:                userID,
		OriginModelName:       "invalid-usage-model",
		UsingGroup:            "default",
		StartTime:             time.Now(),
		FinalPreConsumedQuota: reservedQuota,
		ChannelMeta:           &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	relayInfo.SetEstimatePromptTokens(25)
	maxInt := int(^uint(0) >> 1)

	PostTextConsumeQuota(ctx, relayInfo, &dto.Usage{
		PromptTokens:     maxInt,
		CompletionTokens: 1,
	}, nil)

	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	require.Equal(t, reservedQuota, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)

	var channel model.Channel
	require.NoError(t, model.DB.First(&channel, channelID).Error)
	require.Equal(t, int64(reservedQuota), channel.UsedQuota)

	var consumeLog model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ?", userID).Order("id DESC").First(&consumeLog).Error)
	require.Equal(t, reservedQuota, consumeLog.Quota)
	require.Contains(t, consumeLog.Content, "上游返回无效计费信息")
	var other map[string]interface{}
	require.NoError(t, common.Unmarshal([]byte(consumeLog.Other), &other))
	adminInfo, ok := other["admin_info"].(map[string]interface{})
	require.True(t, ok)
	invalidUsage, ok := adminInfo["invalid_billing_usage"].(map[string]interface{})
	require.True(t, ok)
	require.Equal(t, float64(reservedQuota), invalidUsage["settled_quota"])
}

func TestPostTextConsumeQuotaInvalidTokensAddValidatedToolCharge(t *testing.T) {
	truncate(t)
	const (
		userID        = 91_006
		channelID     = 91_007
		reservedQuota = 777
		toolQuota     = 5_000
	)
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "invalid_token_valid_tool_test")
	ctx.Set("claude_web_search_requests", 1)
	relayInfo := &relaycommon.RelayInfo{
		RequestId:             "invalid-token-valid-tool-request",
		UserId:                userID,
		OriginModelName:       "claude-sonnet-4-6",
		UsingGroup:            "default",
		StartTime:             time.Now(),
		FinalPreConsumedQuota: reservedQuota,
		ChannelMeta:           &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
		Billing: &invalidUsageBillingStub{preConsumed: reservedQuota},
	}
	maxInt := int(^uint(0) >> 1)

	PostTextConsumeQuota(ctx, relayInfo, &dto.Usage{PromptTokens: maxInt, CompletionTokens: 1}, nil)

	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	require.Equal(t, reservedQuota+toolQuota, user.UsedQuota)
	var consumeLog model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ?", userID).Order("id DESC").First(&consumeLog).Error)
	require.Equal(t, reservedQuota+toolQuota, consumeLog.Quota)
	require.Contains(t, consumeLog.Content, "Claude Web Search")
}

func TestPostTextConsumeQuotaRejectsOversizedToolCounter(t *testing.T) {
	truncate(t)
	const (
		userID        = 91_011
		channelID     = 91_012
		reservedQuota = 777
	)
	seedUser(t, userID, 10_000)
	seedChannel(t, channelID)

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "invalid_tool_usage_test")
	ctx.Set("claude_web_search_requests", common.MaxTextToolCallCount+1)
	relayInfo := &relaycommon.RelayInfo{
		UserId:                userID,
		OriginModelName:       "claude-sonnet-4-6",
		UsingGroup:            "default",
		StartTime:             time.Now(),
		FinalPreConsumedQuota: reservedQuota,
		ChannelMeta:           &relaycommon.ChannelMeta{ChannelId: channelID},
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: 1},
		},
	}

	PostTextConsumeQuota(ctx, relayInfo, &dto.Usage{PromptTokens: 10, TotalTokens: 10}, nil)

	var user model.User
	require.NoError(t, model.DB.First(&user, userID).Error)
	require.Equal(t, reservedQuota, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.Nil(t, relayInfo.QuotaClamp, "rejected tool counters must not reach quota conversion")

	var consumeLog model.Log
	require.NoError(t, model.LOG_DB.Where("user_id = ?", userID).Order("id DESC").First(&consumeLog).Error)
	require.Equal(t, reservedQuota, consumeLog.Quota)
	require.Contains(t, consumeLog.Content, "上游返回无效计费信息")
	require.NotContains(t, consumeLog.Content, "Claude Web Search")
}

func TestPostTextConsumeQuotaUnusableUsageSettlesRealBillingSessionFailClosed(t *testing.T) {
	tests := []struct {
		name  string
		usage *dto.Usage
	}{
		{name: "missing usage", usage: nil},
		{name: "all-zero usage", usage: &dto.Usage{}},
		{name: "total-only usage", usage: &dto.Usage{TotalTokens: 100}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupDurableBillingSessionTest(t)
			require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
			oldBatchEnabled := common.BatchUpdateEnabled
			common.BatchUpdateEnabled = false
			t.Cleanup(func() { common.BatchUpdateEnabled = oldBatchEnabled })

			user, token := createDurableBillingBalances(t, db, "unusable-"+test.name)
			channel := model.Channel{Name: "unusable-" + test.name, Status: common.ChannelStatusEnabled}
			require.NoError(t, db.Create(&channel).Error)
			info := &relaycommon.RelayInfo{
				RequestId:             "unusable-" + test.name,
				UserId:                user.Id,
				TokenId:               token.Id,
				TokenKey:              token.Key,
				OriginModelName:       "unusable-usage-model",
				UsingGroup:            "default",
				FinalPreConsumedQuota: 100,
				ChannelMeta:           &relaycommon.ChannelMeta{ChannelId: channel.Id},
				PriceData:             types.PriceData{ModelRatio: 1, CompletionRatio: 1, GroupRatioInfo: types.GroupRatioInfo{GroupRatio: 1}},
			}
			info.SetEstimatePromptTokens(25)
			session := &BillingSession{
				relayInfo:        info,
				funding:          &WalletFunding{userId: user.Id, consumed: 100},
				preConsumedQuota: 100,
				tokenConsumed:    100,
			}
			info.Billing = session

			gin.SetMode(gin.TestMode)
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Set("username", "unusable_usage_test")
			PostTextConsumeQuota(ctx, info, test.usage, nil)

			require.NoError(t, db.First(&user, user.Id).Error)
			require.NoError(t, db.First(&token, token.Id).Error)
			require.NoError(t, db.First(&channel, channel.Id).Error)
			require.Equal(t, 900, user.Quota)
			require.Equal(t, 100, user.UsedQuota)
			require.Equal(t, 1, user.RequestCount)
			require.Equal(t, 400, token.RemainQuota)
			require.Equal(t, 100, token.UsedQuota)
			require.Equal(t, int64(100), channel.UsedQuota)
		})
	}
}

func TestPostTextConsumeQuotaUnusableUsageChargesTrustedEstimate(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	require.NoError(t, db.AutoMigrate(&model.Log{}, &model.Channel{}))
	oldBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	t.Cleanup(func() { common.BatchUpdateEnabled = oldBatchEnabled })

	user, token := createDurableBillingBalances(t, db, "trusted-unusable")
	require.NoError(t, db.Model(&user).Updates(map[string]interface{}{"quota": 1_000, "used_quota": 0, "request_count": 0}).Error)
	require.NoError(t, db.Model(&token).Updates(map[string]interface{}{"remain_quota": 500, "used_quota": 0}).Error)
	channel := model.Channel{Name: "trusted-unusable", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	info := &relaycommon.RelayInfo{
		RequestId:       "trusted-unusable-request",
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		OriginModelName: "trusted-unusable-model",
		UsingGroup:      "default",
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id},
		PriceData: types.PriceData{
			ModelRatio: 1, CompletionRatio: 1,
			QuotaToPreConsume: 125,
			GroupRatioInfo:    types.GroupRatioInfo{GroupRatio: 1},
		},
	}
	info.SetEstimatePromptTokens(25)
	session := &BillingSession{
		relayInfo: info,
		funding:   &WalletFunding{userId: user.Id},
	}
	info.Billing = session

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "trusted_unusable_test")
	PostTextConsumeQuota(ctx, info, nil, nil)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	require.Equal(t, 875, user.Quota)
	require.Equal(t, 125, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.Equal(t, 375, token.RemainQuota)
	require.Equal(t, 125, token.UsedQuota)
	require.Equal(t, int64(125), channel.UsedQuota)
}

func TestPostTextConsumeQuotaInvalidBillingFactorKeepsReservedCharge(t *testing.T) {
	db := setupDurableBillingSessionTest(t)
	user, token := createDurableBillingBalances(t, db, "invalid-billing-factor")
	channel := model.Channel{Name: "invalid-billing-factor", Status: common.ChannelStatusEnabled}
	require.NoError(t, db.Create(&channel).Error)
	info := &relaycommon.RelayInfo{
		RequestId:       "invalid-billing-factor-request",
		UserId:          user.Id,
		TokenId:         token.Id,
		TokenKey:        token.Key,
		OriginModelName: "invalid-billing-factor-model",
		UsingGroup:      "default",
		StartTime:       time.Now(),
		ChannelMeta:     &relaycommon.ChannelMeta{ChannelId: channel.Id},
		PriceData: types.PriceData{
			ModelRatio:      1,
			CompletionRatio: 1,
			GroupRatioInfo:  types.GroupRatioInfo{GroupRatio: -1},
		},
	}
	info.Billing = &BillingSession{
		relayInfo:        info,
		funding:          &WalletFunding{userId: user.Id, consumed: 100},
		preConsumedQuota: 100,
		tokenConsumed:    100,
	}
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("username", "invalid_billing_factor_test")

	PostTextConsumeQuota(ctx, info, &dto.Usage{PromptTokens: 10, TotalTokens: 10}, nil)

	require.NoError(t, db.First(&user, user.Id).Error)
	require.NoError(t, db.First(&token, token.Id).Error)
	require.NoError(t, db.First(&channel, channel.Id).Error)
	require.Equal(t, 900, user.Quota)
	require.Equal(t, 100, user.UsedQuota)
	require.Equal(t, 1, user.RequestCount)
	require.Equal(t, 400, token.RemainQuota)
	require.Equal(t, 100, token.UsedQuota)
	require.Equal(t, int64(100), channel.UsedQuota)
	var consumeLog model.Log
	require.NoError(t, db.Where("user_id = ?", user.Id).First(&consumeLog).Error)
	require.Equal(t, 100, consumeLog.Quota)
	require.Contains(t, consumeLog.Content, "按预扣额度结算")
}
