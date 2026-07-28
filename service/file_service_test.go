package service

import (
	"encoding/base64"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBase64ContextCacheKeyHashesTheEntirePayload(t *testing.T) {
	prefix := strings.Repeat("A", 256)
	first := prefix + "first-payload"
	second := prefix + "other-payload"
	require.Equal(t, len(first), len(second))

	firstKey := getBase64ContextCacheKey(first, "image/png")
	secondKey := getBase64ContextCacheKey(second, "image/png")

	require.NotEqual(t, firstKey, secondKey)
	require.Equal(t, firstKey, getBase64ContextCacheKey(first, "image/png"))
	require.NotEqual(t, firstKey, getBase64ContextCacheKey(first, "image/jpeg"))
}

func TestLoadFromBase64EnforcesFileSizeLimitBeforeCaching(t *testing.T) {
	originalLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = originalLimit })

	oversized := base64.StdEncoding.EncodeToString(make([]byte, (1<<20)+1))
	cached, err := loadFromBase64(oversized, "application/octet-stream")

	require.ErrorContains(t, err, "file size exceeds maximum")
	require.Nil(t, cached)
}

func TestCleanupFileSourcesClearsStateAndAllowsReuse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	source := types.NewBase64FileSource(base64.StdEncoding.EncodeToString([]byte("test")), "text/plain")

	first, err := LoadFileSource(ctx, source)
	require.NoError(t, err)
	require.NotNil(t, first)
	require.True(t, source.HasCache())
	require.True(t, source.IsRegistered())

	CleanupFileSources(ctx)
	assert.False(t, source.HasCache())
	assert.False(t, source.IsRegistered())
	_, err = first.GetBase64Data()
	assert.ErrorContains(t, err, "file cache already closed")

	second, err := LoadFileSource(ctx, source)
	require.NoError(t, err)
	require.NotNil(t, second)
	assert.NotSame(t, first, second)
	assert.True(t, source.HasCache())
	assert.True(t, source.IsRegistered())

	CleanupFileSources(ctx)
}

func TestCleanupFileSourcesRetainsConcurrentRegistrations(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(nil)
	sources := []*types.Base64Source{
		types.NewBase64FileSource(base64.StdEncoding.EncodeToString([]byte("first")), "text/plain"),
		types.NewBase64FileSource(base64.StdEncoding.EncodeToString([]byte("second")), "text/plain"),
	}

	start := make(chan struct{})
	errs := make(chan error, len(sources))
	var wg sync.WaitGroup
	for _, source := range sources {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := LoadFileSource(ctx, source)
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}

	CleanupFileSources(ctx)
	for _, source := range sources {
		assert.False(t, source.HasCache())
		assert.False(t, source.IsRegistered())
	}
}
