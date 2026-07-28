package jina

import (
	"testing"

	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelListMatchesPublishedEmbeddingAndRerankCatalog(t *testing.T) {
	models := (&Adaptor{}).GetModelList()
	expected := []string{
		"jina-embeddings-v2-base-en",
		"jina-embeddings-v2-base-zh",
		"jina-embeddings-v2-base-de",
		"jina-embeddings-v2-base-es",
		"jina-embeddings-v2-base-code",
		"jina-embeddings-v3",
		"jina-embeddings-v4",
		"jina-embeddings-v5-text-nano",
		"jina-embeddings-v5-text-small",
		"jina-embeddings-v5-omni-nano",
		"jina-embeddings-v5-omni-small",
		"jina-code-embeddings-0.5b",
		"jina-code-embeddings-1.5b",
		"jina-clip-v1",
		"jina-clip-v2",
		"jina-colbert-v1-en",
		"jina-colbert-v2",
		"elser-v2",
		"jina-reranker-v1-tiny-en",
		"jina-reranker-v1-turbo-en",
		"jina-reranker-v1-base-en",
		"jina-reranker-v2-base-multilingual",
		"jina-reranker-m0",
		"jina-reranker-v3",
		"jina-reranker-v3.5",
	}

	assert.ElementsMatch(t, expected, models)
	assert.NotContains(t, models, "jina-embedding-b-en-v1")
}

func TestPublishedModelsHaveDefaultPricing(t *testing.T) {
	for _, model := range (&Adaptor{}).GetModelList() {
		t.Run(model, func(t *testing.T) {
			ratio, ok := ratio_setting.GetDefaultModelRatioMap()[model]
			require.True(t, ok)

			want := 0.05 / 2
			if model == "jina-embeddings-v5-text-nano" || model == "jina-embeddings-v5-omni-nano" {
				want = 0.02 / 2
			}
			assert.Equal(t, want, ratio)
		})
	}
}
