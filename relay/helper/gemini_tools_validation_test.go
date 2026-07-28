package helper

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func geminiRequestWithTools(tools string) *dto.GeminiChatRequest {
	return &dto.GeminiChatRequest{
		Contents: []dto.GeminiChatContent{{
			Parts: []dto.GeminiPart{{Text: "find a place"}},
		}},
		Tools: []byte(tools),
	}
}

func TestValidateGeminiRequestRejectsAmbiguousGroundingCombinations(t *testing.T) {
	tests := []struct {
		name  string
		tools string
	}{
		{
			name:  "search and maps in separate definitions",
			tools: `[{"googleSearch":{}},{"googleMaps":{}}]`,
		},
		{
			name:  "search and maps in one definition",
			tools: `[{"googleSearch":{},"googleMaps":{}}]`,
		},
		{
			name:  "current and legacy search",
			tools: `[{"googleSearch":{},"googleSearchRetrieval":{}}]`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateGeminiRequest(geminiRequestWithTools(test.tools))
			require.Error(t, err)
			assert.Contains(t, err.Error(), "cannot")
		})
	}
}

func TestValidateGeminiRequestAllowsAttributableGroundingTools(t *testing.T) {
	for _, tools := range []string{
		`[{"googleSearch":{}}]`,
		`[{"googleSearchRetrieval":{}}]`,
		`[{"googleMaps":{}}]`,
		`[{"googleSearch":{}},{"functionDeclarations":[{"name":"local"}]}]`,
	} {
		require.NoError(t, ValidateGeminiRequest(geminiRequestWithTools(tools)), tools)
	}
}

func TestValidateGeminiRequestRejectsGenerateContentBatchShape(t *testing.T) {
	request := &dto.GeminiChatRequest{
		Requests: []dto.GeminiChatRequest{{
			Contents: []dto.GeminiChatContent{{Parts: []dto.GeminiPart{{Text: "one"}}}},
		}},
	}
	require.ErrorContains(t, ValidateGeminiRequest(request), "batch requests are not supported")
}
