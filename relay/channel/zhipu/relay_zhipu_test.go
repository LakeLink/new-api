package zhipu

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetZhipuTokenRecoversFromStaleCacheValue(t *testing.T) {
	const apiKey = "test-id.test-secret"

	tests := []struct {
		name  string
		value any
	}{
		{name: "wrong type", value: 123},
		{name: "empty token", value: zhipuTokenData{ExpiryTime: time.Now().Add(time.Hour)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			zhipuTokens.Store(apiKey, test.value)
			t.Cleanup(func() {
				zhipuTokens.Delete(apiKey)
			})

			var token string
			require.NotPanics(t, func() {
				token = getZhipuToken(apiKey)
			})
			assert.NotEmpty(t, token)

			cached, ok := zhipuTokens.Load(apiKey)
			require.True(t, ok)
			tokenData, valid := cached.(zhipuTokenData)
			require.True(t, valid)
			assert.NotEmpty(t, tokenData.Token)
		})
	}
}
