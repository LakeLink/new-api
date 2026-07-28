package model_setting

import (
	"math"
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestClaudeSettingsWriteHeadersMergesConfiguredValuesIntoSingleHeader(t *testing.T) {
	settings := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-7-sonnet-20250219-thinking": {
				"anthropic-beta": {
					"token-efficient-tools-2025-02-19",
				},
			},
		},
	}

	headers := http.Header{}
	headers.Set("anthropic-beta", "output-128k-2025-02-19")

	settings.WriteHeaders("claude-3-7-sonnet-20250219-thinking", &headers)

	got := headers.Values("anthropic-beta")
	if len(got) != 1 {
		t.Fatalf("expected a single merged header value, got %v", got)
	}
	expected := "output-128k-2025-02-19,token-efficient-tools-2025-02-19"
	if got[0] != expected {
		t.Fatalf("expected merged header %q, got %q", expected, got[0])
	}
}

func TestClaudeThinkingBudgetTokensStayWithinProviderBounds(t *testing.T) {
	tests := []struct {
		name       string
		percentage float64
		maxTokens  uint
		want       int
	}{
		{name: "default ratio reaches provider minimum", percentage: 0.8, maxTokens: 1280, want: 1024},
		{name: "small ratio is raised to provider minimum", percentage: 0.1, maxTokens: 1280, want: 1024},
		{name: "one leaves room for response", percentage: 1, maxTokens: 1280, want: 1279},
		{name: "NaN falls back safely", percentage: math.NaN(), maxTokens: 1280, want: 1024},
		{name: "infinity falls back safely", percentage: math.Inf(1), maxTokens: 1280, want: 1024},
		{name: "oversized ratio falls back safely", percentage: 2, maxTokens: 1280, want: 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			settings := &ClaudeSettings{ThinkingAdapterBudgetTokensPercentage: tt.percentage}
			assert.Equal(t, tt.want, settings.GetThinkingBudgetTokens(tt.maxTokens))
		})
	}
}

func TestClaudeDefaultMaxTokensRejectUnsafeConfiguredValuesAtRuntime(t *testing.T) {
	settings := &ClaudeSettings{DefaultMaxTokens: map[string]int{
		"default":   4096,
		"valid":     2048,
		"negative":  -1,
		"oversized": common.MaxTokensLimit + 1,
	}}

	assert.Equal(t, 2048, settings.GetDefaultMaxTokens("valid"))
	assert.Equal(t, 4096, settings.GetDefaultMaxTokens("negative"))
	assert.Equal(t, 4096, settings.GetDefaultMaxTokens("oversized"))
	assert.Equal(t, 4096, settings.GetDefaultMaxTokens("missing"))

	settings.DefaultMaxTokens["default"] = -1
	assert.Equal(t, defaultClaudeMaxTokens, settings.GetDefaultMaxTokens("missing"))
}

func TestClaudeSettingsWriteHeadersDeduplicatesAcrossCommaSeparatedAndRepeatedValues(t *testing.T) {
	settings := &ClaudeSettings{
		HeadersSettings: map[string]map[string][]string{
			"claude-3-7-sonnet-20250219-thinking": {
				"anthropic-beta": {
					"token-efficient-tools-2025-02-19",
					"computer-use-2025-01-24",
				},
			},
		},
	}

	headers := http.Header{}
	headers.Add("anthropic-beta", "output-128k-2025-02-19, token-efficient-tools-2025-02-19")
	headers.Add("anthropic-beta", "token-efficient-tools-2025-02-19")

	settings.WriteHeaders("claude-3-7-sonnet-20250219-thinking", &headers)

	got := headers.Values("anthropic-beta")
	if len(got) != 1 {
		t.Fatalf("expected duplicate values to collapse into one header, got %v", got)
	}
	expected := "output-128k-2025-02-19,token-efficient-tools-2025-02-19,computer-use-2025-01-24"
	if got[0] != expected {
		t.Fatalf("expected deduplicated merged header %q, got %q", expected, got[0])
	}
}
