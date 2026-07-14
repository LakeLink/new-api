package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestDecompressRequestMiddlewareRejectsDeclaredOversizedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldLimit := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = oldLimit })

	called := false
	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.POST("/api", func(c *gin.Context) {
		called = true
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("x"))
	request.ContentLength = (1 << 20) + 1
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
	require.False(t, called)
}

func TestDecompressRequestMiddlewareBoundsChunkedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldLimit := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = oldLimit })

	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.POST("/api", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		require.True(t, common.IsRequestBodyTooLargeError(err))
		c.Status(http.StatusRequestEntityTooLarge)
	})
	request := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	request.ContentLength = -1
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
}
