package main

import (
	"net/http"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
)

func TestSessionCookieOptionsSupportOAuthCallback(t *testing.T) {
	originalSecure := common.SessionCookieSecure
	common.SessionCookieSecure = true
	t.Cleanup(func() {
		common.SessionCookieSecure = originalSecure
	})

	options := sessionCookieOptions()

	assert.Equal(t, http.SameSiteLaxMode, options.SameSite)
	assert.True(t, options.HttpOnly)
	assert.True(t, options.Secure)
	assert.Equal(t, "/", options.Path)
}
