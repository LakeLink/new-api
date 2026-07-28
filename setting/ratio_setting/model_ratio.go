package ratio_setting

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/QuantumNous/new-api/setting/reasoning"
	"github.com/QuantumNous/new-api/types"
)

// from songquanpeng/one-api
const (
	USD2RMB = 7.3 // 暂定 1 USD = 7.3 RMB
	USD     = 500 // $0.002 = 1 -> $1 = 500
	RMB     = USD / USD2RMB

	// OpenAILongContextThreshold is the prompt-token boundary after which
	// OpenAI charges supported 1M-context models at long-context rates.
	OpenAILongContextThreshold = 272000
)

type OpenAIPriorityPriceRatios struct {
	ModelRatio         float64
	CompletionRatio    float64
	CacheRatio         float64
	CacheCreationRatio float64
}

func GetOpenAIPriorityPriceRatios(model string) (OpenAIPriorityPriceRatios, bool) {
	var input, cached, cacheWrite, output float64
	switch {
	case model == "gpt-5.6" || model == "gpt-5.6-sol":
		input, cached, cacheWrite, output = 10, 1, 12.5, 60
	case model == "gpt-5.6-terra":
		input, cached, cacheWrite, output = 5, 0.5, 6.25, 30
	case model == "gpt-5.6-luna":
		input, cached, cacheWrite, output = 2, 0.2, 2.5, 12
	case model == "gpt-5.5" || model == "gpt-5.5-2026-04-23":
		input, cached, output = 12.5, 1.25, 75
	case model == "gpt-5.4" || model == "gpt-5.4-2026-03-05":
		input, cached, output = 5, 0.5, 30
	case model == "gpt-5.4-mini":
		input, cached, output = 1.5, 0.15, 9
	default:
		return OpenAIPriorityPriceRatios{}, false
	}

	ratios := OpenAIPriorityPriceRatios{
		ModelRatio:      input / 2,
		CompletionRatio: output / input,
		CacheRatio:      cached / input,
	}
	if cacheWrite > 0 {
		ratios.CacheCreationRatio = cacheWrite / input
	}
	return ratios, true
}

func IsOpenAILongContextModel(model string) bool {
	return model == "gpt-5.4" || model == "gpt-5.4-2026-03-05" ||
		model == "gpt-5.4-pro" || model == "gpt-5.4-pro-2026-03-05" ||
		model == "gpt-5.5" || model == "gpt-5.5-2026-04-23" || model == "gpt-5.5-pro" ||
		model == "gpt-5.6" || strings.HasPrefix(model, "gpt-5.6-")
}

func IsOpenAIRegionalProcessingUpliftModel(model string) bool {
	return strings.HasPrefix(model, "gpt-5.4") || strings.HasPrefix(model, "gpt-5.5") ||
		strings.HasPrefix(model, "gpt-5.6") || strings.HasPrefix(model, "gpt-image-2")
}

// modelRatio
// https://platform.openai.com/docs/models/model-endpoint-compatibility
// https://cloud.baidu.com/doc/WENXINWORKSHOP/s/Blfmc9dlf
// https://openai.com/pricing
// TODO: when a new api is enabled, check the pricing here
// 1 === $0.002 / 1K tokens
// 1 === ￥0.014 / 1k tokens

