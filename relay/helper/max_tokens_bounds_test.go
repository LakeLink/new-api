package helper

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestMaxTokensBounds guards the billing invariant that user-supplied max
// token fields are bounded on every relay format. These values feed
// pre-consume quota math (preConsumedTokens * ratio); a huge or
// wrapped-negative value (e.g. 18446744073686646784 parsed into *uint) must
// be rejected at validation instead of corrupting the pre-charge.
func TestMaxTokensBounds(t *testing.T) {
	gin.SetMode(gin.TestMode)

	newJSONContext := func(t *testing.T, body string) *gin.Context {
		c, _ := gin.CreateTestContext(httptest.NewRecorder())
		c.Request = httptest.NewRequest(http.MethodPost, "/relay", bytes.NewBufferString(body))
		c.Request.Header.Set("Content-Type", "application/json")
		return c
	}

	const hugeN = "18446744073686646784"

	t.Run("openai max_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":`+hugeN+`}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_tokens is invalid")
	})

	t.Run("openai max_completion_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_completion_tokens":`+hugeN+`}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_tokens is invalid")
	})

	t.Run("openai combined max tokens and n are bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":`+
			fmt.Sprint(common.MaxTokensLimit)+`,"n":2}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.ErrorContains(t, err, "multiplied by n")
	})

	t.Run("claude max_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":`+hugeN+`}`)
		_, err := GetAndValidateClaudeRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_tokens is invalid")
	})

	t.Run("claude normal max_tokens accepted", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":8192}`)
		req, err := GetAndValidateClaudeRequest(c)
		require.NoError(t, err)
		require.EqualValues(t, 8192, *req.MaxTokens)
	})

	t.Run("claude thinking budget is bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":4096,"thinking":{"type":"enabled","budget_tokens":`+
			fmt.Sprint(common.MaxTokensLimit+1)+`}}`)
		_, err := GetAndValidateClaudeRequest(c)
		require.ErrorContains(t, err, "thinking.budget_tokens")
	})

	t.Run("openai native thinking budget is bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"thinking":{"type":"enabled","budget_tokens":`+
			fmt.Sprint(common.MaxTokensLimit+1)+`}}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.ErrorContains(t, err, "thinking.budget_tokens")
	})

	t.Run("openrouter reasoning max tokens is bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"anthropic/claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"reasoning":{"enabled":true,"max_tokens":`+
			fmt.Sprint(common.MaxTokensLimit+1)+`}}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.ErrorContains(t, err, "reasoning.max_tokens")
	})

	t.Run("gemini maxOutputTokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":`+hugeN+`}}`)
		_, err := GetAndValidateGeminiRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "maxOutputTokens is invalid")
	})

	t.Run("gemini serviceTier is normalized", func(t *testing.T) {
		c := newJSONContext(t, `{"contents":[{"parts":[{"text":"hi"}]}],"serviceTier":"PRIORITY"}`)
		req, err := GetAndValidateGeminiRequest(c)
		require.NoError(t, err)
		require.NotNil(t, req.ServiceTier)
		require.Equal(t, "priority", *req.ServiceTier)
	})

	t.Run("gemini invalid serviceTier is rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"contents":[{"parts":[{"text":"hi"}]}],"serviceTier":"default"}`)
		_, err := GetAndValidateGeminiRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "serviceTier must be one of")
	})

	t.Run("gemini candidateCount is a bounded billing multiplier", func(t *testing.T) {
		c := newJSONContext(t, `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":2,"candidateCount":3}}`)
		req, err := GetAndValidateGeminiRequest(c)
		require.NoError(t, err)
		require.Equal(t, 6, req.GetTokenCountMeta().MaxTokens)
	})

	t.Run("gemini candidateCount zero is rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"candidateCount":0}}`)
		_, err := GetAndValidateGeminiRequest(c)
		require.ErrorContains(t, err, "candidateCount")
	})

	t.Run("gemini candidateCount is bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"candidateCount":129}}`)
		_, err := GetAndValidateGeminiRequest(c)
		require.ErrorContains(t, err, "candidateCount")
	})

	t.Run("gemini combined max output and candidate count are bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"contents":[{"parts":[{"text":"hi"}]}],"generationConfig":{"maxOutputTokens":`+
			fmt.Sprint(common.MaxTokensLimit)+`,"candidateCount":2}}`)
		_, err := GetAndValidateGeminiRequest(c)
		require.ErrorContains(t, err, "multiplied by candidateCount")
	})

	t.Run("responses max_output_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","input":"hi","max_output_tokens":`+hugeN+`}`)
		_, err := GetAndValidateResponsesRequest(c)
		require.Error(t, err)
		require.Contains(t, err.Error(), "max_output_tokens is invalid")
	})

	t.Run("audio max_new_tokens overflow rejected", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"vllm-omni","input":"hi","voice":"alloy","max_new_tokens":`+hugeN+`}`)
		_, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech)
		require.ErrorContains(t, err, "max_new_tokens is invalid")
	})

	t.Run("audio explicit zero max_new_tokens is preserved", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"vllm-omni","input":"hi","voice":"alloy","max_new_tokens":0}`)
		req, err := GetAndValidAudioRequest(c, relayconstant.RelayModeAudioSpeech)
		require.NoError(t, err)
		require.NotNil(t, req.MaxNewTokens)
		require.Zero(t, *req.MaxNewTokens)
	})

	t.Run("responses max_tool_calls is bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","input":"hi","max_tool_calls":1025}`)
		_, err := GetAndValidateResponsesRequest(c)
		require.ErrorContains(t, err, "max_tool_calls")
	})

	t.Run("responses maximum max_tool_calls is accepted", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","input":"hi","max_tool_calls":1024}`)
		req, err := GetAndValidateResponsesRequest(c)
		require.NoError(t, err)
		require.EqualValues(t, 1024, *req.MaxToolCalls)
	})

	t.Run("claude web search max_uses is bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1025}]}`)
		_, err := GetAndValidateClaudeRequest(c)
		require.ErrorContains(t, err, "max_uses")
	})

	t.Run("claude maximum web search max_uses is accepted", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"tools":[{"type":"web_search_20250305","name":"web_search","max_uses":1024}]}`)
		req, err := GetAndValidateClaudeRequest(c)
		require.NoError(t, err)
		require.NotNil(t, req)
	})

	t.Run("claude web search requires the protocol tool name", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"claude-sonnet-4","messages":[{"role":"user","content":"hi"}],"max_tokens":100,"tools":[{"type":"web_search_20250305","name":"not_web_search","max_uses":1}]}`)
		_, err := GetAndValidateClaudeRequest(c)
		require.ErrorContains(t, err, "name must be web_search")
	})

	t.Run("chat completion count must be positive", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"n":0}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "n must be an integer between")
	})

	t.Run("chat completion count is bounded", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"n":129}`)
		_, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.Error(t, err)
		require.Contains(t, err.Error(), "n must be an integer between")
	})

	t.Run("chat completion count multiplies pre-consume output", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":2,"n":3}`)
		req, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
		require.Equal(t, 6, req.GetTokenCountMeta().MaxTokens)
	})

	t.Run("maximum chat completion count is accepted", func(t *testing.T) {
		c := newJSONContext(t, `{"model":"gpt-4o","messages":[{"role":"user","content":"hi"}],"max_tokens":1,"n":128}`)
		req, err := GetAndValidateTextRequest(c, relayconstant.RelayModeChatCompletions)
		require.NoError(t, err)
		require.Equal(t, 128, req.GetTokenCountMeta().MaxTokens)
	})
}
