package operation_setting

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/setting/config"
	"golang.org/x/net/http/httpguts"
)

const maxChannelAffinityEntries = 1_000_000

type ChannelAffinityKeySource struct {
	Type string `json:"type"` // context_int, context_string, request_header, gjson
	Key  string `json:"key,omitempty"`
	Path string `json:"path,omitempty"`
}

type ChannelAffinityRule struct {
	Name             string                     `json:"name"`
	ModelRegex       []string                   `json:"model_regex"`
	PathRegex        []string                   `json:"path_regex"`
	UserAgentInclude []string                   `json:"user_agent_include,omitempty"`
	KeySources       []ChannelAffinityKeySource `json:"key_sources"`

	ValueRegex string `json:"value_regex"`
	TTLSeconds int    `json:"ttl_seconds"`

	ParamOverrideTemplate map[string]interface{} `json:"param_override_template,omitempty"`

	SkipRetryOnFailure bool `json:"skip_retry_on_failure"`

	IncludeUsingGroup bool `json:"include_using_group"`
	IncludeModelName  bool `json:"include_model_name"`
	IncludeRuleName   bool `json:"include_rule_name"`
}

type ChannelAffinitySetting struct {
	Enabled               bool                  `json:"enabled"`
	SwitchOnSuccess       bool                  `json:"switch_on_success"`
	KeepOnChannelDisabled bool                  `json:"keep_on_channel_disabled"`
	MaxEntries            int                   `json:"max_entries"`
	DefaultTTLSeconds     int                   `json:"default_ttl_seconds"`
	Rules                 []ChannelAffinityRule `json:"rules"`
}

// Keep Codex CLI passthrough aligned with upstream. Codex uses lower-case
// header names, while HTTP matching here is case-insensitive.
// Request session/thread headers:
// https://github.com/openai/codex/commit/7c7b4861d88960f7e3bd5b7f30f8351be666dd84
// Responses metadata headers/client_metadata:
// https://github.com/openai/codex/commit/14df0e8833aad0d6d78287954b61ffac67af936c
// x-codex-turn-state response/request round trip:
// https://github.com/openai/codex/commit/ebdd8795e924a8149b616e46ca2ed7848c207a4b
var codexCliPassThroughHeaders = []string{
	"Originator",
	"Session_id",
	"Thread_id",
	"Session-Id",
	"Thread-Id",
	"X-Client-Request-Id",
	"User-Agent",
	"X-Codex-Beta-Features",
	"X-Codex-Turn-State",
	"X-Codex-Turn-Metadata",
	"X-Codex-Window-Id",
	"X-Codex-Parent-Thread-Id",
	//"X-Codex-Installation-Id",
	"X-OpenAI-Subagent",
	"X-OpenAI-Memgen-Request",
	//"X-OAI-Attestation",
	"X-ResponsesAPI-Include-Timing-Metrics",
	"X-OpenAI-Internal-Codex-Responses-Lite",
}

var claudeCliPassThroughHeaders = []string{
	"X-Stainless-Arch",
	"X-Stainless-Lang",
	"X-Stainless-Os",
	"X-Stainless-Package-Version",
	"X-Stainless-Retry-Count",
	"X-Stainless-Runtime",
	"X-Stainless-Runtime-Version",
	"X-Stainless-Timeout",
	"User-Agent",
	"X-App",
	"Anthropic-Beta",
	"Anthropic-Dangerous-Direct-Browser-Access",
	"Anthropic-Version",
}

func buildPassHeaderTemplate(headers []string) map[string]interface{} {
	clonedHeaders := make([]string, 0, len(headers))
	clonedHeaders = append(clonedHeaders, headers...)
	return map[string]interface{}{
		"operations": []map[string]interface{}{
			{
				"mode":        "pass_headers",
				"value":       clonedHeaders,
				"keep_origin": true,
			},
		},
	}
}

func buildCodexPassHeaderTemplate() map[string]interface{} {
	requestHeaders := make([]string, 0, len(codexCliPassThroughHeaders))
	requestHeaders = append(requestHeaders, codexCliPassThroughHeaders...)
	return map[string]interface{}{
		"operations": []map[string]interface{}{
			{
				"mode":        "pass_headers",
				"value":       requestHeaders,
				"keep_origin": true,
			},
		},
	}
}