var defaultModelRatio = map[string]float64{
	//"midjourney":                50,
	"gpt-4-gizmo-*":                             15,
	"gpt-4o-gizmo-*":                            2.5,
	"gpt-4-all":                                 15,
	"gpt-4o-all":                                15,
	"gpt-4":                                     15,
	"gpt-4-0613":                                15,
	"gpt-4-32k":                                 30,
	"gpt-4-32k-0613":                            30,
	"gpt-4-1106-preview":                        5,    // $10 / 1M tokens
	"gpt-4-0125-preview":                        5,    // $10 / 1M tokens
	"gpt-4-turbo-preview":                       5,    // $10 / 1M tokens
	"gpt-4-vision-preview":                      5,    // $10 / 1M tokens
	"gpt-4-1106-vision-preview":                 5,    // $10 / 1M tokens
	"chatgpt-4o-latest":                         2.5,  // $5 / 1M tokens
	"gpt-4o":                                    1.25, // $2.5 / 1M tokens
	"gpt-4o-audio-preview":                      1.25, // $2.5 / 1M tokens
	"gpt-4o-audio-preview-2024-10-01":           1.25, // $2.5 / 1M tokens
	"gpt-4o-2024-05-13":                         2.5,  // $5 / 1M tokens
	"gpt-4o-2024-08-06":                         1.25, // $2.5 / 1M tokens
	"gpt-4o-2024-11-20":                         1.25, // $2.5 / 1M tokens
	"gpt-4o-realtime-preview":                   2.5,
	"gpt-4o-realtime-preview-2024-10-01":        2.5,
	"gpt-4o-realtime-preview-2024-12-17":        2.5,
	"gpt-4o-realtime-preview-2025-06-03":        2.5,
	"gpt-4o-mini-realtime-preview":              0.3,
	"gpt-4o-mini-realtime-preview-2024-12-17":   0.3,
	"gpt-4.1":                                   1.0,  // $2 / 1M tokens
	"gpt-4.1-2025-04-14":                        1.0,  // $2 / 1M tokens
	"gpt-4.1-mini":                              0.2,  // $0.4 / 1M tokens
	"gpt-4.1-mini-2025-04-14":                   0.2,  // $0.4 / 1M tokens
	"gpt-4.1-nano":                              0.05, // $0.1 / 1M tokens
	"gpt-4.1-nano-2025-04-14":                   0.05, // $0.1 / 1M tokens
	"gpt-image-1":                               2.5,  // $5 / 1M tokens
	"o1":                                        7.5,  // $15 / 1M tokens
	"o1-2024-12-17":                             7.5,  // $15 / 1M tokens
	"o1-preview":                                7.5,  // $15 / 1M tokens
	"o1-preview-2024-09-12":                     7.5,  // $15 / 1M tokens
	"o1-mini":                                   0.55, // $1.1 / 1M tokens
	"o1-mini-2024-09-12":                        0.55, // $1.1 / 1M tokens
	"o1-pro":                                    75.0, // $150 / 1M tokens
	"o1-pro-2025-03-19":                         75.0, // $150 / 1M tokens
	"o3-mini":                                   0.55,
	"o3-mini-2025-01-31":                        0.55,
	"o3-mini-high":                              0.55,
	"o3-mini-2025-01-31-high":                   0.55,
	"o3-mini-low":                               0.55,
	"o3-mini-2025-01-31-low":                    0.55,
	"o3-mini-medium":                            0.55,
	"o3-mini-2025-01-31-medium":                 0.55,
	"o3":                                        1.0,  // $2 / 1M tokens
	"o3-2025-04-16":                             1.0,  // $2 / 1M tokens
	"o3-pro":                                    10.0, // $20 / 1M tokens
	"o3-pro-2025-06-10":                         10.0, // $20 / 1M tokens
	"o3-deep-research":                          5.0,  // $10 / 1M tokens
	"o3-deep-research-2025-06-26":               5.0,  // $10 / 1M tokens
	"o4-mini":                                   0.55, // $1.1 / 1M tokens
	"o4-mini-2025-04-16":                        0.55, // $1.1 / 1M tokens
	"o4-mini-deep-research":                     1.0,  // $2 / 1M tokens
	"o4-mini-deep-research-2025-06-26":          1.0,  // $2 / 1M tokens
	"gpt-4o-mini":                               0.075,
	"gpt-4o-mini-2024-07-18":                    0.075,
	"gpt-4-turbo":                               5, // $0.01 / 1K tokens
	"gpt-4-turbo-2024-04-09":                    5, // $0.01 / 1K tokens
	"gpt-4.5-preview":                           37.5,
	"gpt-4.5-preview-2025-02-27":                37.5,
	"gpt-5":                                     0.625,
	"gpt-5-2025-08-07":                          0.625,
	"gpt-5-chat-latest":                         0.625,
	"gpt-5-mini":                                0.125,
	"gpt-5-mini-2025-08-07":                     0.125,
	"gpt-5-nano":                                0.025,
	"gpt-5-nano-2025-08-07":                     0.025,
	"gpt-5-codex":                               0.625,
	"gpt-5-pro":                                 7.5,
	"gpt-5-pro-2025-10-06":                      7.5,
	"gpt-5-search-api":                          0.625,
	"gpt-5-search-api-2025-10-14":               0.625,
	"gpt-5.1":                                   0.625,
	"gpt-5.1-2025-11-13":                        0.625,
	"gpt-5.1-chat-latest":                       0.625,
	"gpt-5.1-codex":                             0.625,
	"gpt-5.1-codex-mini":                        0.125,
	"gpt-5.1-codex-max":                         0.625,
	"gpt-5.2":                                   0.875,
	"gpt-5.2-2025-12-11":                        0.875,
	"gpt-5.2-chat-latest":                       0.875,
	"gpt-5.2-codex":                             0.875,
	"gpt-5.2-pro":                               10.5,
	"gpt-5.2-pro-2025-12-11":                    10.5,
	"gpt-5.3-chat-latest":                       0.875,
	"gpt-5.3-codex":                             0.875,
	"gpt-5.4":                                   1.25,
	"gpt-5.4-2026-03-05":                        1.25,
	"gpt-5.4-mini":                              0.375,
	"gpt-5.4-nano":                              0.1,
	"gpt-5.4-pro":                               15,
	"gpt-5.4-pro-2026-03-05":                    15,
	"gpt-5.5":                                   2.5, // $5 / 1M tokens
	"gpt-5.5-2026-04-23":                        2.5,
	"gpt-5.5-pro":                               15,
	"gpt-5.6":                                   2.5,
	"gpt-5.6-sol":                               2.5,
	"gpt-5.6-terra":                             1.25,
	"gpt-5.6-luna":                              0.5,
	"gpt-3.5-turbo":                             0.25,
	"gpt-3.5-turbo-0613":                        0.75,
	"gpt-3.5-turbo-16k":                         1.5, // $0.003 / 1K tokens
	"gpt-3.5-turbo-16k-0613":                    1.5,
	"gpt-3.5-turbo-instruct":                    0.75, // $0.0015 / 1K tokens
	"gpt-3.5-turbo-instruct-0914":               0.75,
	"gpt-3.5-turbo-1106":                        0.5, // $0.001 / 1K tokens
	"gpt-3.5-turbo-0125":                        0.25,
	"text-ada-001":                              0.2,
	"text-babbage-001":                          0.25,
	"text-curie-001":                            1,
	"text-davinci-edit-001":                     10,
	"code-davinci-edit-001":                     10,
	"whisper-1":                                 3, // $0.006 / minute at 1,000 duration tokens/minute
	"gpt-4o-transcribe":                         3,
	"gpt-4o-transcribe-diarize":                 3,
	"gpt-4o-mini-transcribe":                    1.5,
	"gpt-4o-mini-transcribe-2025-03-20":         1.5,
	"gpt-4o-mini-transcribe-2025-12-15":         1.5,
	"tts-1":                                     7.5, // 1k characters -> $0.015
	"tts-1-1106":                                7.5, // 1k characters -> $0.015
	"tts-1-hd":                                  15,  // 1k characters -> $0.03
	"tts-1-hd-1106":                             15,  // 1k characters -> $0.03
	"davinci":                                   10,
	"curie":                                     10,
	"text-embedding-3-small":                    0.01,
	"text-embedding-3-large":                    0.065,
	"text-embedding-ada-002":                    0.05,
	"text-search-ada-doc-001":                   10,
	"text-moderation-stable":                    0,
	"text-moderation-latest":                    0,
	"omni-moderation-latest":                    0,
	"omni-moderation-2024-09-26":                0,
	"gpt-4o-search-preview":                     1.25,
	"gpt-4o-search-preview-2025-03-11":          1.25,
	"gpt-4o-mini-search-preview":                0.075,
	"gpt-4o-mini-search-preview-2025-03-11":     0.075,
	"computer-use-preview":                      1.5,
	"computer-use-preview-2025-03-11":           1.5,
	"gpt-4o-audio-preview-2024-12-17":           1.25,
	"gpt-4o-audio-preview-2025-06-03":           1.25,
	"gpt-4o-mini-audio-preview":                 0.3,
	"gpt-4o-mini-audio-preview-2024-12-17":      0.3,
	"gpt-4o-mini-tts":                           0.3,
	"gpt-4o-mini-tts-2025-03-20":                0.3,
	"gpt-4o-mini-tts-2025-12-15":                0.3,
	"gpt-audio":                                 1.25,
	"gpt-audio-2025-08-28":                      1.25,
	"gpt-audio-1.5":                             1.25,
	"gpt-audio-mini":                            0.3,
	"gpt-audio-mini-2025-10-06":                 0.3,
	"gpt-audio-mini-2025-12-15":                 0.3,
	"gpt-realtime":                              2,
	"gpt-realtime-2025-08-28":                   2,
	"gpt-realtime-1.5":                          2,
	"gpt-realtime-mini":                         0.3,
	"gpt-realtime-mini-2025-10-06":              0.3,
	"gpt-realtime-mini-2025-12-15":              0.3,
	"gpt-image-1-mini":                          1,
	"gpt-image-1.5":                             2.5,
	"chatgpt-image-latest":                      2.5,
	"gpt-image-2":                               2.5,
	"gpt-image-2-2026-04-21":                    2.5,
	"babbage-002":                               0.2,
	"davinci-002":                               1,
	"claude-3-haiku-20240307":                   0.125, // $0.25 / 1M tokens
	"claude-3-5-haiku-20241022":                 0.5,   // $1 / 1M tokens
	"claude-haiku-4-5-20251001":                 0.5,   // $1 / 1M tokens
	"claude-fable-5":                            5,     // $10 / 1M tokens
	"claude-mythos-5":                           5,     // $10 / 1M tokens, limited availability
	"claude-sonnet-5":                           1,     // $2 / 1M tokens through August 31, 2026
	"claude-3-sonnet-20240229":                  1.5,   // $3 / 1M tokens
	"claude-3-5-sonnet-20240620":                1.5,
	"claude-3-5-sonnet-20241022":                1.5,
	"claude-3-7-sonnet-20250219":                1.5,
	"claude-3-7-sonnet-20250219-thinking":       1.5,
	"claude-sonnet-4-20250514":                  1.5,
	"claude-sonnet-4-20250514-thinking":         1.5,
	"claude-sonnet-4-5-20250929":                1.5,
	"claude-sonnet-4-5-20250929-thinking":       1.5,
	"claude-sonnet-4-6":                         1.5,
	"claude-opus-4-5-20251101":                  2.5,
	"claude-opus-4-5-20251101-thinking":         2.5,
	"claude-opus-4-6":                           2.5,
	"claude-opus-4-6-max":                       2.5,
	"claude-opus-4-6-high":                      2.5,
	"claude-opus-4-6-medium":                    2.5,
	"claude-opus-4-6-low":                       2.5,
	"claude-opus-4-7":                           2.5,
	"claude-opus-4-7-max":                       2.5,
	"claude-opus-4-7-xhigh":                     2.5,
	"claude-opus-4-7-high":                      2.5,
	"claude-opus-4-7-medium":                    2.5,
	"claude-opus-4-7-low":                       2.5,
	"claude-opus-4-7-thinking":                  2.5,
	"claude-opus-4-8":                           2.5,
	"claude-opus-4-8-max":                       2.5,
	"claude-opus-4-8-xhigh":                     2.5,
	"claude-opus-4-8-high":                      2.5,
	"claude-opus-4-8-medium":                    2.5,
	"claude-opus-4-8-low":                       2.5,
	"claude-opus-4-8-thinking":                  2.5,
	"claude-opus-5":                             2.5, // $5 / 1M tokens
	"claude-opus-5-max":                         2.5,
	"claude-opus-5-xhigh":                       2.5,
	"claude-opus-5-high":                        2.5,
	"claude-opus-5-medium":                      2.5,
	"claude-opus-5-low":                         2.5,
	"claude-opus-5-thinking":                    2.5,
	"claude-3-opus-20240229":                    7.5, // $15 / 1M tokens
	"claude-opus-4-20250514":                    7.5,
	"claude-opus-4-20250514-thinking":           7.5,
	"claude-opus-4-1-20250805":                  7.5,
	"claude-opus-4-1-20250805-thinking":         7.5,
	"ERNIE-4.0-8K":                              0.120 * RMB,
	"ERNIE-3.5-8K":                              0.012 * RMB,
	"ERNIE-3.5-8K-0205":                         0.024 * RMB,
	"ERNIE-3.5-8K-1222":                         0.012 * RMB,
	"ERNIE-Bot-8K":                              0.024 * RMB,
	"ERNIE-3.5-4K-0205":                         0.012 * RMB,
	"ERNIE-Speed-8K":                            0.004 * RMB,
	"ERNIE-Speed-128K":                          0.004 * RMB,
	"ERNIE-Lite-8K-0922":                        0.008 * RMB,
	"ERNIE-Lite-8K-0308":                        0.003 * RMB,
	"ERNIE-Tiny-8K":                             0.001 * RMB,
	"BLOOMZ-7B":                                 0.004 * RMB,
	"Embedding-V1":                              0.002 * RMB,
	"bge-large-zh":                              0.002 * RMB,
	"bge-large-en":                              0.002 * RMB,
	"tao-8k":                                    0.002 * RMB,
	"PaLM-2":                                    1,
	"gemini-1.5-pro-latest":                     1.25, // $3.5 / 1M tokens
	"gemini-1.5-flash-latest":                   0.075,
	"gemini-3.6-flash":                          0.75,
	"gemini-3.5-flash":                          0.75,
	"gemini-3.5-flash-lite":                     0.15,
	"gemini-3.1-flash-lite":                     0.125,
	"gemini-3-flash-preview":                    0.25,
	"gemini-flash-latest":                       0.75,
	"gemini-flash-lite-latest":                  0.15,
	"gemini-pro-latest":                         1,
	"gemini-2.0-flash":                          0.05,
	"gemini-2.5-pro-exp-03-25":                  0.625,
	"gemini-2.5-pro-preview-03-25":              0.625,
	"gemini-2.5-pro":                            0.625,
	"gemini-2.5-flash-preview-04-17":            0.075,
	"gemini-2.5-flash-preview-04-17-thinking":   0.075,
	"gemini-2.5-flash-preview-04-17-nothinking": 0.075,
	"gemini-2.5-flash-preview-05-20":            0.075,
	"gemini-2.5-flash-preview-05-20-thinking":   0.075,
	"gemini-2.5-flash-preview-05-20-nothinking": 0.075,
	"gemini-2.5-flash-thinking-*":               0.075, // 用于为后续所有2.5 flash thinking budget 模型设置默认倍率
	"gemini-2.5-pro-thinking-*":                 0.625, // 用于为后续所有2.5 pro thinking budget 模型设置默认倍率
	"gemini-2.5-flash-lite-preview-thinking-*":  0.05,
	"gemini-2.5-flash-lite-preview-06-17":       0.05,
	"gemini-2.5-flash":                          0.15,
	"gemini-2.5-flash-lite":                     0.05,
	"gemini-2.5-flash-image":                    0.15,
	"gemini-3.1-flash-image":                    0.25,
	"gemini-3.1-flash-lite-image":               0.125,
	"gemini-3-pro-image":                        1,
	"gemini-2.5-flash-preview-tts":              0.25,
	"gemini-2.5-pro-preview-tts":                0.5,
	"gemini-3.1-flash-tts-preview":              0.5,
	"gemini-2.5-computer-use-preview-10-2025":   0.625,
	"gemini-3.1-pro-preview":                    1,
	"gemini-3.1-pro-preview-customtools":        1,
	"gemini-robotics-er-1.5-preview":            0.15,
	"gemini-robotics-er-1.6-preview":            0.5,
	"gemini-embedding-001":                      0.075,
	"gemini-embedding-2":                        0.1,
	"text-embedding-004":                        0.001,
	"chatglm_turbo":                             0.3572,     // ￥0.005 / 1k tokens
	"chatglm_pro":                               0.7143,     // ￥0.01 / 1k tokens
	"chatglm_std":                               0.3572,     // ￥0.005 / 1k tokens
	"chatglm_lite":                              0.1429,     // ￥0.002 / 1k tokens
	"glm-4":                                     7.143,      // ￥0.1 / 1k tokens
	"glm-4v":                                    0.05 * RMB, // ￥0.05 / 1k tokens
	"glm-4-alltools":                            0.1 * RMB,  // ￥0.1 / 1k tokens
	"glm-3-turbo":                               0.3572,
	"glm-4-plus":                                0.05 * RMB,
	"glm-4-0520":                                0.1 * RMB,
	"glm-4-air":                                 0.001 * RMB,
	"glm-4-airx":                                0.01 * RMB,
	"glm-4-long":                                0.001 * RMB,
	"glm-4-flash":                               0,
	"glm-4v-plus":                               0.01 * RMB,
	"qwen-turbo":                                0.8572, // ￥0.012 / 1k tokens
	"qwen-plus":                                 10,     // ￥0.14 / 1k tokens
	"text-embedding-v1":                         0.05,   // ￥0.0007 / 1k tokens
	"SparkDesk-v1.1":                            1.2858, // ￥0.018 / 1k tokens
	"SparkDesk-v2.1":                            1.2858, // ￥0.018 / 1k tokens
	"SparkDesk-v3.1":                            1.2858, // ￥0.018 / 1k tokens
	"SparkDesk-v3.5":                            1.2858, // ￥0.018 / 1k tokens
	"SparkDesk-v4.0":                            1.2858,
	"hunyuan":                                   7.143, // ¥0.1 / 1k tokens  // https://cloud.tencent.com/document/product/1729/97731#e0e6be58-60c8-469f-bdeb-6c264ce3b4d0
	// https://platform.lingyiwanwu.com/docs#-计费单元
	// 已经按照 7.2 来换算美元价格
	"yi-34b-chat-0205":      0.18,
	"yi-34b-chat-200k":      0.864,
	"yi-vl-plus":            0.432,
	"yi-large":              20.0 / 1000 * RMB,
	"yi-medium":             2.5 / 1000 * RMB,
	"yi-vision":             6.0 / 1000 * RMB,
	"yi-medium-200k":        12.0 / 1000 * RMB,
	"yi-spark":              1.0 / 1000 * RMB,
	"yi-large-rag":          25.0 / 1000 * RMB,
	"yi-large-turbo":        12.0 / 1000 * RMB,
	"yi-large-preview":      20.0 / 1000 * RMB,
	"yi-large-rag-preview":  25.0 / 1000 * RMB,
	"command":               0.5,
	"command-nightly":       0.5,
	"command-light":         0.5,
	"command-light-nightly": 0.5,
	"command-r":             0.25,
	"command-r-plus":        1.5,
	// Cohere documents these models as free on the standard API until its
	// published rate limits are reached. Dedicated Model Vault deployments use
	// contract pricing and should override these defaults.
	"command-a-plus-05-2026":             0,
	"command-a-translate-08-2025":        0,
	"command-a-reasoning-08-2025":        0,
	"command-a-vision-07-2025":           0,
	"north-mini-code-1-0":                0,
	"command-a-03-2025":                  1.25,
	"command-r7b-12-2024":                0.01875,
	"command-r-08-2024":                  0.075,
	"command-r-plus-08-2024":             1.25,
	"c4ai-aya-expanse-32b":               0.25,
	"deepseek-chat":                      0.14 / 2,
	"deepseek-coder":                     0.27 / 2,
	"deepseek-reasoner":                  0.14 / 2,
	"deepseek-v4-flash":                  0.14 / 2,
	"deepseek-v4-pro":                    0.435 / 2,
	"jina-embeddings-v2-base-en":         0.05 / 2,
	"jina-embeddings-v2-base-zh":         0.05 / 2,
	"jina-embeddings-v2-base-de":         0.05 / 2,
	"jina-embeddings-v2-base-es":         0.05 / 2,
	"jina-embeddings-v2-base-code":       0.05 / 2,
	"jina-embeddings-v3":                 0.05 / 2,
	"jina-embeddings-v4":                 0.05 / 2,
	"jina-embeddings-v5-text-nano":       0.02 / 2,
	"jina-embeddings-v5-text-small":      0.05 / 2,
	"jina-embeddings-v5-omni-nano":       0.02 / 2,
	"jina-embeddings-v5-omni-small":      0.05 / 2,
	"jina-code-embeddings-0.5b":          0.05 / 2,
	"jina-code-embeddings-1.5b":          0.05 / 2,
	"jina-clip-v1":                       0.05 / 2,
	"jina-clip-v2":                       0.05 / 2,
	"jina-colbert-v1-en":                 0.05 / 2,
	"jina-colbert-v2":                    0.05 / 2,
	"elser-v2":                           0.05 / 2,
	"jina-reranker-v1-tiny-en":           0.05 / 2,
	"jina-reranker-v1-turbo-en":          0.05 / 2,
	"jina-reranker-v1-base-en":           0.05 / 2,
	"jina-reranker-v2-base-multilingual": 0.05 / 2,
	"jina-reranker-m0":                   0.05 / 2,
	"jina-reranker-v3":                   0.05 / 2,
	"jina-reranker-v3.5":                 0.05 / 2,
	// Perplexity Sonar token prices. Per-request and research/search-query
	// charges are reserved separately and reconciled from usage.cost.
	"sonar":               0.5,
	"sonar-pro":           1.5,
	"sonar-reasoning-pro": 1,
	"sonar-deep-research": 1,
	// Current xAI text models. Priority and prompts over 200K tokens are
	// dynamic multipliers applied only on first-party xAI channels.
	"grok-4.5":                       1,
	"grok-4.5-latest":                1,
	"grok-4.3":                       0.625,
	"grok-4.3-latest":                0.625,
	"grok-latest":                    0.625,
	"grok-4.20-0309-reasoning":       0.625,
	"grok-4.20-0309":                 0.625,
	"grok-4.20-reasoning":            0.625,
	"grok-4.20-reasoning-latest":     0.625,
	"grok-4.20":                      0.625,
	"grok-4.20-0309-non-reasoning":   0.625,
	"grok-4.20-non-reasoning":        0.625,
	"grok-4.20-non-reasoning-latest": 0.625,
	"grok-4.20-multi-agent-0309":     0.625,
	"grok-4.20-multi-agent":          0.625,
	"grok-4.20-multi-agent-latest":   0.625,
	"grok-build-0.1":                 0.5,
	"grok-build-latest":              0.5,
	"grok-code-fast-1":               0.5,
	"grok-code-fast":                 0.5,
	"grok-code-fast-1-0825":          0.5,
	// submodel
	"NousResearch/Hermes-4-405B-FP8":          0.8,
	"Qwen/Qwen3-235B-A22B-Thinking-2507":      0.6,
	"Qwen/Qwen3-Coder-480B-A35B-Instruct-FP8": 0.8,
	"Qwen/Qwen3-235B-A22B-Instruct-2507":      0.3,
	"zai-org/GLM-4.5-FP8":                     0.8,
	"openai/gpt-oss-120b":                     0.5,
	"deepseek-ai/DeepSeek-R1-0528":            0.8,
	"deepseek-ai/DeepSeek-R1":                 0.8,
	"deepseek-ai/DeepSeek-V3-0324":            0.8,
	"deepseek-ai/DeepSeek-V3.1":               0.8,
}

