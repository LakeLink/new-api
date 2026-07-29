package ratio_setting

import (
	"github.com/QuantumNous/new-api/types"
)

var defaultCacheRatio = map[string]float64{
	"grok-4.5":                            0.15,
	"grok-4.5-latest":                     0.15,
	"grok-4.3":                            0.16,
	"grok-4.3-latest":                     0.16,
	"grok-latest":                         0.16,
	"grok-4.20-0309-reasoning":            0.16,
	"grok-4.20-0309":                      0.16,
	"grok-4.20-reasoning":                 0.16,
	"grok-4.20-reasoning-latest":          0.16,
	"grok-4.20":                           0.16,
	"grok-4.20-0309-non-reasoning":        0.16,
	"grok-4.20-non-reasoning":             0.16,
	"grok-4.20-non-reasoning-latest":      0.16,
	"grok-4.20-multi-agent-0309":          0.16,
	"grok-4.20-multi-agent":               0.16,
	"grok-4.20-multi-agent-latest":        0.16,
	"grok-build-0.1":                      0.2,
	"grok-build-latest":                   0.2,
	"grok-code-fast-1":                    0.2,
	"grok-code-fast":                      0.2,
	"grok-code-fast-1-0825":               0.2,
	"claude-fable-5":                      0.1,
	"claude-mythos-5":                     0.1,
	"claude-sonnet-5":                     0.1,
	"gemini-3.6-flash":                    0.1,
	"gemini-3.5-flash":                    0.1,
	"gemini-3.5-flash-lite":               0.1,
	"gemini-3.1-flash-lite":               0.1,
	"gemini-3-flash-preview":              0.1,
	"gemini-flash-latest":                 0.1,
	"gemini-flash-lite-latest":            0.1,
	"gemini-pro-latest":                   0.1,
	"gemini-2.5-pro":                      0.1,
	"gemini-2.5-flash":                    0.1,
	"gemini-2.5-flash-lite":               0.1,
	"gemini-3.1-pro-preview":              0.1,
	"gemini-3.1-pro-preview-customtools":  0.1,
	"gpt-4":                               0.5,
	"o1":                                  0.5,
	"o1-2024-12-17":                       0.5,
	"o1-preview-2024-09-12":               0.5,
	"o1-preview":                          0.5,
	"o1-mini-2024-09-12":                  0.5,
	"o1-mini":                             0.5,
	"o3-mini":                             0.5,
	"o3-mini-2025-01-31":                  0.5,
	"gpt-4o-2024-11-20":                   0.5,
	"gpt-4o-2024-08-06":                   0.5,
	"gpt-4o":                              0.5,
	"gpt-4o-mini-2024-07-18":              0.5,
	"gpt-4o-mini":                         0.5,
	"gpt-4o-realtime-preview":             0.5,
	"gpt-4o-mini-realtime-preview":        0.5,
	"gpt-4.5-preview":                     0.5,
	"gpt-4.5-preview-2025-02-27":          0.5,
	"gpt-4.1":                             0.25,
	"gpt-4.1-mini":                        0.25,
	"gpt-4.1-nano":                        0.25,
	"gpt-5":                               0.1,
	"gpt-5-2025-08-07":                    0.1,
	"gpt-5-chat-latest":                   0.1,
	"gpt-5-mini":                          0.1,
	"gpt-5-mini-2025-08-07":               0.1,
	"gpt-5-nano":                          0.1,
	"gpt-5-nano-2025-08-07":               0.1,
	"gpt-5-codex":                         0.1,
	"gpt-5-search-api":                    0.1,
	"gpt-5-search-api-2025-10-14":         0.1,
	"gpt-5.1":                             0.1,
	"gpt-5.1-2025-11-13":                  0.1,
	"gpt-5.1-chat-latest":                 0.1,
	"gpt-5.1-codex":                       0.1,
	"gpt-5.1-codex-mini":                  0.1,
	"gpt-5.1-codex-max":                   0.1,
	"gpt-5.2":                             0.1,
	"gpt-5.2-2025-12-11":                  0.1,
	"gpt-5.2-chat-latest":                 0.1,
	"gpt-5.2-codex":                       0.1,
	"gpt-5.3-chat-latest":                 0.1,
	"gpt-5.3-codex":                       0.1,
	"gpt-5.4":                             0.1,
	"gpt-5.4-2026-03-05":                  0.1,
	"gpt-5.4-mini":                        0.1,
	"gpt-5.4-nano":                        0.1,
	"gpt-5.5":                             0.1,
	"gpt-5.5-2026-04-23":                  0.1,
	"gpt-5.6":                             0.1,
	"gpt-5.6-sol":                         0.1,
	"gpt-5.6-terra":                       0.1,
	"gpt-5.6-luna":                        0.1,
	"gpt-image-1-mini":                    0.1,
	"gpt-image-1.5":                       0.25,
	"chatgpt-image-latest":                0.25,
	"gpt-image-2":                         0.25,
	"gpt-image-2-2026-04-21":              0.25,
	"deepseek-chat":                       0.0028 / 0.14,
	"deepseek-reasoner":                   0.0028 / 0.14,
	"deepseek-coder":                      0.25,
	"deepseek-v4-flash":                   0.0028 / 0.14,
	"deepseek-v4-pro":                     0.003625 / 0.435,
	"kimi-k3":                             2.0 / 20.0,
	"kimi-k2.7-code":                      1.3 / 6.5,
	"kimi-k2.7-code-highspeed":            2.6 / 13.0,
	"kimi-k2.6":                           1.1 / 6.5,
	"kimi-k2.5":                           0.7 / 4.0,
	"claude-3-sonnet-20240229":            0.1,
	"claude-3-opus-20240229":              0.1,
	"claude-3-haiku-20240307":             0.1,
	"claude-3-5-haiku-20241022":           0.1,
	"claude-haiku-4-5-20251001":           0.1,
	"claude-3-5-sonnet-20240620":          0.1,
	"claude-3-5-sonnet-20241022":          0.1,
	"claude-3-7-sonnet-20250219":          0.1,
	"claude-3-7-sonnet-20250219-thinking": 0.1,
	"claude-sonnet-4-20250514":            0.1,
	"claude-sonnet-4-20250514-thinking":   0.1,
	"claude-opus-4-20250514":              0.1,
	"claude-opus-4-20250514-thinking":     0.1,
	"claude-opus-4-1-20250805":            0.1,
	"claude-opus-4-1-20250805-thinking":   0.1,
	"claude-sonnet-4-5-20250929":          0.1,
	"claude-sonnet-4-5-20250929-thinking": 0.1,
	"claude-sonnet-4-6":                   0.1,
	"claude-opus-4-5-20251101":            0.1,
	"claude-opus-4-5-20251101-thinking":   0.1,
	"claude-opus-4-6":                     0.1,
	"claude-opus-4-6-thinking":            0.1,
	"claude-opus-4-6-max":                 0.1,
	"claude-opus-4-6-high":                0.1,
	"claude-opus-4-6-medium":              0.1,
	"claude-opus-4-6-low":                 0.1,
	"claude-opus-4-7":                     0.1,
	"claude-opus-4-7-thinking":            0.1,
	"claude-opus-4-7-max":                 0.1,
	"claude-opus-4-7-xhigh":               0.1,
	"claude-opus-4-7-high":                0.1,
	"claude-opus-4-7-medium":              0.1,
	"claude-opus-4-7-low":                 0.1,
	"claude-opus-4-8":                     0.1,
	"claude-opus-4-8-thinking":            0.1,
	"claude-opus-4-8-max":                 0.1,
	"claude-opus-4-8-xhigh":               0.1,
	"claude-opus-4-8-high":                0.1,
	"claude-opus-4-8-medium":              0.1,
	"claude-opus-4-8-low":                 0.1,
	"claude-opus-5":                       0.1,
	"claude-opus-5-thinking":              0.1,
	"claude-opus-5-max":                   0.1,
	"claude-opus-5-xhigh":                 0.1,
	"claude-opus-5-high":                  0.1,
	"claude-opus-5-medium":                0.1,
	"claude-opus-5-low":                   0.1,
}

