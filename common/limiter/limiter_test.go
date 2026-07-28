package limiter

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisLimiterFailsClosedWithoutClient(t *testing.T) {
	allowed, err := New(context.Background(), nil).Allow(context.Background(), "key")

	require.Error(t, err)
	assert.False(t, allowed)
	assert.Contains(t, err.Error(), "not initialized")
}
