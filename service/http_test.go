package service

import (
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadResponseBodyWithLimitRejectsOversizedBody(t *testing.T) {
	body, err := ReadResponseBodyWithLimit(
		strings.NewReader(strings.Repeat("x", 17)),
		16,
	)

	require.ErrorContains(t, err, "response body exceeds 16 bytes")
	assert.Nil(t, body)

	body, err = ReadResponseBodyWithLimit(io.LimitReader(strings.NewReader("exact"), 5), 5)
	require.NoError(t, err)
	assert.Equal(t, []byte("exact"), body)
}

func TestNewProxyHTTPClientRequiresHostAndConfiguresHandshakeTimeouts(t *testing.T) {
	ResetProxyClientCache()
	t.Cleanup(ResetProxyClientCache)

	client, err := NewProxyHttpClient("http://proxy-user:supersecret@proxy.example:8080")
	require.NoError(t, err)
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.DialContext)
	assert.NotZero(t, transport.TLSHandshakeTimeout)
	assert.NotZero(t, transport.ExpectContinueTimeout)

	proxyClientLock.Lock()
	require.Len(t, proxyClients, 1)
	for cacheKey := range proxyClients {
		assert.NotContains(t, cacheKey, "proxy-user")
		assert.NotContains(t, cacheKey, "supersecret")
		assert.NotContains(t, cacheKey, "proxy.example")
	}
	proxyClientLock.Unlock()

	_, err = NewProxyHttpClient("http://user:supersecret@")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecret")
}

func TestRelayHTTPClientSettingsRejectNegativeAndOverflowDurations(t *testing.T) {
	originalRelayTimeout := common.RelayTimeout
	originalIdleTimeout := common.RelayIdleConnTimeout
	originalMaxIdleConns := common.RelayMaxIdleConns
	originalMaxIdleConnsPerHost := common.RelayMaxIdleConnsPerHost
	t.Cleanup(func() {
		common.RelayTimeout = originalRelayTimeout
		common.RelayIdleConnTimeout = originalIdleTimeout
		common.RelayMaxIdleConns = originalMaxIdleConns
		common.RelayMaxIdleConnsPerHost = originalMaxIdleConnsPerHost
	})

	common.RelayTimeout = -1
	common.RelayIdleConnTimeout = -1
	common.RelayMaxIdleConns = -1
	common.RelayMaxIdleConnsPerHost = -1

	client := &http.Client{
		Transport: newRelayTransport(nil, nil),
		Timeout:   relayRequestTimeout(),
	}
	transport, ok := client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.Equal(t, invalidRelayTimeoutFallback, client.Timeout)
	assert.Equal(t, defaultRelayIdleConnTimeout, transport.IdleConnTimeout)
	assert.Equal(t, defaultRelayMaxIdleConns, transport.MaxIdleConns)
	assert.Equal(t, defaultRelayMaxIdleConnsHost, transport.MaxIdleConnsPerHost)

	overflowingSeconds64 := int64(math.MaxInt64)/int64(time.Second) + 1
	overflowingSeconds := int(overflowingSeconds64)
	if int64(overflowingSeconds) == overflowingSeconds64 {
		common.RelayTimeout = overflowingSeconds
		common.RelayIdleConnTimeout = overflowingSeconds
		assert.Equal(t, invalidRelayTimeoutFallback, relayRequestTimeout())
		_, _, idleTimeout := relayTransportPoolSettings()
		assert.Equal(t, defaultRelayIdleConnTimeout, idleTimeout)
	}
}

func TestRelayHTTPClientSettingsPreserveDocumentedZeroTimeouts(t *testing.T) {
	originalRelayTimeout := common.RelayTimeout
	originalIdleTimeout := common.RelayIdleConnTimeout
	t.Cleanup(func() {
		common.RelayTimeout = originalRelayTimeout
		common.RelayIdleConnTimeout = originalIdleTimeout
	})
	common.RelayTimeout = 0
	common.RelayIdleConnTimeout = 0

	assert.Zero(t, relayRequestTimeout())
	_, _, idleTimeout := relayTransportPoolSettings()
	assert.Zero(t, idleTimeout)
	assert.Equal(t, 60*time.Second, newProtectedFetchHTTPClient().Timeout)
}

func TestCopyUpstreamResponseHeadersFiltersIntermediaryAndCookieFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	headers := http.Header{
		"Connection":           []string{"keep-alive, X-Remove-Me"},
		"Keep-Alive":           []string{"timeout=5"},
		"Proxy-Authenticate":   []string{"Basic"},
		"Proxy-Authorization":  []string{"secret"},
		"Set-Cookie":           []string{"session=attacker", "other=value"},
		"Transfer-Encoding":    []string{"chunked"},
		"X-Accel-Redirect":     []string{"/protected/internal-file"},
		"X-Sendfile":           []string{"/etc/passwd"},
		"X-Lighttpd-Send-File": []string{"/protected/lighttpd-file"},
		"X-Remove-Me":          []string{"nominated by Connection"},
		"Warning":              []string{"199 first", "299 second"},
		common.RequestIdKey:    []string{"upstream-request-id"},
	}

	CopyUpstreamResponseHeaders(c, headers)

	for _, name := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Set-Cookie", "Transfer-Encoding", "X-Accel-Redirect", "X-Sendfile",
		"X-Lighttpd-Send-File", "X-Remove-Me", common.RequestIdKey,
	} {
		assert.Empty(t, recorder.Header().Values(name), name)
	}
	assert.Equal(t, []string{"199 first", "299 second"}, recorder.Header().Values("Warning"))
	upstreamRequestID, ok := c.Get(common.UpstreamRequestIdKey)
	require.True(t, ok)
	assert.Equal(t, "upstream-request-id", upstreamRequestID)
}

