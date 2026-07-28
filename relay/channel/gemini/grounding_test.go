package gemini

import (
	"testing"

	"github.com/QuantumNous/new-api/dto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGeminiGroundingUsageCountsActualNonEmptyQueryOccurrences(t *testing.T) {
	response := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{
		{GroundingMetadata: &dto.GeminiGroundingMetadata{
			WebSearchQueries:   []string{"query one", "", "query two", "query two"},
			ImageSearchQueries: []string{" query one ", "image query"},
			GroundingChunks:    []dto.GeminiGroundingChunk{{Web: map[string]any{"uri": "https://example.com"}}},
		}},
		{GroundingMetadata: &dto.GeminiGroundingMetadata{
			WebSearchQueries: []string{"query two"},
		}},
	}}
	usage := newGeminiGroundingUsage()
	usage.addResponse(response)

	tool, count := usage.billableToolAndCount("gemini-3.5-flash")
	assert.Equal(t, "google_search", tool)
	assert.Equal(t, 6, count)
	tool, count = usage.billableToolAndCount("gemini-robotics-er-1.6-preview")
	assert.Equal(t, "google_search", tool)
	assert.Equal(t, 6, count)
	tool, count = usage.billableToolAndCount("gemini-2.5-flash")
	assert.Equal(t, "google_search", tool)
	assert.Equal(t, 1, count)
}

func TestGeminiGroundingUsageReconcilesRepeatedStreamingSnapshots(t *testing.T) {
	usage := newGeminiGroundingUsage()
	firstSnapshot := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{
		{Index: 0, GroundingMetadata: &dto.GeminiGroundingMetadata{
			WebSearchQueries: []string{"same query", "same query"},
		}},
		{Index: 1, GroundingMetadata: &dto.GeminiGroundingMetadata{
			ImageSearchQueries: []string{"same query"},
		}},
	}}
	usage.addResponse(firstSnapshot)
	usage.addResponse(firstSnapshot)

	tool, count := usage.billableToolAndCount("gemini-3.5-flash")
	require.Equal(t, "google_search", tool)
	require.Equal(t, 3, count)

	usage.addResponse(&dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{
		{Index: 0, GroundingMetadata: &dto.GeminiGroundingMetadata{
			WebSearchQueries: []string{"same query", "same query", "same query"},
		}},
		{Index: 1, GroundingMetadata: &dto.GeminiGroundingMetadata{
			ImageSearchQueries: []string{"same query", "same query"},
		}},
	}})

	tool, count = usage.billableToolAndCount("gemini-3.5-flash")
	require.Equal(t, "google_search", tool)
	require.Equal(t, 5, count)
}

func TestGeminiGroundingUsageDistinguishesMapsPricing(t *testing.T) {
	response := &dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		GroundingMetadata: &dto.GeminiGroundingMetadata{
			WebSearchQueries: []string{"restaurants near me", "late night restaurants"},
			GroundingChunks:  []dto.GeminiGroundingChunk{{Maps: map[string]any{"placeId": "places/1"}}},
		},
	}}}
	usage := newGeminiGroundingUsage()
	usage.addResponse(response)

	tool, count := usage.billableToolAndCount("gemini-3.5-flash")
	require.Equal(t, "google_maps", tool)
	require.Equal(t, 2, count)
	tool, count = usage.billableToolAndCount("gemini-2.5-pro")
	require.Equal(t, "google_maps", tool)
	require.Equal(t, 1, count)
}

func TestGeminiGroundingUsageFallsBackToOneForOmittedQueryList(t *testing.T) {
	usage := newGeminiGroundingUsage()
	usage.addResponse(&dto.GeminiChatResponse{Candidates: []dto.GeminiChatCandidate{{
		GroundingMetadata: &dto.GeminiGroundingMetadata{
			GroundingChunks: []dto.GeminiGroundingChunk{{Web: map[string]any{"uri": "https://example.com"}}},
		},
	}}})

	tool, count := usage.billableToolAndCount("gemini-3.5-flash")
	require.Equal(t, "google_search", tool)
	require.Equal(t, 1, count)
}
