package helper

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGetAndValidateResponsesRequestAcceptsDocumentedWebSearchTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	toolTypes := []string{
		dto.BuildInToolWebSearch,
		dto.BuildInToolWebSearch20250826,
		dto.BuildInToolWebSearchPreview,
		dto.BuildInToolWebSearchPreview20250311,
	}
	for _, toolType := range toolTypes {
		t.Run(toolType, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":"gpt-test","input":"hi","tools":[{"type":%q,"search_context_size":"high"},{"type":"function","name":"lookup","parameters":{"type":"object"}}]}`, toolType)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body))
			c.Request.Header.Set("Content-Type", "application/json")

			request, err := GetAndValidateResponsesRequest(c)

			require.NoError(t, err)
			require.NotNil(t, request)
		})
	}
}

func TestGetAndValidateResponsesRequestRejectsAmbiguousOrMalformedTools(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name    string
		tools   string
		message string
	}{
		{name: "tools is object", tools: `{"type":"web_search"}`, message: "tools must be an array"},
		{name: "tool is null", tools: `[null]`, message: "tools[0] must be an object"},
		{name: "missing type", tools: `[{}]`, message: "tools[0].type is required"},
		{name: "non-string type", tools: `[{"type":7}]`, message: "tools[0].type must be a non-empty string"},
		{name: "function missing name", tools: `[{"type":"function"}]`, message: "tools[0].name must be a non-empty string"},
		{name: "function blank name", tools: `[{"type":"function","name":" "}]`, message: "tools[0].name must be a non-empty string"},
		{name: "unsupported web-search version", tools: `[{"type":"web_search_2099_01_01"}]`, message: "not a supported Responses web-search tool"},
		{name: "invalid context size", tools: `[{"type":"web_search","search_context_size":"maximum"}]`, message: "search_context_size must be one of"},
		{name: "duplicate current definitions", tools: `[{"type":"web_search"},{"type":"web_search_2025_08_26"}]`, message: "ambiguous duplicate web-search definitions"},
		{name: "mixed current and preview definitions", tools: `[{"type":"web_search"},{"type":"web_search_preview"}]`, message: "ambiguous duplicate web-search definitions"},
		{name: "duplicate preview definitions", tools: `[{"type":"web_search_preview_2025_03_11"},{"type":"web_search_preview_2025_03_11"}]`, message: "ambiguous duplicate web-search definitions"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"model":"gpt-test","input":"hi","tools":%s}`, test.tools)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body))
			c.Request.Header.Set("Content-Type", "application/json")

			_, err := GetAndValidateResponsesRequest(c)

			require.ErrorContains(t, err, test.message)
		})
	}
}
