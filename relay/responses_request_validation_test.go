package relay

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRefreshFinalResponsesRequestUsesToolsAddedByParamOverride(t *testing.T) {
	original := dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: []byte(`"hello"`),
		Tools: []byte(`[{"type":"web_search","search_context_size":"low"}]`),
	}
	jsonData, err := common.Marshal(original)
	require.NoError(t, err)
	jsonData, err = relaycommon.ApplyParamOverride(jsonData, map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path": "tools",
				"mode": "set",
				"value": []interface{}{
					map[string]interface{}{"type": dto.BuildInToolFileSearch},
				},
			},
		},
	}, nil)
	require.NoError(t, err)

	info := &relaycommon.RelayInfo{ResponsesUsageInfo: relaycommon.NewResponsesUsageInfo(&original)}
	require.NoError(t, refreshFinalResponsesRequest(info, jsonData))

	assert.NotContains(t, info.ResponsesUsageInfo.BuiltInTools, dto.BuildInToolWebSearch)
	fileSearch, exists := info.ResponsesUsageInfo.BuiltInTools[dto.BuildInToolFileSearch]
	require.True(t, exists)
	assert.Zero(t, fileSearch.CallCount)
}

func TestRefreshFinalResponsesRequestRejectsUnsafeParamOverride(t *testing.T) {
	original := dto.OpenAIResponsesRequest{
		Model: "gpt-test",
		Input: []byte(`"hello"`),
	}
	jsonData, err := common.Marshal(original)
	require.NoError(t, err)
	jsonData, err = relaycommon.ApplyParamOverride(jsonData, map[string]interface{}{
		"operations": []interface{}{
			map[string]interface{}{
				"path":  "max_tool_calls",
				"mode":  "set",
				"value": common.MaxTextToolCallCount + 1,
			},
		},
	}, nil)
	require.NoError(t, err)

	err = refreshFinalResponsesRequest(&relaycommon.RelayInfo{}, jsonData)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "max_tool_calls must not exceed")
}

func TestRefreshFinalResponsesRequestPersistsOverriddenToolCap(t *testing.T) {
	originalCap := uint(1)
	finalCap := uint(4)
	original := dto.OpenAIResponsesRequest{
		Model:        "gpt-test",
		Input:        []byte(`"hello"`),
		MaxToolCalls: &originalCap,
		Tools:        []byte(`[{"type":"file_search"}]`),
	}
	finalRequest := original
	finalRequest.MaxToolCalls = &finalCap
	jsonData, err := common.Marshal(finalRequest)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{Request: &original}

	require.NoError(t, refreshFinalResponsesRequest(info, jsonData))
	require.NotNil(t, info.ResponsesUsageInfo.MaxToolCalls)
	assert.Equal(t, finalCap, *info.ResponsesUsageInfo.MaxToolCalls)
	require.NotNil(t, original.MaxToolCalls)
	assert.Equal(t, finalCap, *original.MaxToolCalls)
}

func TestRefreshFinalResponsesRequestReplacesStaleImageBillingMetadata(t *testing.T) {
	original := dto.OpenAIResponsesRequest{
		Model:  "gpt-test",
		Input:  []byte(`"hello"`),
		Stream: common.GetPointer(true),
		Tools: []byte(`[{
			"type":"image_generation",
			"model":"gpt-image-1",
			"quality":"low",
			"size":"1024x1024",
			"partial_images":0
		}]`),
	}
	finalRequest := original
	finalRequest.Tools = []byte(`[{
		"type":"image_generation",
		"model":"gpt-image-2",
		"quality":"high",
		"size":"2048x1024",
		"partial_images":3
	}]`)
	jsonData, err := common.Marshal(finalRequest)
	require.NoError(t, err)
	info := &relaycommon.RelayInfo{
		Request:            &original,
		ResponsesUsageInfo: relaycommon.NewResponsesUsageInfo(&original),
	}

	require.NoError(t, refreshFinalResponsesRequest(info, jsonData))

	tool := info.ResponsesUsageInfo.BuiltInTools["image_generation"]
	require.NotNil(t, tool)
	assert.Equal(t, "gpt-image-2", tool.ImageModel)
	assert.Equal(t, "high", tool.ImageQuality)
	assert.Equal(t, "2048x1024", tool.ImageSize)
	assert.Equal(t, 3, tool.ImagePartialImages)
	assert.Zero(t, tool.CallCount)
	assert.Empty(t, tool.ImageOutputs)
	assert.Empty(t, info.ResponsesUsageInfo.SeenPartialImages)
}
