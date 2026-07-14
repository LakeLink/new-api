package service

import (
	"strings"
	"testing"

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