var defaultModelPrice = map[string]float64{
	// Cohere Rerank prices are USD per search unit. Cohere defines the public
	// rates per 1,000 searches; settlement multiplies these values by the
	// provider-reported billed_units.search_units.
	"rerank-v4.0-fast":               2.0 / 1000,
	"rerank-v4.0-pro":                2.5 / 1000,
	"grok-imagine-image-quality":     0.05,
	"grok-imagine-image":             0.02,
	"suno_music":                     0.1,
	"suno_lyrics":                    0.01,
	"dall-e-3":                       0.04,
	"dall-e-2":                       0.02,
	"imagen-3.0-generate-002":        0.03,
	"imagen-4.0-fast-generate-001":   0.02,
	"imagen-4.0-generate-001":        0.04,
	"imagen-4.0-ultra-generate-001":  0.06,
	"black-forest-labs/flux-1.1-pro": 0.04,
	"gpt-4-gizmo-*":                  0.1,
	"mj_video":                       0.8,
	"mj_imagine":                     0.1,
	"mj_edits":                       0.1,
	"mj_variation":                   0.1,
	"mj_reroll":                      0.1,
	"mj_blend":                       0.1,
	"mj_modal":                       0.1,
	"mj_zoom":                        0.1,
	"mj_shorten":                     0.1,
	"mj_high_variation":              0.1,
	"mj_low_variation":               0.1,
	"mj_pan":                         0.1,
	"mj_inpaint":                     0,
	"mj_custom_zoom":                 0,
	"mj_describe":                    0.05,
	"mj_upscale":                     0.05,
	"swap_face":                      0.05,
	"mj_upload":                      0.05,
	"sora-2":                         0.3,
	"sora-2-pro":                     0.5,
	"veo-3.0-generate-001":           0.4,
	"veo-3.0-fast-generate-001":      0.15,
	"veo-3.1-generate-001":           0.4,
	"veo-3.1-fast-generate-001":      0.1,
	"veo-3.1-lite-generate-001":      0.05,
	"veo-3.1-generate-preview":       0.4,
	"veo-3.1-fast-generate-preview":  0.1,
	"veo-3.1-lite-generate-preview":  0.05,
}

