package service

import (
	"image"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func jinaTestImage(width, height int) *types.FileMeta {
	source := types.NewBase64FileSource("ignored", "image/png")
	source.SetCache(&types.CachedFileData{
		ImageConfig: &image.Config{Width: width, Height: height},
		ImageFormat: "png",
	})
	return types.NewImageFileMeta(source, "")
}

func TestJinaImageTokenCalculationUsesPublishedTilePricing(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	file := jinaTestImage(600, 600)

	tests := []struct {
		model string
		want  int
	}{
		{model: "jina-embeddings-v4", want: 4840},
		{model: "jina-clip-v2", want: 16000},
		{model: "jina-clip-v1", want: 9000},
	}

	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			tokens, handled, clamp, err := getJinaMediaToken(c, file, test.model)
			require.NoError(t, err)
			require.True(t, handled)
			assert.Nil(t, clamp)
			assert.Equal(t, test.want, tokens)
		})
	}
}

func TestJinaUnspecifiedMediaPricingUsesBoundedModelReserve(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	tests := []struct {
		name     string
		model    string
		fileType types.FileType
		want     int
	}{
		{name: "reranker image", model: "jina-reranker-m0", fileType: types.FileTypeImage, want: 10240},
		{name: "omni nano audio", model: "jina-embeddings-v5-omni-nano", fileType: types.FileTypeAudio, want: 8192},
		{name: "omni small video", model: "jina-embeddings-v5-omni-small", fileType: types.FileTypeVideo, want: 32768},
		{name: "v4 pdf", model: "jina-embeddings-v4", fileType: types.FileTypeFile, want: 32768},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			file := types.NewFileMeta(test.fileType, types.NewBase64FileSource("ignored", "application/octet-stream"))
			tokens, handled, clamp, err := getJinaMediaToken(c, file, test.model)
			require.NoError(t, err)
			require.True(t, handled)
			assert.Nil(t, clamp)
			assert.Equal(t, test.want, tokens)
		})
	}
}

func TestJinaImageTokenCalculationSaturatesHugeDimensions(t *testing.T) {
	if uint64(^uint(0)) < math.MaxUint32 {
		t.Skip("requires a 64-bit target to represent PNG dimensions")
	}
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	tokens, handled, clamp, err := getJinaMediaToken(c, jinaTestImage(int(math.MaxUint32), int(math.MaxUint32)), "jina-embeddings-v4")

	require.NoError(t, err)
	require.True(t, handled)
	assert.Equal(t, common.MaxQuota, tokens)
	if assert.NotNil(t, clamp) {
		assert.Equal(t, common.QuotaClampOverflow, clamp.Kind)
	}
}

func TestEstimateRequestTokenPreconsumesJinaImageTokens(t *testing.T) {
	originalCountToken := constant.CountToken
	constant.CountToken = true
	t.Cleanup(func() { constant.CountToken = originalCountToken })

	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	common.SetContextKey(c, constant.ContextKeyOriginalModel, "jina-clip-v2")
	meta := &types.TokenCountMeta{Files: []*types.FileMeta{jinaTestImage(600, 600)}}
	info := &relaycommon.RelayInfo{RelayFormat: types.RelayFormatOpenAI}

	tokens, err := EstimateRequestToken(c, meta, info)

	require.NoError(t, err)
	assert.Equal(t, 16003, tokens)
}
