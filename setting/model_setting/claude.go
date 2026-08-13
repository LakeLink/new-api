package model_setting

import (
	"fmt"
	"math"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/config"
	"golang.org/x/net/http/httpguts"
)

const (
	claudeMinimumThinkingBudgetTokens = 1024
	defaultClaudeThinkingBudgetRatio  = 0.8
	defaultClaudeMaxTokens            = 8192
)

//var claudeHeadersSettings = map[string][]string{}
//
//var ClaudeThinkingAdapterEnabled = true
//var ClaudeThinkingAdapterMaxTokens = 8192
//var ClaudeThinkingAdapterBudgetTokensPercentage = 0.8

// ClaudeSettings 定义Claude模型的配置
type ClaudeSettings struct {
	HeadersSettings                       map[string]map[string][]string `json:"model_headers_settings"`
	DefaultMaxTokens                      map[string]int                 `json:"default_max_tokens"`
	ThinkingAdapterEnabled                bool                           `json:"thinking_adapter_enabled"`
	ThinkingAdapterBudgetTokensPercentage float64                        `json:"thinking_adapter_budget_tokens_percentage"`
}

// 默认配置
var defaultClaudeSettings = ClaudeSettings{
	HeadersSettings:        map[string]map[string][]string{},
	ThinkingAdapterEnabled: true,
	DefaultMaxTokens: map[string]int{
		"default": defaultClaudeMaxTokens,
	},
	ThinkingAdapterBudgetTokensPercentage: defaultClaudeThinkingBudgetRatio,
}

// 全局实例
var claudeSettings = defaultClaudeSettings

func (s ClaudeSettings) Validate() error {
	percentage := s.ThinkingAdapterBudgetTokensPercentage
	if math.IsNaN(percentage) || math.IsInf(percentage, 0) || percentage < 0.1 || percentage > 1 {
		return fmt.Errorf("Claude thinking budget percentage must be finite and in the range [0.1, 1]")
	}
	for model, limit := range s.DefaultMaxTokens {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("Claude default max-token model name must not be empty")
		}
		if limit < 1 || limit > common.MaxTokensLimit {
			return fmt.Errorf("Claude default max tokens for %q must be in the range [1, %d]", model, common.MaxTokensLimit)
		}
	}
	for model, headers := range s.HeadersSettings {
		if strings.TrimSpace(model) == "" {
			return fmt.Errorf("Claude header model name must not be empty")
		}
		for name, values := range headers {
			if !httpguts.ValidHeaderFieldName(name) {
				return fmt.Errorf("Claude model %q contains invalid header name %q", model, name)
			}
			for _, value := range values {
				if !httpguts.ValidHeaderFieldValue(value) {
					return fmt.Errorf("Claude model %q header %q contains an invalid value", model, name)
				}
			}
		}
	}
	return nil
}

func init() {
	// 注册到全局配置管理器
	config.GlobalConfig.Register("claude", &claudeSettings)
}

// GetClaudeSettings 获取Claude配置
func GetClaudeSettings() *ClaudeSettings {
	settings := config.Snapshot[ClaudeSettings]("claude")
	if _, ok := settings.DefaultMaxTokens["default"]; ok {
		return settings
	}

	fallback := *settings
	fallback.DefaultMaxTokens = make(map[string]int, len(settings.DefaultMaxTokens)+1)
	for model, maxTokens := range settings.DefaultMaxTokens {
		fallback.DefaultMaxTokens[model] = maxTokens
	}
	fallback.DefaultMaxTokens["default"] = defaultClaudeMaxTokens
	return &fallback
}

func (c *ClaudeSettings) WriteHeaders(originModel string, httpHeader *http.Header) {
	if headers, ok := c.HeadersSettings[originModel]; ok {
		for headerKey, headerValues := range headers {
			mergedValues := normalizeHeaderListValues(
				append(append([]string(nil), httpHeader.Values(headerKey)...), headerValues...),
			)
			if len(mergedValues) == 0 {
				continue
			}
			httpHeader.Set(headerKey, strings.Join(mergedValues, ","))
		}
	}
}

func normalizeHeaderListValues(values []string) []string {
	normalizedValues := make([]string, 0, len(values))
	seenValues := make(map[string]struct{}, len(values))
	for _, value := range values {
		for _, item := range strings.Split(value, ",") {
			normalizedItem := strings.TrimSpace(item)
			if normalizedItem == "" {
				continue
			}
			if _, exists := seenValues[normalizedItem]; exists {
				continue
			}
			seenValues[normalizedItem] = struct{}{}
			normalizedValues = append(normalizedValues, normalizedItem)
		}
	}
	return normalizedValues
}

func (c *ClaudeSettings) GetDefaultMaxTokens(model string) int {
	if maxTokens, ok := c.DefaultMaxTokens[model]; ok && maxTokens > 0 && maxTokens <= common.MaxTokensLimit {
		return maxTokens
	}
	if maxTokens := c.DefaultMaxTokens["default"]; maxTokens > 0 && maxTokens <= common.MaxTokensLimit {
		return maxTokens
	}
	return defaultClaudeMaxTokens
}

// GetThinkingBudgetTokens turns the configured ratio into a provider-valid
// manual thinking budget. Callers ensure maxTokens is at least 1,280, leaving
// room for Anthropic's 1,024-token minimum while keeping the budget strictly
// below max_tokens.
func (c *ClaudeSettings) GetThinkingBudgetTokens(maxTokens uint) int {
	percentage := c.ThinkingAdapterBudgetTokensPercentage
	if math.IsNaN(percentage) || math.IsInf(percentage, 0) || percentage < 0.1 || percentage > 1 {
		percentage = defaultClaudeThinkingBudgetRatio
	}

	budgetTokens := common.QuotaFromFloat(float64(maxTokens) * percentage)
	maxBudgetTokens := common.QuotaFromFloat(float64(maxTokens - 1))
	if budgetTokens < claudeMinimumThinkingBudgetTokens {
		budgetTokens = claudeMinimumThinkingBudgetTokens
	}
	if budgetTokens > maxBudgetTokens {
		budgetTokens = maxBudgetTokens
	}
	return budgetTokens
}

// ValidateClaudeDefaultMaxTokens validates the JSON persisted by the option
// API. Zero stays allowed — the current Messages API accepts max_tokens: 0 as
// cache pre-warming — but negative values are rejected because they would
// wrap into huge unsigned values during request conversion.
func ValidateClaudeDefaultMaxTokens(value string) error {
	var settings map[string]int
	if err := common.UnmarshalJsonStr(value, &settings); err != nil {
		return fmt.Errorf("Claude default max tokens must be a JSON map of model to integer: %w", err)
	}
	if settings == nil {
		return fmt.Errorf("Claude default max tokens must be a JSON map of model to integer")
	}
	for model, maxTokens := range settings {
		if maxTokens < 0 {
			return fmt.Errorf("negative Claude default max_tokens %d for %q", maxTokens, model)
		}
	}
	return nil
}