var defaultAudioRatio = map[string]float64{
	"gpt-4o-audio-preview":                    16,
	"gpt-4o-audio-preview-2024-10-01":         16,
	"gpt-4o-audio-preview-2024-12-17":         16,
	"gpt-4o-audio-preview-2025-06-03":         16,
	"gpt-4o-mini-audio-preview":               16.6666666667,
	"gpt-4o-mini-audio-preview-2024-12-17":    16.6666666667,
	"gpt-4o-realtime-preview":                 8,
	"gpt-4o-realtime-preview-2024-10-01":      8,
	"gpt-4o-realtime-preview-2024-12-17":      8,
	"gpt-4o-realtime-preview-2025-06-03":      8,
	"gpt-4o-mini-realtime-preview":            16.67,
	"gpt-4o-mini-realtime-preview-2024-12-17": 16.67,
	"gpt-4o-mini-tts":                         1,
	"gpt-4o-mini-tts-2025-03-20":              1,
	"gpt-4o-mini-tts-2025-12-15":              1,
	"gpt-audio":                               16,
	"gpt-audio-2025-08-28":                    16,
	"gpt-audio-1.5":                           12.8,
	"gpt-audio-mini":                          16.6666666667,
	"gpt-audio-mini-2025-10-06":               16.6666666667,
	"gpt-audio-mini-2025-12-15":               16.6666666667,
	"gpt-realtime":                            8,
	"gpt-realtime-2025-08-28":                 8,
	"gpt-realtime-1.5":                        8,
	"gpt-realtime-mini":                       16.6666666667,
	"gpt-realtime-mini-2025-10-06":            16.6666666667,
	"gpt-realtime-mini-2025-12-15":            16.6666666667,
}

