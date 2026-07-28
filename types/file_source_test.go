package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFileSourceIdentifiersDoNotExposeCredentialsOrMedia(t *testing.T) {
	urlSource := NewURLFileSource("https://user:password@example.com/private/token.png?signature=secret#fragment")
	assert.Equal(t, "url:https://example.com", urlSource.GetIdentifier())

	invalidURL := NewURLFileSource("not a URL?secret=value")
	assert.Equal(t, "url:[invalid]", invalidURL.GetIdentifier())

	base64Source := NewBase64FileSource("super-secret-inline-media", "image/png")
	assert.Equal(t, "base64:[25 encoded bytes]", base64Source.GetIdentifier())
}

func TestFileSourceCleanupRegistrationIsAtomicAndReusable(t *testing.T) {
	source := NewBase64FileSource("dGVzdA==", "text/plain")

	require.True(t, source.ClaimCleanupRegistration())
	assert.True(t, source.IsRegistered())
	assert.False(t, source.ClaimCleanupRegistration())

	source.SetRegistered(false)
	assert.True(t, source.ClaimCleanupRegistration())
}
