package reasoning

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseDeepSeekV4ThinkingSuffix(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		wantBase     string
		wantThinking string
		wantEffort   string
	}{
		{name: "disable thinking", model: "deepseek-v4-flash-none", wantBase: "deepseek-v4-flash", wantThinking: "disabled"},
		{name: "low maps to high", model: "deepseek-v4-flash-low", wantBase: "deepseek-v4-flash", wantThinking: "enabled", wantEffort: "high"},
		{name: "medium maps to high", model: "deepseek-v4-pro-medium", wantBase: "deepseek-v4-pro", wantThinking: "enabled", wantEffort: "high"},
		{name: "high stays high", model: "deepseek-v4-pro-high", wantBase: "deepseek-v4-pro", wantThinking: "enabled", wantEffort: "high"},
		{name: "xhigh maps to max", model: "deepseek-v4-flash-xhigh", wantBase: "deepseek-v4-flash", wantThinking: "enabled", wantEffort: "max"},
		{name: "max stays max", model: "deepseek-v4-pro-max", wantBase: "deepseek-v4-pro", wantThinking: "enabled", wantEffort: "max"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base, thinking, effort, ok := ParseDeepSeekV4ThinkingSuffix(test.model)
			require.True(t, ok)
			assert.Equal(t, test.wantBase, base)
			assert.Equal(t, test.wantThinking, thinking)
			assert.Equal(t, test.wantEffort, effort)
		})
	}
}

func TestParseDeepSeekV4ThinkingSuffixRejectsUnknownModels(t *testing.T) {
	for _, model := range []string{
		"deepseek-v4-flash-minimal",
		"deepseek-v4-unknown-max",
		"other-deepseek-v4-flash-max",
	} {
		t.Run(model, func(t *testing.T) {
			base, thinking, effort, ok := ParseDeepSeekV4ThinkingSuffix(model)
			assert.False(t, ok)
			assert.Equal(t, model, base)
			assert.Empty(t, thinking)
			assert.Empty(t, effort)
		})
	}
}

func TestResolveDeepSeekV4Aliases(t *testing.T) {
	tests := []struct {
		model        string
		wantThinking string
	}{
		{model: "deepseek-chat", wantThinking: "disabled"},
		{model: "deepseek-reasoner", wantThinking: "enabled"},
	}

	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			base, thinking, effort, ok := ResolveDeepSeekV4AliasOrSuffix(test.model)
			require.True(t, ok)
			assert.Equal(t, "deepseek-v4-flash", base)
			assert.Equal(t, test.wantThinking, thinking)
			assert.Empty(t, effort)
		})
	}
}

func TestParseClaudeEffortSuffixAcceptsOnlyProviderLevels(t *testing.T) {
	for _, effort := range []string{"max", "xhigh", "high", "medium", "low"} {
		t.Run(effort, func(t *testing.T) {
			assert.True(t, IsClaudeEffortLevel(effort))
			base, gotEffort, ok := ParseClaudeEffortSuffix("claude-opus-5-" + effort)
			require.True(t, ok)
			assert.Equal(t, "claude-opus-5", base)
			assert.Equal(t, effort, gotEffort)
		})
	}

	base, effort, ok := ParseClaudeEffortSuffix("claude-opus-5-minimal")
	assert.False(t, ok)
	assert.Equal(t, "claude-opus-5-minimal", base)
	assert.Empty(t, effort)
	assert.False(t, IsClaudeEffortLevel("minimal"))
}

func TestClaudeCurrentModelCapabilities(t *testing.T) {
	for _, model := range []string{
		"claude-opus-4-7",
		"claude-opus-4-8-high",
		"claude-opus-5-thinking",
		"claude-sonnet-5",
		"claude-fable-5",
		"claude-mythos-5",
	} {
		assert.True(t, IsClaudeAdaptiveThinkingOnlyModel(model), model)
		assert.True(t, IsClaudeSamplingRestrictedModel(model), model)
	}

	assert.False(t, IsClaudeAdaptiveThinkingOnlyModel("claude-opus-4-6"))
	assert.False(t, IsClaudeSamplingRestrictedModel("claude-sonnet-4-6"))
	assert.True(t, IsClaudeAlwaysThinkingModel("claude-fable-5"))
	assert.True(t, IsClaudeAlwaysThinkingModel("claude-mythos-5-high"))
	assert.False(t, IsClaudeAlwaysThinkingModel("claude-opus-5"))
	assert.True(t, IsClaudeOpus5Model("claude-opus-5-xhigh"))
	assert.False(t, IsClaudeOpus5Model("claude-opus-4-8"))
	assert.False(t, IsClaudeAdaptiveThinkingOnlyModel("claude-opus-5-minimal"))
	assert.False(t, IsClaudeOpus5Model("claude-opus-50"))
}