var defaultAudioCompletionRatio = map[string]float64{
	"gpt-4o-realtime":                         2,
	"gpt-4o-mini-realtime":                    2,
	"gpt-4o-audio-preview":                    2,
	"gpt-4o-audio-preview-2024-10-01":         2,
	"gpt-4o-audio-preview-2024-12-17":         2,
	"gpt-4o-audio-preview-2025-06-03":         2,
	"gpt-4o-mini-audio-preview":               2,
	"gpt-4o-mini-audio-preview-2024-12-17":    2,
	"gpt-4o-realtime-preview":                 2,
	"gpt-4o-realtime-preview-2024-10-01":      2,
	"gpt-4o-realtime-preview-2024-12-17":      2,
	"gpt-4o-realtime-preview-2025-06-03":      2,
	"gpt-4o-mini-realtime-preview":            2,
	"gpt-4o-mini-realtime-preview-2024-12-17": 2,
	"gpt-4o-mini-tts":                         20,
	"gpt-4o-mini-tts-2025-03-20":              20,
	"gpt-4o-mini-tts-2025-12-15":              20,
	"gpt-audio":                               2,
	"gpt-audio-2025-08-28":                    2,
	"gpt-audio-1.5":                           2,
	"gpt-audio-mini":                          2,
	"gpt-audio-mini-2025-10-06":               2,
	"gpt-audio-mini-2025-12-15":               2,
	"gpt-realtime":                            2,
	"gpt-realtime-2025-08-28":                 2,
	"gpt-realtime-1.5":                        2,
	"gpt-realtime-mini":                       2,
	"gpt-realtime-mini-2025-10-06":            2,
	"gpt-realtime-mini-2025-12-15":            2,
	"tts-1":                                   0,
	"tts-1-hd":                                0,
	"tts-1-1106":                              0,
	"tts-1-hd-1106":                           0,
}

