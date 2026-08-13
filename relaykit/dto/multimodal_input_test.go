package dto

import (
	"testing"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEmbeddingMultimodalInputContributesToTokenMetadata(t *testing.T) {
	request := EmbeddingRequest{
		Input: []any{
			map[string]any{"text": "caption"},
			map[string]any{"image": "https://example.test/image.png"},
			map[string]any{"content": []any{
				map[string]any{"text": "spoken words"},
				map[string]any{"audio": "audio-base64"},
			}},
		},
	}

	meta := request.GetTokenCountMeta()
	assert.Equal(t, "caption\nspoken words", meta.CombineText)
	require.Len(t, meta.Files, 2)
	assert.Equal(t, types.FileTypeImage, meta.Files[0].FileType)
	assert.True(t, meta.Files[0].Source.IsURL())
	assert.Equal(t, types.FileTypeAudio, meta.Files[1].FileType)
	assert.Equal(t, "audio-base64", meta.Files[1].Source.GetRawData())
	assert.Equal(t, []string{"caption", "spoken words"}, request.ParseInput())
}

func TestRerankImageQueryAndDocumentsContributeToTokenMetadata(t *testing.T) {
	request := RerankRequest{
		Query: map[string]any{"image": "https://example.test/query.png"},
		Documents: []any{
			"plain document",
			map[string]any{"text": "object document"},
			map[string]any{"image": "document-base64"},
		},
	}

	require.True(t, request.HasValidQuery())
	_, textQuery := request.QueryString()
	assert.False(t, textQuery)

	meta := request.GetTokenCountMeta()
	assert.Equal(t, "plain document\nobject document", meta.CombineText)
	require.Len(t, meta.Files, 2)
	assert.Equal(t, types.FileTypeImage, meta.Files[0].FileType)
	assert.Equal(t, "document-base64", meta.Files[0].Source.GetRawData())
	assert.Equal(t, "https://example.test/query.png", meta.Files[1].Source.GetRawData())
}
