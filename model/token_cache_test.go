package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTokenCacheSnapshotOwnsPointerFields(t *testing.T) {
	allowIPs := "192.0.2.1"
	token := Token{AllowIps: &allowIPs}

	snapshot := snapshotTokenForCache(token)
	require.NotNil(t, snapshot.AllowIps)

	*token.AllowIps = "198.51.100.2"
	assert.Equal(t, "192.0.2.1", *snapshot.AllowIps)
}