var modelPriceMap = types.NewRWMap[string, float64]()
var modelRatioMap = types.NewRWMap[string, float64]()
var completionRatioMap = types.NewRWMap[string, float64]()

var defaultCompletionRatio = map[string]float64{
	"c4ai-aya-expanse-32b":                    3,
	"sonar":                                   1,
	"sonar-pro":                               5,
	"sonar-reasoning-pro":                     4,
	"sonar-deep-research":                     4,
	"grok-4.5":                                3,
	"grok-4.5-latest":                         3,
	"grok-4.3":                                2,
	"grok-4.3-latest":                         2,
	"grok-latest":                             2,
	"grok-4.20-0309-reasoning":                2,
	"grok-4.20-0309":                          2,
	"grok-4.20-reasoning":                     2,
	"grok-4.20-reasoning-latest":              2,
	"grok-4.20":                               2,
	"grok-4.20-0309-non-reasoning":            2,
	"grok-4.20-non-reasoning":                 2,
	"grok-4.20-non-reasoning-latest":          2,
	"grok-4.20-multi-agent-0309":              2,
	"grok-4.20-multi-agent":                   2,
	"grok-4.20-multi-agent-latest":            2,
	"grok-build-0.1":                          2,
	"grok-build-latest":                       2,
	"grok-code-fast-1":                        2,
	"grok-code-fast":                          2,
	"grok-code-fast-1-0825":                   2,
	"gpt-4-gizmo-*":                           2,
	"gpt-4o-gizmo-*":                          3,
	"gpt-4-all":                               2,
	"gpt-image-1":                             8,
	"gpt-image-1-mini":                        4,
	"gpt-image-1.5":                           6.4,
	"chatgpt-image-latest":                    6.4,
	"gpt-image-2":                             6,
	"gpt-image-2-2026-04-21":                  6,
	"gpt-5.1":                                 8,
	"gpt-5.1-2025-11-13":                      8,
	"gpt-5.1-chat-latest":                     8,
	"gpt-5.1-codex":                           8,
	"gpt-5.1-codex-mini":                      8,
	"gpt-5.1-codex-max":                       8,
	"gpt-5.2":                                 8,
	"gpt-5.2-2025-12-11":                      8,
	"gpt-5.2-chat-latest":                     8,
	"gpt-5.2-codex":                           8,
	"gpt-5.2-pro":                             8,
	"gpt-5.2-pro-2025-12-11":                  8,
	"gpt-5.3-chat-latest":                     8,
	"gpt-5.3-codex":                           8,
	"gemini-flash-latest":                     5,
	"gemini-flash-lite-latest":                2.5 / 0.3,
	"gemini-pro-latest":                       6,
	"gemini-3.6-flash":                        5,
	"gemini-3.5-flash":                        6,
	"gemini-3.5-flash-lite":                   2.5 / 0.3,
	"gemini-3.1-flash-lite":                   6,
	"gemini-3-flash-preview":                  6,
	"gemini-2.5-flash-image":                  2.5 / 0.3,
	"gemini-3.1-flash-image":                  6,
	"gemini-3.1-flash-lite-image":             6,
	"gemini-3-pro-image":                      6,
	"gemini-2.5-computer-use-preview-10-2025": 8,
	"gemini-2.5-flash-preview-tts":            20,
	"gemini-2.5-pro-preview-tts":              20,
	"gemini-3.1-flash-tts-preview":            20,
	"gemini-3.1-pro-preview":                  6,
	"gemini-3.1-pro-preview-customtools":      6,
	"gemini-robotics-er-1.6-preview":          5,
	"claude-fable-5":                          5,
	"claude-mythos-5":                         5,
	"claude-sonnet-5":                         5,
	"claude-opus-5":                           5,
	"claude-opus-5-max":                       5,
	"claude-opus-5-xhigh":                     5,
	"claude-opus-5-high":                      5,
	"claude-opus-5-medium":                    5,
	"claude-opus-5-low":                       5,
	"claude-opus-5-thinking":                  5,
	"deepseek-chat":                           2,
	"deepseek-reasoner":                       2,
	"deepseek-v4-flash":                       2,
	"deepseek-v4-pro":                         2,
}

// InitRatioSettings initializes all model related settings maps
func InitRatioSettings() {
	modelPriceMap.AddAll(defaultModelPrice)
	modelRatioMap.AddAll(defaultModelRatio)
	completionRatioMap.AddAll(defaultCompletionRatio)
	cacheRatioMap.AddAll(defaultCacheRatio)
	createCacheRatioMap.AddAll(defaultCreateCacheRatio)
	imageRatioMap.AddAll(defaultImageRatio)
	audioRatioMap.AddAll(defaultAudioRatio)
	audioCompletionRatioMap.AddAll(defaultAudioCompletionRatio)
}

func GetModelPriceMap() map[string]float64 {
	return modelPriceMap.ReadAll()
}

func ModelPrice2JSONString() string {
	return modelPriceMap.MarshalJSONString()
}

func UpdateModelPriceByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(modelPriceMap, jsonStr, InvalidateExposedDataCache)
}

