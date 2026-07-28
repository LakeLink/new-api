package service

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/system_setting"

	"golang.org/x/net/proxy"
)

var (
	httpClient              *http.Client
	ssrfProtectedHTTPClient *http.Client
	proxyClientLock         sync.Mutex
	proxyClients            = make(map[string]*http.Client)
)

const (
	defaultRelayIdleConnTimeout  = 90 * time.Second
	defaultRelayMaxIdleConns     = 500
	defaultRelayMaxIdleConnsHost = 100
	invalidRelayTimeoutFallback  = 60 * time.Second
)

func relayRequestTimeout() time.Duration {
	if common.RelayTimeout == 0 {
		return 0
	}
	return common.SafeIntervalDuration(
		common.RelayTimeout,
		time.Second,
		invalidRelayTimeoutFallback,
		"relay request timeout",
	)
}

func relayTransportPoolSettings() (int, int, time.Duration) {
	maxIdleConns := common.RelayMaxIdleConns
	if maxIdleConns < 0 {
		common.SysError(fmt.Sprintf(
			"relay max idle connections %d is invalid; using %d",
			maxIdleConns,
			defaultRelayMaxIdleConns,
		))
		maxIdleConns = defaultRelayMaxIdleConns
	}

	maxIdleConnsPerHost := common.RelayMaxIdleConnsPerHost
	if maxIdleConnsPerHost < 0 {
		common.SysError(fmt.Sprintf(
			"relay max idle connections per host %d is invalid; using %d",
			maxIdleConnsPerHost,
			defaultRelayMaxIdleConnsHost,
		))
		maxIdleConnsPerHost = defaultRelayMaxIdleConnsHost
	}

	idleConnTimeout := time.Duration(0)
	if common.RelayIdleConnTimeout != 0 {
		idleConnTimeout = common.SafeIntervalDuration(
			common.RelayIdleConnTimeout,
			time.Second,
			defaultRelayIdleConnTimeout,
			"relay idle connection timeout",
		)
	}
	return maxIdleConns, maxIdleConnsPerHost, idleConnTimeout
}

func newRelayTransport(proxyFunc func(*http.Request) (*url.URL, error), dialContext func(context.Context, string, string) (net.Conn, error)) *http.Transport {
	if dialContext == nil {
		dialContext = (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}
	maxIdleConns, maxIdleConnsPerHost, idleConnTimeout := relayTransportPoolSettings()
	transport := &http.Transport{
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		ForceAttemptHTTP2:     true,
		Proxy:                 proxyFunc,
		DialContext:           dialContext,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: time.Second,
	}
	if common.TLSInsecureSkipVerify {
		transport.TLSClientConfig = common.InsecureTLSConfig
	}
	return transport
}

func proxyClientCacheKey(proxyURL *url.URL) string {
	digest := sha256.Sum256([]byte(proxyURL.String()))
	return fmt.Sprintf("proxy:%x", digest)
}

func cacheProxyClient(cacheKey string, client *http.Client) *http.Client {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	if existing, ok := proxyClients[cacheKey]; ok {
		client.CloseIdleConnections()
		return existing
	}
	proxyClients[cacheKey] = client
	return client
}

func redirectCrossesOrigin(req *http.Request, via []*http.Request) bool {
	if req == nil || req.URL == nil || len(via) == 0 || via[0] == nil || via[0].URL == nil {
		return false
	}
	return !strings.EqualFold(req.URL.Scheme, via[0].URL.Scheme) ||
		!strings.EqualFold(req.URL.Host, via[0].URL.Host)
}

func redirectDowngradesTLS(req *http.Request, via []*http.Request) bool {
	if len(via) == 0 {
		return false
	}
	previous := via[len(via)-1]
	return req != nil && req.URL != nil && len(via) > 0 &&
		previous != nil && previous.URL != nil &&
		strings.EqualFold(previous.URL.Scheme, "https") &&
		strings.EqualFold(req.URL.Scheme, "http")
}

func rejectUnsafeRedirect(req *http.Request, via []*http.Request) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("invalid redirect request")
	}
	if req.URL.User != nil {
		return fmt.Errorf("redirect URL credentials are not allowed")
	}
	if redirectDowngradesTLS(req, via) {
		return fmt.Errorf("HTTPS to HTTP redirect is not allowed")
	}
	if redirectCrossesOrigin(req, via) && req.Body != nil && req.Body != http.NoBody {
		return fmt.Errorf("cross-origin redirect with request body is not allowed")
	}
	return nil
}

