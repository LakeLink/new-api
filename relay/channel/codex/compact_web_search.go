package codex

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// RewriteCompactWebSearch restores a missing cached search declaration only for
// explicitly identified Codex compaction requests. Ordinary turns are untouched.
// Apply it to the final JSON, after parameter overrides.
func RewriteCompactWebSearch(c *gin.Context, info *relaycommon.RelayInfo, body []byte) ([]byte, error) {
	if info.ChannelType != constant.ChannelTypeCodex || !info.ChannelSetting.CodexCompactWebSearch {
		return body, nil
	}
	compact := info.RelayMode == relayconstant.RelayModeResponsesCompact
	if !compact && info.RelayMode != relayconstant.RelayModeResponses {
		return body, nil
	}
	if !compact && c != nil {
		if header := c.GetHeader("X-Codex-Turn-Metadata"); header != "" {
			var metadata struct {
				RequestKind string `json:"request_kind"`
			}
			if err := common.UnmarshalJsonStr(header, &metadata); err != nil {
				return nil, fmt.Errorf("invalid Codex turn metadata: %w", err)
			}
			compact = metadata.RequestKind == "compaction"
		}
	}
	input := gjson.GetBytes(body, "input")
	if !input.IsArray() {
		return body, nil
	}
	items := input.Array()
	searched, offered := false, false
	additional := -1
	for i, item := range items {
		switch item.Get("type").String() {
		case "compaction_trigger":
			compact = true
		case "web_search_call":
			searched = true
		case "additional_tools":
			tools := item.Get("tools")
			if declaresWebSearch(tools) {
				return body, nil
			}
			offered = offered || len(tools.Array()) > 0
			if additional < 0 {
				additional = i
			}
		}
	}
	if !compact || !searched {
		return body, nil
	}
	tools := gjson.GetBytes(body, "tools")
	if tools.Type != gjson.Null && !tools.IsArray() {
		return nil, fmt.Errorf("Codex compact tools must be an array")
	}
	if declaresWebSearch(tools) {
		return body, nil
	}
	offered = offered || len(tools.Array()) > 0
	search := map[string]any{"type": "web_search", "external_web_access": false}
	lite := additional >= 0 || gjson.GetBytes(body, "reasoning.context").String() == "all_turns"
	if c != nil {
		lite = lite || strings.EqualFold(c.GetHeader("X-OpenAI-Internal-Codex-Responses-Lite"), "true")
	}
	var err error
	switch {
	case additional >= 0:
		path := "input." + strconv.Itoa(additional) + ".tools"
		if !gjson.GetBytes(body, path).IsArray() {
			return nil, fmt.Errorf("Codex compact additional_tools.tools must be an array")
		}
		body, err = sjson.SetBytes(body, path+".-1", search)
	case lite:
		// Prepend the declaration so a terminal compaction_trigger stays last.
		declaration, marshalErr := common.Marshal(map[string]any{"type": "additional_tools", "role": "developer", "tools": []any{search}})
		if marshalErr != nil {
			return nil, marshalErr
		}
		newInput := "[" + string(declaration) + "," + strings.TrimSpace(input.Raw)[1:]
		body, err = sjson.SetRawBytes(body, "input", []byte(newInput))
	default:
		body, err = sjson.SetBytes(body, "tools.-1", search)
	}
	if err != nil {
		return nil, err
	}
	if !offered {
		choice := gjson.GetBytes(body, "tool_choice")
		if choice.Type == gjson.Null || choice.Type == gjson.String && (choice.String() == "auto" || choice.String() == "" || choice.String() == "none") {
			body, err = sjson.SetBytes(body, "tool_choice", "none")
			if err != nil {
				return nil, err
			}
		}
	}
	reason := "codex_compact_web_search_declared"
	if lite {
		reason = "codex_compact_web_search_declared_lite"
	}
	service.RequestPolicy(c).AddEvent(service.PolicyEvent{
		ChannelID: info.ChannelId,
		Decision:  service.PolicyDecision{Action: "rewrite", Reason: reason, Source: "channel"},
	})
	return body, nil
}

func declaresWebSearch(tools gjson.Result) bool {
	for _, tool := range tools.Array() {
		if strings.HasPrefix(tool.Get("type").String(), "web_search") {
			return true
		}
	}
	return false
}
