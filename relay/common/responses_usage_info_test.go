package common

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenRelayInfoResponsesRecognizesDocumentedWebSearchContextSizes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		toolType        string
		contextField    string
		expectedContext string
	}{
		{toolType: dto.BuildInToolWebSearch, contextField: `,"search_context_size":"high"`, expectedContext: "high"},
		{toolType: dto.BuildInToolWebSearch20250826, expectedContext: "medium"},
		{toolType: dto.BuildInToolWebSearchPreview, contextField: `,"search_context_size":"low"`, expectedContext: "low"},
		{toolType: dto.BuildInToolWebSearchPreview20250311, contextField: `,"search_context_size":"medium"`, expectedContext: "medium"},
	}

	for _, test := range tests {
		t.Run(test.toolType, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
			request := &dto.OpenAIResponsesRequest{
				Model: "gpt-test",
				Input: []byte(`"hi"`),
				Tools: []byte(fmt.Sprintf(`[{"type":%q%s}]`, test.toolType, test.contextField)),
			}

			info := GenRelayInfoResponses(c, request)
			configuredType, tool, err := info.ResponsesUsageInfo.WebSearchTool()

			require.NoError(t, err)
			require.NotNil(t, tool)
			assert.Equal(t, test.toolType, configuredType)
			assert.Equal(t, test.expectedContext, tool.SearchContextSize)
		})
	}
}

func TestResponsesUsageInfoRejectsAmbiguousWebSearchConfiguration(t *testing.T) {
	usageInfo := &ResponsesUsageInfo{BuiltInTools: map[string]*BuildInToolInfo{
		dto.BuildInToolWebSearch:        {ToolName: dto.BuildInToolWebSearch},
		dto.BuildInToolWebSearchPreview: {ToolName: dto.BuildInToolWebSearchPreview},
	}}

	_, _, err := usageInfo.WebSearchTool()

	require.ErrorContains(t, err, "ambiguous")
}
