package service

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"

	"github.com/gin-gonic/gin"
)

type sanitizedNetworkError struct {
	message string
	cause   error
}

func (e *sanitizedNetworkError) Error() string {
	return e.message
}

func (e *sanitizedNetworkError) Unwrap() error {
	return e.cause
}

var hopByHopResponseHeaders = map[string]struct{}{
	"connection":          {},
	"keep-alive":          {},
	"proxy-authenticate":  {},
	"proxy-authorization": {},
	"proxy-connection":    {},
	"te":                  {},
	"trailer":             {},
	"transfer-encoding":   {},
	"upgrade":             {},
	// Reverse-proxy internal redirect headers can make a fronting web server
	// serve local or otherwise protected resources if relayed from an upstream.
	"x-accel-redirect":     {},
	"x-sendfile":           {},
	"x-lighttpd-send-file": {},
	"x-lighttpd-send-temp": {},
}

func CloseResponseBodyGracefully(httpResponse *http.Response) {
	if httpResponse == nil || httpResponse.Body == nil {
		return
	}
	err := httpResponse.Body.Close()
	if err != nil {
		common.SysError("failed to close response body: " + err.Error())
	}
}

// SanitizeNetworkError removes credentials, query values, and log-control
// characters from transport errors before they cross a provider boundary.
func SanitizeNetworkError(err error) error {
	if err == nil {
		return nil
	}
	message := common.MaskSensitiveInfo(err.Error())
	message = strings.Map(func(character rune) rune {
		if character < 0x20 || character == 0x7f {
			return ' '
		}
		return character
	}, message)
	runes := []rune(message)
	if len(runes) > common.LocalLogContentLimit {
		message = string(runes[:common.LocalLogContentLimit]) + "... [truncated]"
	}
	return &sanitizedNetworkError{
		message: message,
		cause:   err,
	}
}

// DoUpstreamRequest executes an HTTP request while ensuring the returned
// transport error cannot disclose URL credentials or API-key query values.
func DoUpstreamRequest(client *http.Client, request *http.Request) (*http.Response, error) {
	if client == nil {
		return nil, errors.New("upstream HTTP client is nil")
	}
	if request == nil {
		return nil, errors.New("upstream HTTP request is nil")
	}
	response, err := client.Do(request)
	if err != nil {
		return response, SanitizeNetworkError(err)
	}
	return response, nil
}

// ReadResponseBodyWithLimit buffers an HTTP response while enforcing a hard
// byte ceiling before an upstream can drive an unbounded allocation.
func ReadResponseBodyWithLimit(body io.Reader, maxBytes int64) ([]byte, error) {
	if body == nil {
		return nil, fmt.Errorf("response body is nil")
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("response body limit must be positive")
	}
	data, err := io.ReadAll(io.LimitReader(body, common.ReadLimitWithOverrunByte(maxBytes)))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("response body exceeds %d bytes", maxBytes)
	}
	return data, nil
}

// ReadUpstreamResponseBody applies the configured non-stream upstream response
// limit, with the same 128 MiB fallback used by the relay transport.
func ReadUpstreamResponseBody(body io.Reader) ([]byte, error) {
	maxMB := constant.MaxUpstreamResponseBodyMB
	if maxMB <= 0 {
		maxMB = 128
	}
	return ReadResponseBodyWithLimit(body, common.BytesFromMegabytes(maxMB))
}

// ShouldCopyUpstreamHeader checks whether a given upstream response header
// should be copied to the client response. It returns false for Content-Length
// (managed separately) and X-Oneapi-Request-Id (to preserve the local instance
// ID). When the upstream header is X-Oneapi-Request-Id, the value is captured
// into the Gin context for later logging.
func ShouldCopyUpstreamHeader(c *gin.Context, k string, v []string) bool {
	if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Set-Cookie") {
		return false
	}
	if _, blocked := hopByHopResponseHeaders[strings.ToLower(k)]; blocked {
		return false
	}
	if strings.EqualFold(k, common.RequestIdKey) {
		if c != nil && len(v) > 0 {
			c.Set(common.UpstreamRequestIdKey, v[0])
		}
		return false
	}
	return true
}

// CopyUpstreamResponseHeaders copies end-to-end response headers while
// preserving repeated values. Hop-by-hop fields, upstream cookies, and fields
// nominated by Connection are never forwarded by the gateway.
func CopyUpstreamResponseHeaders(c *gin.Context, headers http.Header) {
	if c == nil || c.Writer == nil {
		return
	}
	connectionHeaders := make(map[string]struct{})
	for _, value := range headers.Values("Connection") {
		for _, name := range strings.Split(value, ",") {
			if name = strings.TrimSpace(name); name != "" {
				connectionHeaders[strings.ToLower(name)] = struct{}{}
			}
		}
	}
	for name, values := range headers {
		if _, blocked := connectionHeaders[strings.ToLower(name)]; blocked || !ShouldCopyUpstreamHeader(c, name, values) {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}
}

func IOCopyBytesGracefully(c *gin.Context, src *http.Response, data []byte) {
	if c.Writer == nil {
		return
	}

	body := io.NopCloser(bytes.NewBuffer(data))

	// We shouldn't set the header before we parse the response body, because the parse part may fail.
	// And then we will have to send an error response, but in this case, the header has already been set.
	// So the httpClient will be confused by the response.
	// For example, Postman will report error, and we cannot check the response at all.
	if src != nil {
		CopyUpstreamResponseHeaders(c, src.Header)
	}

	// set Content-Length header manually BEFORE calling WriteHeader
	c.Writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write header with status code (this sends the headers)
	if src != nil {
		c.Writer.WriteHeader(src.StatusCode)
	} else {
		c.Writer.WriteHeader(http.StatusOK)
	}

	_, err := io.Copy(c.Writer, body)
	if err != nil {
		logger.LogError(c, fmt.Sprintf("failed to copy response body: %s", err.Error()))
	}
	c.Writer.Flush()
}
