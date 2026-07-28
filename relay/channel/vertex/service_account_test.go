package vertex

import (
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetAccessTokenRejectsStaleCacheValueWithoutPanicking(t *testing.T) {
	const channelID = 991337
	cacheKey := "access-token-991337"
	Cache.DeleteIf(func(key string) bool {
		return key == cacheKey
	})
	Cache.SetDefault(cacheKey, 12345)
	t.Cleanup(func() {
		Cache.DeleteIf(func(key string) bool {
			return key == cacheKey
		})
	})

	adaptor := &Adaptor{
		AccountCredentials: Credentials{
			ClientEmail: "service-account@example.com",
			PrivateKey:  "not-a-private-key",
		},
	}
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{ChannelId: channelID},
	}

	var (
		token string
		err   error
	)
	require.NotPanics(t, func() {
		token, err = getAccessToken(adaptor, info)
	})

	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to create signed JWT")
	assert.Empty(t, token)
}
