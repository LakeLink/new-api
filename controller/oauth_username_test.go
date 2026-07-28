package controller

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/QuantumNous/new-api/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewOAuthUsernameIsUniqueAndBounded(t *testing.T) {
	first := newOAuthUsername("github_")
	second := newOAuthUsername("github_")

	assert.NotEqual(t, first, second)
	assert.True(t, strings.HasPrefix(first, "github_"))
	assert.LessOrEqual(t, utf8.RuneCountInString(first), model.UserNameMaxLength)
}

func TestNewOAuthUsernameTruncatesPrefixByRunes(t *testing.T) {
	username := newOAuthUsername(strings.Repeat("微", model.UserNameMaxLength))

	require.True(t, utf8.ValidString(username))
	assert.Equal(t, model.UserNameMaxLength, utf8.RuneCountInString(username))
	assert.True(t, strings.HasPrefix(username, strings.Repeat("微", model.UserNameMaxLength-oauthUsernameSuffixLength)))
}
