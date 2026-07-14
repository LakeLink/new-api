package baidu

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBaiduAccessTokenNeverReturnsExpiredCachedToken(t *testing.T) {
	const key = "invalid-key"
	baiduTokenStore.Store(key, BaiduAccessToken{
		AccessToken: "expired-token",
		ExpiresAt:   time.Now().Add(-time.Minute),
	})
	t.Cleanup(func() { baiduTokenStore.Delete(key) })

	token, err := getBaiduAccessToken(key, context.Background())

	require.ErrorContains(t, err, "invalid baidu apikey")
	assert.Empty(t, token)
	_, stillCached := baiduTokenStore.Load(key)
	assert.False(t, stillCached)
}