func stripSensitiveRedirectHeaders(req *http.Request, via []*http.Request) {
	if !redirectCrossesOrigin(req, via) {
		return
	}
	safeHeaders := map[string]struct{}{
		"Accept":          {},
		"Accept-Encoding": {},
		"Range":           {},
		"User-Agent":      {},
	}
	for header := range req.Header {
		if _, safe := safeHeaders[http.CanonicalHeaderKey(header)]; !safe {
			req.Header.Del(header)
		}
	}
}

func checkRedirect(req *http.Request, via []*http.Request) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("invalid redirect request")
	}
	if err := rejectUnsafeRedirect(req, via); err != nil {
		return err
	}
	urlStr := req.URL.String()
	if err := validateURLWithCurrentFetchSetting(urlStr, true); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", common.MaskSensitiveInfo(urlStr), err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	stripSensitiveRedirectHeaders(req, via)
	return nil
}

func checkProtectedFetchRedirect(req *http.Request, via []*http.Request) error {
	if req == nil || req.URL == nil {
		return fmt.Errorf("invalid redirect request")
	}
	if err := rejectUnsafeRedirect(req, via); err != nil {
		return err
	}
	urlStr := req.URL.String()
	if err := ValidateSSRFProtectedFetchURL(urlStr); err != nil {
		return fmt.Errorf("redirect to %s blocked: %v", common.MaskSensitiveInfo(urlStr), err)
	}
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	stripSensitiveRedirectHeaders(req, via)
	return nil
}

func validateURLWithCurrentFetchSetting(urlStr string, applyDomainIPFilter bool) error {
	if _, err := common.ParseAbsoluteHTTPURL(urlStr); err != nil {
		return err
	}
	fetchSetting := system_setting.GetFetchSetting()
	return common.ValidateURLWithFetchSetting(urlStr, fetchSetting.EnableSSRFProtection, fetchSetting.AllowPrivateIp, fetchSetting.DomainFilterMode, fetchSetting.IpFilterMode, fetchSetting.DomainList, fetchSetting.IpList, fetchSetting.AllowedPorts, applyDomainIPFilter && fetchSetting.ApplyIPFilterForDomain)
}

func ValidateSSRFProtectedFetchURL(urlStr string) error {
	return validateURLWithCurrentFetchSetting(urlStr, true)
}

func InitHttpClient() {
	httpClient = &http.Client{
		Transport:     newRelayTransport(http.ProxyFromEnvironment, nil),
		Timeout:       relayRequestTimeout(),
		CheckRedirect: checkRedirect,
	}
	ssrfProtectedHTTPClient = newProtectedFetchHTTPClient()
}

// GetHttpClient returns the general outbound client used by relay/provider
// integrations. Do not attach the SSRF-protected dialer here: provider base URLs
// are root/operator-managed deployment targets, not arbitrary user-controlled
// input, and may legitimately point at private networks, private-link endpoints,
// self-hosted services, or local proxies. Code paths that fetch arbitrary
// user-controlled URLs must use GetSSRFProtectedHTTPClient or
// ValidateSSRFProtectedFetchURL instead.
func GetHttpClient() *http.Client {
	return httpClient
}

