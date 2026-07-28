package gemini

import (
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
)

type geminiGroundingUsage struct {
	queryOccurrences map[geminiGroundingQuery]int
	hasWebResult     bool
	hasImageResult   bool
	hasMapsResult    bool
}

type geminiGroundingQuery struct {
	candidateIndex int64
	kind           string
	query          string
}

func newGeminiGroundingUsage() *geminiGroundingUsage {
	return &geminiGroundingUsage{queryOccurrences: make(map[geminiGroundingQuery]int)}
}

func (u *geminiGroundingUsage) addResponse(response *dto.GeminiChatResponse) {
	if u == nil || response == nil {
		return
	}
	// Gemini streaming responses can repeat a candidate's cumulative grounding
	// metadata. Reconcile each response as a snapshot: preserve the greatest
	// observed multiplicity for a candidate/query pair instead of summing every
	// chunk. Keeping web and image queries in separate namespaces also preserves
	// two actual searches when both modalities happen to use the same text.
	snapshotOccurrences := make(map[geminiGroundingQuery]int)
	for _, candidate := range response.Candidates {
		metadata := candidate.GroundingMetadata
		if metadata == nil {
			continue
		}
		for _, queryList := range []struct {
			kind    string
			queries []string
		}{
			{kind: "web", queries: metadata.WebSearchQueries},
			{kind: "image", queries: metadata.ImageSearchQueries},
		} {
			for _, query := range queryList.queries {
				query = strings.TrimSpace(query)
				if query == "" {
					continue
				}
				key := geminiGroundingQuery{
					candidateIndex: candidate.Index,
					kind:           queryList.kind,
					query:          query,
				}
				snapshotOccurrences[key]++
			}
		}
		for _, chunk := range metadata.GroundingChunks {
			u.hasWebResult = u.hasWebResult || chunk.Web != nil
			u.hasImageResult = u.hasImageResult || chunk.Image != nil
			u.hasMapsResult = u.hasMapsResult || chunk.Maps != nil
		}
	}
	for query, count := range snapshotOccurrences {
		if count > u.queryOccurrences[query] {
			u.queryOccurrences[query] = count
		}
	}
}

func (u *geminiGroundingUsage) queryCount() int {
	count := 0
	for _, occurrences := range u.queryOccurrences {
		count += occurrences
	}
	return count
}

func (u *geminiGroundingUsage) billableToolAndCount(model string) (string, int) {
	if u == nil {
		return "", 0
	}
	if strings.HasPrefix(model, "gemini-3") ||
		model == "gemini-flash-latest" ||
		model == "gemini-flash-lite-latest" ||
		model == "gemini-pro-latest" ||
		strings.HasPrefix(model, "gemini-robotics-er-1.6") {
		count := u.queryCount()
		if count == 0 && (u.hasWebResult || u.hasImageResult || u.hasMapsResult) {
			// A grounding source proves that at least one billable search
			// occurred even if an upstream omitted its query list.
			count = 1
		}
		if count == 0 {
			return "", 0
		}
		if u.hasMapsResult && !u.hasWebResult && !u.hasImageResult {
			return "google_maps", count
		}
		return "google_search", count
	}

	// Gemini 2.5 and older bill one grounded prompt. Maps must be
	// distinguished because its prompt price differs from Google Search.
	if u.hasMapsResult {
		return "google_maps", 1
	}
	if u.queryCount() > 0 || u.hasWebResult || u.hasImageResult {
		return "google_search", 1
	}
	return "", 0
}

func (u *geminiGroundingUsage) setBillingContext(c *gin.Context, model string) {
	tool, count := u.billableToolAndCount(model)
	common.SetContextKey(c, constant.ContextKeyGeminiGroundingTool, tool)
	common.SetContextKey(c, constant.ContextKeyGeminiGroundingSearchCount, count)
}
