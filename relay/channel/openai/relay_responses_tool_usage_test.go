package openai

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOaiResponsesHandlerCountsActualBuiltInOutputCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	toolTypes := []string{
		dto.BuildInToolWebSearch,
		dto.BuildInToolWebSearch20250826,
		dto.BuildInToolWebSearchPreview,
		dto.BuildInToolWebSearchPreview20250311,
	}
	for _, toolType := range toolTypes {
		t.Run(toolType, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			body := fmt.Sprintf(`{
		"tools":[
			{"type":%q},
			{"type":"file_search"},
			{"type":"image_generation"},
			{"type":"function","name":"lookup"}
		],
		"output":[
			{"type":"web_search_call","id":"ws_1"},
			{"type":"file_search_call","id":"fs_1"},
			{"type":"function_call","id":"fn_1"},
			{"type":"web_search_call","id":"ws_2"},
			{"type":"file_search_call","id":"fs_2"},
			{"type":"image_generation_call","id":"ig_1","quality":"low","size":"1024x1024"},
			{"type":"image_generation_call","id":"ig_2","quality":"low","size":"1024x1024"}
		],
		"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}
	}`, toolType)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     http.Header{"Content-Type": []string{"application/json"}},
			}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{},
				ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
					toolType:                  {ToolName: toolType},
					dto.BuildInToolFileSearch: {ToolName: dto.BuildInToolFileSearch},
					"image_generation":        {ToolName: "image_generation"},
					"function":                {ToolName: "function"},
				}},
			}

			_, apiErr := OaiResponsesHandler(c, info, resp)

			require.Nil(t, apiErr)
			assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools[toolType].CallCount)
			assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch].CallCount)
			assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools["image_generation"].CallCount)
			assert.Len(t, info.ResponsesUsageInfo.BuiltInTools["image_generation"].ImageOutputs, 2)
			assert.Zero(t, info.ResponsesUsageInfo.BuiltInTools["function"].CallCount)
		})
	}
}

func TestOaiResponsesStreamHandlerCountsWebAndFileOutputCalls(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
	c.Set(common.RequestIdKey, "responses-tool-usage-test")
	body := strings.Join([]string{
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_1"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"file_search_call","id":"fs_1"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"function_call","id":"fn_1"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"web_search_call","id":"ws_2"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"file_search_call","id":"fs_2"}}`,
		`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_1","output_index":4,"sequence_number":10,"partial_image_index":0,"partial_image_b64":"a"}`,
		`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_1","output_index":4,"sequence_number":10,"partial_image_index":0,"partial_image_b64":"a"}`,
		`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_1","output_index":4,"sequence_number":11,"partial_image_index":2,"partial_image_b64":"b"}`,
		`data: {"type":"response.output_item.done","item":{"type":"image_generation_call","id":"ig_1","quality":"low","size":"1024x1024"}}`,
		`data: {"type":"response.output_item.done","item":{"type":"image_generation_call","id":"ig_1","quality":"low","size":"1024x1024"}}`,
		`data: {"type":"response.image_generation_call.partial_image","item_id":"ig_2","output_index":5,"sequence_number":12,"partial_image_index":0,"partial_image_b64":"c"}`,
		`data: {"type":"response.output_item.done","item":{"type":"image_generation_call","id":"ig_2","quality":"low","size":"1024x1024"}}`,
		`data: {"type":"response.completed","response":{"output":[{"type":"web_search_call","id":"ws_1"},{"type":"file_search_call","id":"fs_1"},{"type":"web_search_call","id":"ws_2"},{"type":"file_search_call","id":"fs_2"},{"type":"image_generation_call","id":"ig_1","quality":"low","size":"1024x1024"},{"type":"image_generation_call","id":"ig_2","quality":"low","size":"1024x1024"}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
		`data: [DONE]`,
		``,
	}, "\n")
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "gpt-test"},
		IsStream:    true,
		DisablePing: true,
		ResponsesUsageInfo: &relaycommon.ResponsesUsageInfo{BuiltInTools: map[string]*relaycommon.BuildInToolInfo{
			dto.BuildInToolWebSearch20250826: {ToolName: dto.BuildInToolWebSearch20250826},
			dto.BuildInToolFileSearch:        {ToolName: dto.BuildInToolFileSearch},
			"image_generation": {
				ToolName:           "image_generation",
				ImageModel:         "gpt-image-1",
				ImageQuality:       "low",
				ImageSize:          "1024x1024",
				ImagePartialImages: 3,
			},
			"function": {ToolName: "function"},
		}},
	}

	_, apiErr := OaiResponsesStreamHandler(c, info, resp)

	require.Nil(t, apiErr)
	assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolWebSearch20250826].CallCount)
	assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch].CallCount)
	assert.Equal(t, 2, info.ResponsesUsageInfo.BuiltInTools["image_generation"].CallCount)
	assert.Equal(t, 3, info.ResponsesUsageInfo.BuiltInTools["image_generation"].ImagePartialCount)
	assert.Len(t, info.ResponsesUsageInfo.BuiltInTools["image_generation"].ImageOutputs, 2)
	assert.Zero(t, info.ResponsesUsageInfo.BuiltInTools["function"].CallCount)
}