var defaultCreateCacheRatio = map[string]float64{
	"gpt-5.6":                             1.25,
	"gpt-5.6-sol":                         1.25,
	"gpt-5.6-terra":                       1.25,
	"gpt-5.6-luna":                        1.25,
	"claude-fable-5":                      1.25,
	"claude-mythos-5":                     1.25,
	"claude-sonnet-5":                     1.25,
	"claude-3-sonnet-20240229":            1.25,
	"claude-3-opus-20240229":              1.25,
	"claude-3-haiku-20240307":             1.25,
	"claude-3-5-haiku-20241022":           1.25,
	"claude-haiku-4-5-20251001":           1.25,
	"claude-3-5-sonnet-20240620":          1.25,
	"claude-3-5-sonnet-20241022":          1.25,
	"claude-3-7-sonnet-20250219":          1.25,
	"claude-3-7-sonnet-20250219-thinking": 1.25,
	"claude-sonnet-4-20250514":            1.25,
	"claude-sonnet-4-20250514-thinking":   1.25,
	"claude-opus-4-20250514":              1.25,
	"claude-opus-4-20250514-thinking":     1.25,
	"claude-opus-4-1-20250805":            1.25,
	"claude-opus-4-1-20250805-thinking":   1.25,
	"claude-sonnet-4-5-20250929":          1.25,
	"claude-sonnet-4-5-20250929-thinking": 1.25,
	"claude-sonnet-4-6":                   1.25,
	"claude-opus-4-5-20251101":            1.25,
	"claude-opus-4-5-20251101-thinking":   1.25,
	"claude-opus-4-6":                     1.25,
	"claude-opus-4-6-thinking":            1.25,
	"claude-opus-4-6-max":                 1.25,
	"claude-opus-4-6-high":                1.25,
	"claude-opus-4-6-medium":              1.25,
	"claude-opus-4-6-low":                 1.25,
	"claude-opus-4-7":                     1.25,
	"claude-opus-4-7-thinking":            1.25,
	"claude-opus-4-7-max":                 1.25,
	"claude-opus-4-7-xhigh":               1.25,
	"claude-opus-4-7-high":                1.25,
	"claude-opus-4-7-medium":              1.25,
	"claude-opus-4-7-low":                 1.25,
	"claude-opus-4-8":                     1.25,
	"claude-opus-4-8-thinking":            1.25,
	"claude-opus-4-8-max":                 1.25,
	"claude-opus-4-8-xhigh":               1.25,
	"claude-opus-4-8-high":                1.25,
	"claude-opus-4-8-medium":              1.25,
	"claude-opus-4-8-low":                 1.25,
	"claude-opus-5":                       1.25,
	"claude-opus-5-thinking":              1.25,
	"claude-opus-5-max":                   1.25,
	"claude-opus-5-xhigh":                 1.25,
	"claude-opus-5-high":                  1.25,
	"claude-opus-5-medium":                1.25,
	"claude-opus-5-low":                   1.25,
}