func TestProtectedRedirectStripsCredentialsAcrossOrigins(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = false

	req := &http.Request{
		URL:    &url.URL{Scheme: "https", Host: "cdn.example", Path: "/asset"},
		Header: make(http.Header),
	}
	for _, header := range []string{
		"Authorization",
		"Cookie",
		"Content-Type",
		"Proxy-Authorization",
		"X-Webhook-Signature",
		"X-Goog-Api-Key",
		"X-Api-Key",
	} {
		req.Header.Set(header, "secret")
	}
	req.Header.Set("Accept", "video/mp4")
	via := []*http.Request{{
		URL: &url.URL{Scheme: "https", Host: "api.example", Path: "/asset"},
	}}

	require.NoError(t, checkProtectedFetchRedirect(req, via))
	for _, header := range []string{
		"Authorization",
		"Cookie",
		"Content-Type",
		"Proxy-Authorization",
		"X-Webhook-Signature",
		"X-Goog-Api-Key",
		"X-Api-Key",
	} {
		assert.Empty(t, req.Header.Get(header))
	}
	assert.Equal(t, "video/mp4", req.Header.Get("Accept"))
}

func TestProtectedRedirectRejectsCrossOriginRequestBody(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = false

	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "redirect.example", Path: "/hook"},
		Header: make(http.Header),
		Body:   io.NopCloser(strings.NewReader(`{"secret":"payload"}`)),
	}
	via := []*http.Request{{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "origin.example", Path: "/hook"},
	}}

	err := checkProtectedFetchRedirect(req, via)

	require.ErrorContains(t, err, "cross-origin redirect with request body")
}

func TestGeneralRedirectRejectsCrossOriginRequestBody(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = false

	req := &http.Request{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "redirect.example", Path: "/worker"},
		Header: make(http.Header),
		Body:   io.NopCloser(strings.NewReader(`{"key":"worker-secret"}`)),
	}
	via := []*http.Request{{
		Method: http.MethodPost,
		URL:    &url.URL{Scheme: "https", Host: "origin.example", Path: "/worker"},
	}}

	err := checkRedirect(req, via)

	require.ErrorContains(t, err, "cross-origin redirect with request body")
}

func TestRedirectsRejectHTTPSDowngrade(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = false

	req := &http.Request{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "http", Host: "origin.example", Path: "/asset"},
		Header: make(http.Header),
	}
	via := []*http.Request{{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "https", Host: "origin.example", Path: "/asset"},
	}}

	require.ErrorContains(t, checkRedirect(req, via), "HTTPS to HTTP")
	require.ErrorContains(t, checkProtectedFetchRedirect(req, via), "HTTPS to HTTP")
}

func TestRedirectsRejectCredentialedDestinationAndInvalidRequest(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = false

	req := &http.Request{
		Method: http.MethodGet,
		URL: &url.URL{
			Scheme: "https",
			User:   url.UserPassword("redirect-user", "redirect-secret"),
			Host:   "cdn.example",
			Path:   "/asset",
		},
		Header: make(http.Header),
	}
	via := []*http.Request{{
		Method: http.MethodGet,
		URL:    &url.URL{Scheme: "https", Host: "api.example", Path: "/asset"},
	}}

	for _, redirectCheck := range []func(*http.Request, []*http.Request) error{
		checkRedirect,
		checkProtectedFetchRedirect,
	} {
		err := redirectCheck(req, via)
		require.ErrorContains(t, err, "redirect URL credentials")
		assert.NotContains(t, err.Error(), "redirect-secret")
		require.ErrorContains(t, redirectCheck(nil, via), "invalid redirect request")
	}
}

func TestProtectedRedirectErrorDoesNotExposeSignedQuery(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = true
	fetchSetting.AllowPrivateIp = false
	fetchSetting.DomainFilterMode = false
	fetchSetting.IpFilterMode = false
	fetchSetting.DomainList = nil
	fetchSetting.IpList = nil
	fetchSetting.AllowedPorts = nil
	fetchSetting.ApplyIPFilterForDomain = true

	req := &http.Request{
		URL: &url.URL{
			Scheme:   "http",
			Host:     "127.0.0.1",
			Path:     "/asset",
			RawQuery: "signature=supersecret",
		},
		Header: make(http.Header),
	}

	err := checkProtectedFetchRedirect(req, nil)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "supersecret")
}

func TestProtectedFetchURLAlwaysRequiresAbsoluteHTTPURL(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	original := *fetchSetting
	t.Cleanup(func() { *fetchSetting = original })
	fetchSetting.EnableSSRFProtection = false

	for _, rawURL := range []string{
		"",
		"/relative/path",
		"httpsx://example.com/file",
		"gopher://example.com/file",
	} {
		t.Run(rawURL, func(t *testing.T) {
			require.Error(t, ValidateSSRFProtectedFetchURL(rawURL))
		})
	}
	require.NoError(t, ValidateSSRFProtectedFetchURL("https://example.com/file"))
}
