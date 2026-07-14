package dto

import (
	"strings"

	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
)

type RerankRequest struct {
	Documents       []any  `json:"documents"`
	Query           any    `json:"query"`
	Model           string `json:"model"`
	TopN            *int   `json:"top_n,omitempty"`
	ReturnDocuments *bool  `json:"return_documents,omitempty"`
	MaxChunkPerDoc  *int   `json:"max_chunk_per_doc,omitempty"`
	OverLapTokens   *int   `json:"overlap_tokens,omitempty"`
}

func (r *RerankRequest) IsStream(c *gin.Context) bool {
	return false
}

func (r *RerankRequest) GetTokenCountMeta() *types.TokenCountMeta {
	texts := make([]string, 0)
	files := make([]*types.FileMeta, 0)

	for _, document := range r.Documents {
		collectMultimodalTokenInputs(document, &texts, &files)
	}
	collectMultimodalTokenInputs(r.Query, &texts, &files)

	return &types.TokenCountMeta{
		CombineText: strings.Join(texts, "\n"),
		Files:       files,
	}
}

func (r *RerankRequest) QueryString() (string, bool) {
	query, ok := r.Query.(string)
	return query, ok && strings.TrimSpace(query) != ""
}

func (r *RerankRequest) HasValidQuery() bool {
	if _, ok := r.QueryString(); ok {
		return true
	}

	var texts []string
	var files []*types.FileMeta
	collectMultimodalTokenInputs(r.Query, &texts, &files)
	return len(texts) > 0 || len(files) > 0
}

func (r *RerankRequest) SetModelName(modelName string) {
	if modelName != "" {
		r.Model = modelName
	}
}

func (r *RerankRequest) GetReturnDocuments() bool {
	if r.ReturnDocuments == nil {
		return false
	}
	return *r.ReturnDocuments
}

type RerankResponseResult struct {
	Document       any     `json:"document,omitempty"`
	Index          int     `json:"index"`
	RelevanceScore float64 `json:"relevance_score"`
}

type RerankDocument struct {
	Text any `json:"text"`
}

type RerankResponse struct {
	Results []RerankResponseResult `json:"results"`
	Usage   Usage                  `json:"usage"`
}