//var defaultCreateCacheRatio = map[string]float64{}

var cacheRatioMap = types.NewRWMap[string, float64]()
var createCacheRatioMap = types.NewRWMap[string, float64]()

// GetCacheRatioMap returns a copy of the cache ratio map
func GetCacheRatioMap() map[string]float64 {
	return cacheRatioMap.ReadAll()
}

// CacheRatio2JSONString converts the cache ratio map to a JSON string
func CacheRatio2JSONString() string {
	return cacheRatioMap.MarshalJSONString()
}

// CreateCacheRatio2JSONString converts the create cache ratio map to a JSON string
func CreateCacheRatio2JSONString() string {
	return createCacheRatioMap.MarshalJSONString()
}

// UpdateCacheRatioByJSONString updates the cache ratio map from a JSON string
func UpdateCacheRatioByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(cacheRatioMap, jsonStr, InvalidateExposedDataCache)
}

// UpdateCreateCacheRatioByJSONString updates the create cache ratio map from a JSON string
func UpdateCreateCacheRatioByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(createCacheRatioMap, jsonStr, InvalidateExposedDataCache)
}

// GetCacheRatio returns the cache ratio for a model
func GetCacheRatio(name string) (float64, bool) {
	name = FormatMatchingModelName(name)
	ratio, ok := cacheRatioMap.Get(name)
	if !ok {
		return 1, false // Default to 1 if not found
	}
	return ratio, true
}

func GetCreateCacheRatio(name string) (float64, bool) {
	ratio, ok := createCacheRatioMap.Get(name)
	if !ok {
		return 1.25, false // Default to 1.25 if not found
	}
	return ratio, true
}

func GetDefaultCacheRatio(name string) float64 {
	name = FormatMatchingModelName(name)
	if ratio, ok := defaultCacheRatio[name]; ok {
		return ratio
	}
	return 1
}

func GetDefaultCreateCacheRatio(name string) float64 {
	if ratio, ok := defaultCreateCacheRatio[name]; ok {
		return ratio
	}
	return 1.25
}

func GetCacheRatioCopy() map[string]float64 {
	return cacheRatioMap.ReadAll()
}

func GetCreateCacheRatioCopy() map[string]float64 {
	return createCacheRatioMap.ReadAll()
}
