package common

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestMaskSensitiveInfoRemovesURLCredentialsAndQueryValues(t *testing.T) {
	masked := MaskSensitiveInfo(
		`request failed for https://user:password@api.example.com/v1/models?key=secret&token=other`,
	)

	assert.NotContains(t, masked, "user")
	assert.NotContains(t, masked, "password")
	assert.NotContains(t, masked, "secret")
	assert.NotContains(t, masked, "other")
	assert.Contains(t, masked, "key=***")
	assert.Contains(t, masked, "token=***")
}

func TestMaskSensitiveInfoRemovesCredentialLabelsAndAuthorizationSchemes(t *testing.T) {
	masked := MaskSensitiveInfo(
		`Authorization: Bearer abc.def_123 api_key="key-value" password=hunter2 cookie=session-value`,
	)

	assert.NotContains(t, masked, "abc.def_123")
	assert.NotContains(t, masked, "key-value")
	assert.NotContains(t, masked, "hunter2")
	assert.NotContains(t, masked, "session-value")
	assert.Contains(t, masked, "Authorization: ***")
}

func TestMaskSensitiveInfoDoesNotLeakMalformedURL(t *testing.T) {
	masked := MaskSensitiveInfo(
		`parse "https://user:password-secret@example.com/v1?api_key=query-secret%zz": invalid URL escape`,
	)

	assert.NotContains(t, masked, "password-secret")
	assert.NotContains(t, masked, "query-secret")
	assert.NotContains(t, masked, "example.com")
	assert.Contains(t, masked, "https://***")
}

func TestMaskSensitiveInfoRemovesWebSocketCredentialsAndQueryValues(t *testing.T) {
	masked := MaskSensitiveInfo(
		`dial wss://user:password-secret@realtime.example.com/v1?api_key=query-secret failed`,
	)

	assert.NotContains(t, masked, "user")
	assert.NotContains(t, masked, "password-secret")
	assert.NotContains(t, masked, "query-secret")
	assert.NotContains(t, masked, "realtime.example.com")
	assert.Contains(t, masked, "wss://***")
}
