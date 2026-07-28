package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRelayInfoToStringSanitizesChannelBaseURL(t *testing.T) {
	info := &RelayInfo{
		ChannelMeta: &ChannelMeta{
			ChannelBaseUrl: "https://api-user:api-password@example.test/v1?key=api-secret#token",
		},
	}

	logValue := info.ToString()

	assert.Contains(t, logValue, "https://example.test/v1?key=%2A%2A%2Amasked%2A%2A%2A")
	assert.NotContains(t, logValue, "api-user")
	assert.NotContains(t, logValue, "api-password")
	assert.NotContains(t, logValue, "api-secret")
	assert.NotContains(t, logValue, "#token")
}