// GetModelPrice 返回模型的价格，如果模型不存在则返回-1，false
func GetModelPrice(name string, printErr bool) (float64, bool) {
	name = FormatMatchingModelName(name)

	if price, ok := modelPriceMap.Get(name); ok {
		return price, true
	}

	if strings.HasSuffix(name, CompactModelSuffix) {
		price, ok := modelPriceMap.Get(CompactWildcardModelKey)
		if !ok {
			if printErr {
				common.SysError("model price not found: " + name)
			}
			return -1, false
		}
		return price, true
	}

	if printErr {
		common.SysError("model price not found: " + name)
	}
	return -1, false
}

func UpdateModelRatioByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(modelRatioMap, jsonStr, InvalidateExposedDataCache)
}

// 处理带有思考预算的模型名称，方便统一定价
func handleThinkingBudgetModel(name, prefix, wildcard string) string {
	if strings.HasPrefix(name, prefix) && strings.Contains(name, "-thinking-") {
		return wildcard
	}
	return name
}

func GetModelRatio(name string) (float64, bool, string) {
	name = FormatMatchingModelName(name)

	ratio, ok := modelRatioMap.Get(name)
	if !ok {
		if strings.HasSuffix(name, CompactModelSuffix) {
			if wildcardRatio, ok := modelRatioMap.Get(CompactWildcardModelKey); ok {
				return wildcardRatio, true, name
			}
			//return 0, true, name
		}
		selfUseModeEnabled := common.GetLegacyOptionBool("SelfUseModeEnabled", &operation_setting.SelfUseModeEnabled)
		return 37.5, selfUseModeEnabled, name
	}
	return ratio, true, name
}

func DefaultModelRatio2JSONString() string {
	jsonBytes, err := common.Marshal(defaultModelRatio)
	if err != nil {
		common.SysError("error marshalling model ratio: " + err.Error())
	}
	return string(jsonBytes)
}

func GetDefaultModelRatioMap() map[string]float64 {
	return defaultModelRatio
}

func GetDefaultModelPriceMap() map[string]float64 {
	return defaultModelPrice
}

func GetDefaultCompletionRatio(name string) float64 {
	name = FormatMatchingModelName(name)
	hardCodedRatio, locked := getHardcodedCompletionModelRatio(name)
	if locked {
		return hardCodedRatio
	}
	if ratio, ok := defaultCompletionRatio[name]; ok {
		return ratio
	}
	return hardCodedRatio
}

func CompletionRatio2JSONString() string {
	return completionRatioMap.MarshalJSONString()
}

func UpdateCompletionRatioByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(completionRatioMap, jsonStr, InvalidateExposedDataCache)
}

func GetCompletionRatio(name string) float64 {
	name = FormatMatchingModelName(name)

	if strings.Contains(name, "/") {
		if ratio, ok := completionRatioMap.Get(name); ok {
			return ratio
		}
	}
	hardCodedRatio, contain := getHardcodedCompletionModelRatio(name)
	if contain {
		return hardCodedRatio
	}
	if ratio, ok := completionRatioMap.Get(name); ok {
		return ratio
	}
	return hardCodedRatio
}

type CompletionRatioInfo struct {
	Ratio  float64 `json:"ratio"`
	Locked bool    `json:"locked"`
}

func GetCompletionRatioInfo(name string) CompletionRatioInfo {
	name = FormatMatchingModelName(name)

	if strings.Contains(name, "/") {
		if ratio, ok := completionRatioMap.Get(name); ok {
			return CompletionRatioInfo{
				Ratio:  ratio,
				Locked: false,
			}
		}
	}

	hardCodedRatio, locked := getHardcodedCompletionModelRatio(name)
	if locked {
		return CompletionRatioInfo{
			Ratio:  hardCodedRatio,
			Locked: true,
		}
	}

	if ratio, ok := completionRatioMap.Get(name); ok {
		return CompletionRatioInfo{
			Ratio:  ratio,
			Locked: false,
		}
	}

	return CompletionRatioInfo{
		Ratio:  hardCodedRatio,
		Locked: false,
	}
}

func getHardcodedCompletionModelRatio(name string) (float64, bool) {

	isReservedModel := strings.HasSuffix(name, "-all") || strings.HasSuffix(name, "-gizmo-*")
	if isReservedModel {
		return 2, false
	}

	if strings.HasPrefix(name, "gpt-") {
		if strings.HasPrefix(name, "gpt-4o") {
			if name == "gpt-4o-2024-05-13" {
				return 3, true
			}
			if strings.HasPrefix(name, "gpt-4o-mini-tts") {
				return 20, false
			}
			return 4, false
		}
		// gpt-5 匹配
		if strings.HasPrefix(name, "gpt-5") {
			if !strings.Contains(name, ".") {
				return 8, true
			}
			if strings.HasPrefix(name, "gpt-5.4") {
				if strings.HasPrefix(name, "gpt-5.4-nano") {
					return 6.25, true
				}
				return 6, true
			}
			// gpt-5.5 and later models are unlocked
			return 6, false
		}
		// gpt-4.5-preview匹配
		if strings.HasPrefix(name, "gpt-4.5-preview") {
			return 2, true
		}
		if strings.HasPrefix(name, "gpt-4-turbo") || strings.HasSuffix(name, "gpt-4-1106") || strings.HasSuffix(name, "gpt-4-1105") {
			return 3, true
		}
		// 没有特殊标记的 gpt-4 模型默认倍率为 2
		return 2, false
	}
	if strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3") {
		return 4, true
	}
	if name == "chatgpt-4o-latest" {
		return 3, true
	}

	if strings.Contains(name, "claude-3") {
		return 5, true
	} else if strings.Contains(name, "claude-sonnet-4") || strings.Contains(name, "claude-opus-4") || strings.Contains(name, "claude-haiku-4") {
		return 5, true
	}

	if strings.HasPrefix(name, "gpt-3.5") {
		if name == "gpt-3.5-turbo" || strings.HasSuffix(name, "0125") {
			// https://openai.com/blog/new-embedding-models-and-api-updates
			// Updated GPT-3.5 Turbo model and lower pricing
			return 3, true
		}
		if strings.HasSuffix(name, "1106") {
			return 2, true
		}
		return 4.0 / 3.0, true
	}
	if strings.HasPrefix(name, "mistral-") {
		return 3, true
	}
	if strings.HasPrefix(name, "gemini-") {
		if strings.HasPrefix(name, "gemini-1.5") {
			return 4, true
		} else if strings.HasPrefix(name, "gemini-2.0") {
			return 4, true
		} else if strings.HasPrefix(name, "gemini-2.5-pro") { // 移除preview来增加兼容性，这里假设正式版的倍率和preview一致
			return 8, false
		} else if strings.HasPrefix(name, "gemini-2.5-flash") { // 处理不同的flash模型倍率
			if strings.HasPrefix(name, "gemini-2.5-flash-preview") {
				if strings.HasSuffix(name, "-nothinking") {
					return 4, false
				}
				return 3.5 / 0.15, false
			}
			if strings.HasPrefix(name, "gemini-2.5-flash-lite") {
				return 4, false
			}
			return 2.5 / 0.3, false
		} else if strings.HasPrefix(name, "gemini-robotics-er-1.5") {
			return 2.5 / 0.3, false
		} else if strings.HasPrefix(name, "gemini-3-pro") {
			if strings.HasPrefix(name, "gemini-3-pro-image") {
				return 60, false
			}
			return 6, false
		}
		return 4, false
	}
	if strings.HasPrefix(name, "command") {
		switch name {
		case "command-r":
			return 3, true
		case "command-r-plus":
			return 5, true
		case "command-r-08-2024":
			return 4, true
		case "command-r-plus-08-2024":
			return 4, true
		default:
			return 4, false
		}
	}
	// hint 只给官方上4倍率，由于开源模型供应商自行定价，不对其进行补全倍率进行强制对齐
	if strings.HasPrefix(name, "ERNIE-Speed-") {
		return 2, true
	} else if strings.HasPrefix(name, "ERNIE-Lite-") {
		return 2, true
	} else if strings.HasPrefix(name, "ERNIE-Character") {
		return 2, true
	} else if strings.HasPrefix(name, "ERNIE-Functions") {
		return 2, true
	}
	switch name {
	case "llama2-70b-4096":
		return 0.8 / 0.64, true
	case "llama3-8b-8192":
		return 2, true
	case "llama3-70b-8192":
		return 0.79 / 0.59, true
	}
	return 1, false
}

