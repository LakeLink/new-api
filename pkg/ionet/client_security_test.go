package ionet

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadBoundedHTTPResponseBodyRejectsOversizedResponse(t *testing.T) {
	body, err := readBoundedHTTPResponseBody(strings.NewReader("123456789"), 8)

	require.ErrorContains(t, err, "response body exceeds 8 bytes")
	assert.Nil(t, body)

	body, err = readBoundedHTTPResponseBody(strings.NewReader("12345678"), 8)
	require.NoError(t, err)
	assert.Equal(t, []byte("12345678"), body)
}

func TestNewDefaultHTTPClientUsesFiniteDefaultTimeout(t *testing.T) {
	client := NewDefaultHTTPClient(0)

	require.NotNil(t, client)
	assert.Equal(t, DefaultTimeout, client.client.Timeout)

	custom := NewDefaultHTTPClient(5 * time.Second)
	assert.Equal(t, 5*time.Second, custom.client.Timeout)
}

func TestDefaultHTTPClientRedirectPolicyProtectsSecrets(t *testing.T) {
	client := NewDefaultHTTPClient(time.Second)
	require.NotNil(t, client.client.CheckRedirect)

	via := []*http.Request{{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "api.io.example", Path: "/deployments"},
	}}
	redirect := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "attacker.example", Path: "/collect"},
		Header: http.Header{
			"X-Api-Key": []string{"secret-key"},
		},
		Body: io.NopCloser(strings.NewReader(`{"secret_env_variables":{"TOKEN":"secret"}}`)),
	}

	err := client.client.CheckRedirect(redirect, via)
	require.ErrorContains(t, err, "cross-origin redirect with request body")

	redirect.Method = http.MethodGet
	redirect.Body = nil
	require.NoError(t, client.client.CheckRedirect(redirect, via))
	assert.Empty(t, redirect.Header.Get("X-Api-Key"))
}

func TestDefaultHTTPClientRedirectPolicyRejectsTLSDowngrade(t *testing.T) {
	client := NewDefaultHTTPClient(time.Second)
	via := []*http.Request{{
		URL: &url.URL{Scheme: "https", Host: "api.io.example"},
	}}
	redirect := &http.Request{
		URL:    &url.URL{Scheme: "http", Host: "api.io.example"},
		Header: make(http.Header),
	}

	require.ErrorContains(t, client.client.CheckRedirect(redirect, via), "HTTPS to HTTP")
}

func TestDefaultHTTPClientRejectsCredentialedRedirectAndNilInputs(t *testing.T) {
	client := NewDefaultHTTPClient(time.Second)
	redirect := &http.Request{
		URL: &url.URL{
			Scheme: "https",
			User:   url.UserPassword("user", "redirect-secret"),
			Host:   "api.io.example",
		},
		Header: make(http.Header),
	}

	err := client.client.CheckRedirect(redirect, nil)
	require.ErrorContains(t, err, "redirect URL credentials")
	assert.NotContains(t, err.Error(), "redirect-secret")

	_, err = client.Do(nil)
	require.ErrorContains(t, err, "request is nil")
	var nilClient *DefaultHTTPClient
	_, err = nilClient.Do(&HTTPRequest{})
	require.ErrorContains(t, err, "client is nil")
}
