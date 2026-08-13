package matcher

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMatchAnyRegexRepairsInvalidCachedEntry(t *testing.T) {
	const pattern = "^claude-"
	compiledRegexCache.Store(pattern, "invalid")
	t.Cleanup(func() {
		compiledRegexCache.Delete(pattern)
	})

	require.NotPanics(t, func() {
		assert.True(t, MatchAnyRegex([]string{pattern}, "claude-sonnet"))
	})
	cached, ok := compiledRegexCache.Load(pattern)
	require.True(t, ok)
	assert.NotEqual(t, "invalid", cached)
}