func GetAudioRatio(name string) float64 {
	name = FormatMatchingModelName(name)
	if ratio, ok := audioRatioMap.Get(name); ok {
		return ratio
	}
	return 1
}

func GetAudioCompletionRatio(name string) float64 {
	name = FormatMatchingModelName(name)
	if ratio, ok := audioCompletionRatioMap.Get(name); ok {
		return ratio
	}
	return 1
}

func ContainsAudioRatio(name string) bool {
	name = FormatMatchingModelName(name)
	_, ok := audioRatioMap.Get(name)
	return ok
}

func ContainsAudioCompletionRatio(name string) bool {
	name = FormatMatchingModelName(name)
	_, ok := audioCompletionRatioMap.Get(name)
	return ok
}

func ModelRatio2JSONString() string {
	return modelRatioMap.MarshalJSONString()
}

var defaultImageRatio = map[string]float64{
	"gpt-image-1":            2,
	"gpt-image-1-mini":       1.25,
	"gpt-image-1.5":          1.6,
	"chatgpt-image-latest":   1.6,
	"gpt-image-2":            1.6,
	"gpt-image-2-2026-04-21": 1.6,
}
var imageRatioMap = types.NewRWMap[string, float64]()
var audioRatioMap = types.NewRWMap[string, float64]()
var audioCompletionRatioMap = types.NewRWMap[string, float64]()

func ImageRatio2JSONString() string {
	return imageRatioMap.MarshalJSONString()
}

func UpdateImageRatioByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonString(imageRatioMap, jsonStr)
}

func GetImageRatio(name string) (float64, bool) {
	ratio, ok := imageRatioMap.Get(name)
	if !ok {
		return 1, false // Default to 1 if not found
	}
	return ratio, true
}

func AudioRatio2JSONString() string {
	return audioRatioMap.MarshalJSONString()
}

func UpdateAudioRatioByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(audioRatioMap, jsonStr, InvalidateExposedDataCache)
}

func AudioCompletionRatio2JSONString() string {
	return audioCompletionRatioMap.MarshalJSONString()
}

func UpdateAudioCompletionRatioByJSONString(jsonStr string) error {
	if err := CheckRatioMap(jsonStr); err != nil {
		return err
	}
	return types.LoadFromJsonStringWithCallback(audioCompletionRatioMap, jsonStr, InvalidateExposedDataCache)
}

func GetModelRatioCopy() map[string]float64 {
	return modelRatioMap.ReadAll()
}

func GetModelPriceCopy() map[string]float64 {
	return modelPriceMap.ReadAll()
}

func GetCompletionRatioCopy() map[string]float64 {
	return completionRatioMap.ReadAll()
}

func GetImageRatioCopy() map[string]float64 {
	return imageRatioMap.ReadAll()
}

func GetAudioRatioCopy() map[string]float64 {
	return audioRatioMap.ReadAll()
}

func GetAudioCompletionRatioCopy() map[string]float64 {
	return audioCompletionRatioMap.ReadAll()
}

// 转换模型名，减少渠道必须配置各种带参数模型
func FormatMatchingModelName(name string) string {
	name = reasoning.NormalizeDeepSeekV4PricingModel(name)

	if strings.HasPrefix(name, "gemini-2.5-flash-lite") {
		name = handleThinkingBudgetModel(name, "gemini-2.5-flash-lite", "gemini-2.5-flash-lite-thinking-*")
	} else if strings.HasPrefix(name, "gemini-2.5-flash") {
		name = handleThinkingBudgetModel(name, "gemini-2.5-flash", "gemini-2.5-flash-thinking-*")
	} else if strings.HasPrefix(name, "gemini-2.5-pro") {
		name = handleThinkingBudgetModel(name, "gemini-2.5-pro", "gemini-2.5-pro-thinking-*")
	}

	if strings.HasPrefix(name, "gpt-4-gizmo") {
		name = "gpt-4-gizmo-*"
	}
	if strings.HasPrefix(name, "gpt-4o-gizmo") {
		name = "gpt-4o-gizmo-*"
	}
	return name
}

// result: 倍率or价格， usePrice， exist
func GetModelRatioOrPrice(model string) (float64, bool, bool) { // price or ratio
	price, usePrice := GetModelPrice(model, false)
	if usePrice {
		return price, true, true
	}
	modelRatio, success, _ := GetModelRatio(model)
	if success {
		return modelRatio, false, true
	}
	return 37.5, false, false
}
