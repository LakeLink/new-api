package service

import (
	"image"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImageTokenCountBoundsDeclaredHugeDimensions(t *testing.T) {
	if uint64(^uint(0)) < math.MaxUint32 {
		t.Skip("requires a 64-bit target to represent PNG dimensions")
	}
	originalGetMediaToken := constant.GetMediaToken
	originalGetMediaTokenNotStream := constant.GetMediaTokenNotStream
	constant.GetMediaToken = true
	constant.GetMediaTokenNotStream = true
	t.Cleanup(func() {
		constant.GetMediaToken = originalGetMediaToken
		constant.GetMediaTokenNotStream = originalGetMediaTokenNotStream
	})

	source := types.NewBase64FileSource("ignored", "image/png")
	source.SetCache(&types.CachedFileData{
		ImageConfig: &image.Config{Width: int(math.MaxUint32), Height: int(math.MaxUint32)},
		ImageFormat: "png",
	})
	file := types.NewImageFileMeta(source, "high")
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())

	tokens, err := getImageToken(c, file, "gpt-4.1-mini", true)

	require.NoError(t, err)
	require.Equal(t, 2464, tokens)
}
