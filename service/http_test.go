package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCopyUpstreamResponseHeadersFiltersIntermediaryAndCookieFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	headers := http.Header{
		"Connection":          []string{"keep-alive, X-Remove-Me"},
		"Keep-Alive":          []string{"timeout=5"},
		"Proxy-Authenticate":  []string{"Basic"},
		"Proxy-Authorization": []string{"secret"},
		"Set-Cookie":          []string{"session=attacker", "other=value"},
		"Transfer-Encoding":   []string{"chunked"},
		"X-Remove-Me":         []string{"nominated by Connection"},
		"Warning":             []string{"199 first", "299 second"},
		common.RequestIdKey:   []string{"upstream-request-id"},
	}

	CopyUpstreamResponseHeaders(c, headers)

	for _, name := range []string{
		"Connection", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization",
		"Set-Cookie", "Transfer-Encoding", "X-Remove-Me", common.RequestIdKey,
	} {
		assert.Empty(t, recorder.Header().Values(name), name)
	}
	assert.Equal(t, []string{"199 first", "299 second"}, recorder.Header().Values("Warning"))
	upstreamRequestID, ok := c.Get(common.UpstreamRequestIdKey)
	require.True(t, ok)
	assert.Equal(t, "upstream-request-id", upstreamRequestID)
}
