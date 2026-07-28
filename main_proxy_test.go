package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigureTrustedProxiesRejectsSpoofedForwardedIPByDefault(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TRUSTED_PROXIES", "")

	engine := gin.New()
	require.NoError(t, configureTrustedProxies(engine))
	engine.GET("/ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	request := httptest.NewRequest(http.MethodGet, "/ip", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, "192.0.2.10", recorder.Body.String())
}

func TestConfigureTrustedProxiesHonorsExplicitProxyNetwork(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TRUSTED_PROXIES", "192.0.2.0/24")

	engine := gin.New()
	require.NoError(t, configureTrustedProxies(engine))
	engine.GET("/ip", func(c *gin.Context) {
		c.String(http.StatusOK, c.ClientIP())
	})

	request := httptest.NewRequest(http.MethodGet, "/ip", nil)
	request.RemoteAddr = "192.0.2.10:12345"
	request.Header.Set("X-Forwarded-For", "203.0.113.99")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)

	assert.Equal(t, "203.0.113.99", recorder.Body.String())
}

func TestConfigureTrustedProxiesRejectsEmptyListEntry(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "127.0.0.1, ,10.0.0.0/8")

	err := configureTrustedProxies(gin.New())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty proxy")
}
