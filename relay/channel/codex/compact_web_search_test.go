package codex

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	relayconstant "github.com/QuantumNous/new-api/relay/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestRewriteCompactWebSearch(t *testing.T) {
	const searched = `{"type":"web_search_call","id":"ws_history","action":{"type":"search","query":"Go release"}}`
	const base = `{"model":"gpt-6.1-sol","input":[` + searched + `],"tools":[],"tool_choice":"auto","unknown":{"keep":1234567890123456789}}`
	tests := []struct {
		name, body, metadata, lite, declaration, choice string
		mode, channel                                   int
		disabled, unchanged, wantErr                    bool
	}{
		{name: "dedicated compact", body: base, mode: relayconstant.RelayModeResponsesCompact, declaration: "tools.0", choice: "none"},
		{name: "metadata compact", body: base, metadata: `{"request_kind":"compaction"}`, declaration: "tools.0", choice: "none"},
		{name: "ordinary turn", body: base, metadata: `{"request_kind":"regular"}`, unchanged: true},
		{name: "no compact marker", body: base, unchanged: true},
		{name: "disabled channel", body: base, mode: relayconstant.RelayModeResponsesCompact, disabled: true, unchanged: true},
		{name: "other provider", body: base, mode: relayconstant.RelayModeResponsesCompact, channel: 1, unchanged: true},
		{name: "no search history", body: `{"input":[{"type":"message","role":"user","content":"web_search_call"}],"tools":[]}`, mode: relayconstant.RelayModeResponsesCompact, unchanged: true},
		{name: "existing search", body: `{"input":[` + searched + `],"tools":[{"type":"web_search_preview","external_web_access":true}]}`, mode: relayconstant.RelayModeResponsesCompact, unchanged: true},
		{name: "existing additional search", body: `{"input":[{"type":"additional_tools","tools":[{"type":"web_search"}]},` + searched + `]}`, mode: relayconstant.RelayModeResponsesCompact, unchanged: true},
		{name: "preserve tools and choice", body: `{"input":[` + searched + `],"tools":[{"type":"function","name":"shell"}],"tool_choice":"required"}`, mode: relayconstant.RelayModeResponsesCompact, declaration: "tools.1", choice: "required"},
		{name: "lite header", body: base, metadata: `{"request_kind":"compaction"}`, lite: "true", declaration: "input.0.tools.0", choice: "none"},
		{name: "lite reasoning and null tools", body: `{"input":[` + searched + `],"tools":null,"tool_choice":null,"reasoning":{"context":"all_turns"}}`, mode: relayconstant.RelayModeResponsesCompact, declaration: "input.0.tools.0", choice: "none"},
		{name: "additional tools preserve last trigger", body: `{"input":[{"type":"additional_tools","role":"developer","tools":[{"type":"function","name":"shell"}]},` + searched + `,{"type":"compaction_trigger"}],"tool_choice":"auto"}`, declaration: "input.0.tools.1", choice: "auto"},
		{name: "prepend before terminal trigger", body: `{"input":[` + searched + `,{"type":"compaction_trigger"}],"tools":[]}`, lite: "true", declaration: "input.0.tools.0", choice: "none"},
		{name: "invalid tools fail loudly", body: `{"input":[` + searched + `],"tools":{}}`, mode: relayconstant.RelayModeResponsesCompact, wantErr: true},
		{name: "invalid metadata fail loudly", body: base, metadata: `{`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
			c.Request.Header.Set("X-Codex-Turn-Metadata", tt.metadata)
			c.Request.Header.Set("X-OpenAI-Internal-Codex-Responses-Lite", tt.lite)
			channel := tt.channel
			if channel == 0 {
				channel = constant.ChannelTypeCodex
			}
			mode := tt.mode
			if mode == 0 {
				mode = relayconstant.RelayModeResponses
			}
			info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{ChannelId: 79, ChannelType: channel, ChannelSetting: dto.ChannelSettings{CodexCompactWebSearch: !tt.disabled}}, RelayMode: mode}
			out, err := RewriteCompactWebSearch(c, info, []byte(tt.body))
			if tt.wantErr {
				require.Error(t, err)
				assert.Empty(t, service.RequestPolicy(c).Events())
				return
			}
			require.NoError(t, err)
			if tt.unchanged {
				assert.Equal(t, tt.body, string(out))
				assert.Empty(t, service.RequestPolicy(c).Events())
				return
			}
			assert.Equal(t, "web_search", gjson.GetBytes(out, tt.declaration+".type").String())
			assert.False(t, gjson.GetBytes(out, tt.declaration+".external_web_access").Bool())
			assert.Equal(t, tt.choice, gjson.GetBytes(out, "tool_choice").String())
			if gjson.Get(tt.body, "unknown").Exists() {
				assert.Equal(t, gjson.Get(tt.body, "unknown").Raw, gjson.GetBytes(out, "unknown").Raw)
			}
			items := gjson.GetBytes(out, "input").Array()
			before := gjson.Get(tt.body, "input").Array()
			if before[len(before)-1].Get("type").String() == "compaction_trigger" {
				assert.Equal(t, "compaction_trigger", items[len(items)-1].Get("type").String())
			}
			events := service.RequestPolicy(c).Events()
			require.Len(t, events, 1)
			assert.Equal(t, "rewrite", events[0].Decision.Action)
			assert.Equal(t, 79, events[0].ChannelID)
			again, err := RewriteCompactWebSearch(c, info, out)
			require.NoError(t, err)
			assert.Equal(t, string(out), string(again))
			assert.Len(t, service.RequestPolicy(c).Events(), 1)
		})
	}
}