var channelAffinitySetting = ChannelAffinitySetting{
	Enabled:               true,
	SwitchOnSuccess:       true,
	KeepOnChannelDisabled: false,
	MaxEntries:            100_000,
	DefaultTTLSeconds:     3600,
	Rules: []ChannelAffinityRule{
		{
			Name:       "codex cli trace",
			ModelRegex: []string{"^gpt-.*$"},
			PathRegex:  []string{"/v1/responses"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "prompt_cache_key"},
			},
			ValueRegex:            "",
			TTLSeconds:            0,
			ParamOverrideTemplate: buildCodexPassHeaderTemplate(),
			SkipRetryOnFailure:    true,
			IncludeUsingGroup:     true,
			IncludeRuleName:       true,
			UserAgentInclude:      nil,
		},
		{
			Name:       "claude cli trace",
			ModelRegex: []string{"^claude-.*$"},
			PathRegex:  []string{"/v1/messages"},
			KeySources: []ChannelAffinityKeySource{
				{Type: "gjson", Path: "metadata.user_id"},
			},
			ValueRegex:            "",
			TTLSeconds:            0,
			ParamOverrideTemplate: buildPassHeaderTemplate(claudeCliPassThroughHeaders),
			SkipRetryOnFailure:    true,
			IncludeUsingGroup:     true,
			IncludeRuleName:       true,
			UserAgentInclude:      nil,
		},
	},
}

func (s ChannelAffinitySetting) Validate() error {
	if s.MaxEntries < 1 || s.MaxEntries > maxChannelAffinityEntries {
		return fmt.Errorf("channel affinity maximum entries must be in the range [1, %d]", maxChannelAffinityEntries)
	}
	maxTTLSeconds := int64(math.MaxInt64 / int64(time.Second))
	if s.DefaultTTLSeconds < 1 || int64(s.DefaultTTLSeconds) > maxTTLSeconds {
		return fmt.Errorf("channel affinity default TTL must be in the range [1, %d] seconds", maxTTLSeconds)
	}
	if len(s.Rules) > 10_000 {
		return fmt.Errorf("channel affinity cannot contain more than 10000 rules")
	}
	ruleNames := make(map[string]struct{}, len(s.Rules))
	for index, rule := range s.Rules {
		name := strings.TrimSpace(rule.Name)
		if name == "" {
			return fmt.Errorf("channel affinity rule %d must have a name", index+1)
		}
		if _, exists := ruleNames[name]; exists {
			return fmt.Errorf("channel affinity rule name %q is duplicated", name)
		}
		ruleNames[name] = struct{}{}
		if rule.TTLSeconds < 0 || int64(rule.TTLSeconds) > maxTTLSeconds {
			return fmt.Errorf("channel affinity rule %q TTL must be in the range [0, %d] seconds", name, maxTTLSeconds)
		}
		if len(rule.KeySources) == 0 {
			return fmt.Errorf("channel affinity rule %q must have at least one key source", name)
		}
		for _, pattern := range append(append([]string(nil), rule.ModelRegex...), rule.PathRegex...) {
			if _, err := regexp.Compile(pattern); err != nil {
				return fmt.Errorf("channel affinity rule %q contains invalid regex %q: %w", name, pattern, err)
			}
		}
		if rule.ValueRegex != "" {
			if _, err := regexp.Compile(rule.ValueRegex); err != nil {
				return fmt.Errorf("channel affinity rule %q contains invalid value regex: %w", name, err)
			}
		}
		for _, source := range rule.KeySources {
			switch source.Type {
			case "context_int", "context_string":
				if strings.TrimSpace(source.Key) == "" {
					return fmt.Errorf("channel affinity rule %q source %q requires a key", name, source.Type)
				}
			case "request_header":
				if !httpguts.ValidHeaderFieldName(source.Key) {
					return fmt.Errorf("channel affinity rule %q contains invalid request header %q", name, source.Key)
				}
			case "gjson":
				if strings.TrimSpace(source.Path) == "" {
					return fmt.Errorf("channel affinity rule %q gjson source requires a path", name)
				}
			default:
				return fmt.Errorf("channel affinity rule %q contains unsupported key source %q", name, source.Type)
			}
		}
	}
	return nil
}

func init() {
	config.GlobalConfig.Register("channel_affinity_setting", &channelAffinitySetting)
}

func GetChannelAffinitySetting() *ChannelAffinitySetting {
	return config.Snapshot[ChannelAffinitySetting]("channel_affinity_setting")
}