// GetHttpClientWithTimeout returns an independent client configuration that
// shares the relay transport and redirect policy while applying a per-operation
// total timeout. It is suitable for bounded provider control-plane requests.
func GetHttpClientWithTimeout(timeout time.Duration) *http.Client {
	baseClient := GetHttpClient()
	if baseClient == nil {
		return &http.Client{
			Transport:     newRelayTransport(http.ProxyFromEnvironment, nil),
			Timeout:       timeout,
			CheckRedirect: checkRedirect,
		}
	}
	return &http.Client{
		Transport:     baseClient.Transport,
		CheckRedirect: baseClient.CheckRedirect,
		Jar:           baseClient.Jar,
		Timeout:       timeout,
	}
}

// GetSSRFProtectedHTTPClient 返回带拨号时 SSRF 校验和独立请求超时的客户端。
// 即使保护被关闭也保留此专用客户端；它会按配置退化为普通传输，但不会
// 让任意 URL 获取继承 RelayTimeout=0 的无限等待语义。
func GetSSRFProtectedHTTPClient() *http.Client {
	if ssrfProtectedHTTPClient != nil {
		return ssrfProtectedHTTPClient
	}
	return newProtectedFetchHTTPClient()
}

// GetHttpClientWithProxy returns the default client or a proxy-enabled one when proxyURL is provided.
func GetHttpClientWithProxy(proxyURL string) (*http.Client, error) {
	return NewProxyHttpClient(proxyURL)
}

// ResetProxyClientCache 清空代理客户端缓存，确保下次使用时重新初始化
func ResetProxyClientCache() {
	proxyClientLock.Lock()
	defer proxyClientLock.Unlock()
	for _, client := range proxyClients {
		if transport, ok := client.Transport.(*http.Transport); ok && transport != nil {
			transport.CloseIdleConnections()
		}
	}
	proxyClients = make(map[string]*http.Client)
}

// NewProxyHttpClient 创建支持代理的 HTTP 客户端
func NewProxyHttpClient(proxyURL string) (*http.Client, error) {
	if proxyURL == "" {
		if client := GetHttpClient(); client != nil {
			return client, nil
		}
		return GetHttpClientWithTimeout(relayRequestTimeout()), nil
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy URL: %s", common.MaskSensitiveInfo(err.Error()))
	}
	parsedURL.Scheme = strings.ToLower(parsedURL.Scheme)
	if parsedURL.Hostname() == "" {
		return nil, fmt.Errorf("invalid proxy URL: host is required")
	}
	cacheKey := proxyClientCacheKey(parsedURL)
	proxyClientLock.Lock()
	if client, ok := proxyClients[cacheKey]; ok {
		proxyClientLock.Unlock()
		return client, nil
	}
	proxyClientLock.Unlock()

	switch parsedURL.Scheme {
	case "http", "https":
		client := &http.Client{
			Transport:     newRelayTransport(http.ProxyURL(parsedURL), nil),
			Timeout:       relayRequestTimeout(),
			CheckRedirect: checkRedirect,
		}
		return cacheProxyClient(cacheKey, client), nil

	case "socks5", "socks5h":
		// 获取认证信息
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{
				User:     parsedURL.User.Username(),
				Password: "",
			}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}

		// 创建 SOCKS5 代理拨号器
		// proxy.SOCKS5 使用 tcp 参数，所有 TCP 连接包括 DNS 查询都将通过代理进行。行为与 socks5h 相同
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("failed to configure SOCKS5 proxy: %w", SanitizeNetworkError(err))
		}
		contextDialer, ok := dialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 proxy dialer does not support request cancellation")
		}

		client := &http.Client{
			Transport:     newRelayTransport(nil, contextDialer.DialContext),
			Timeout:       relayRequestTimeout(),
			CheckRedirect: checkRedirect,
		}
		return cacheProxyClient(cacheKey, client), nil

	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s, must be http, https, socks5 or socks5h", parsedURL.Scheme)
	}
}
