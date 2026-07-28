package middleware

import (
	"bytes"
	"compress/gzip"
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

func TestDecompressRequestMiddlewareBoundsGetBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldLimit := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = oldLimit })

	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.GET("/api", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		require.True(t, common.IsRequestBodyTooLargeError(err))
		c.Status(http.StatusRequestEntityTooLarge)
	})
	request := httptest.NewRequest(http.MethodGet, "/api", strings.NewReader(strings.Repeat("x", (1<<20)+1)))
	request.ContentLength = -1
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
}

func TestDecompressRequestMiddlewareNormalizesEncodingAndLength(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldLimit := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = oldLimit })

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write([]byte("decompressed"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.POST("/api", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		require.NoError(t, err)
		require.Equal(t, "decompressed", string(body))
		require.Empty(t, c.GetHeader("Content-Encoding"))
		require.Equal(t, int64(-1), c.Request.ContentLength)
		c.Status(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/api", bytes.NewReader(compressed.Bytes()))
	request.Header.Set("Content-Encoding", " GZip ")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestDecompressRequestMiddlewareBoundsDecompressedBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	oldLimit := constant.MaxRequestBodyMB
	constant.MaxRequestBodyMB = 1
	t.Cleanup(func() { constant.MaxRequestBodyMB = oldLimit })

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, err := writer.Write([]byte(strings.Repeat("x", (1<<20)+1)))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	require.Less(t, compressed.Len(), 1<<20)

	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.POST("/api", func(c *gin.Context) {
		_, err := io.ReadAll(c.Request.Body)
		require.True(t, common.IsRequestBodyTooLargeError(err))
		c.Status(http.StatusRequestEntityTooLarge)
	})
	request := httptest.NewRequest(http.MethodPost, "/api", bytes.NewReader(compressed.Bytes()))
	request.Header.Set("Content-Encoding", "gzip")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusRequestEntityTooLarge, recorder.Code)
}

func TestDecompressRequestMiddlewareRejectsUnsupportedEncoding(t *testing.T) {
	gin.SetMode(gin.TestMode)
	called := false
	router := gin.New()
	router.Use(DecompressRequestMiddleware())
	router.POST("/api", func(c *gin.Context) {
		called = true
	})
	request := httptest.NewRequest(http.MethodPost, "/api", strings.NewReader("body"))
	request.Header.Set("Content-Encoding", "zstd")
	recorder := httptest.NewRecorder()

	router.ServeHTTP(recorder, request)

	require.Equal(t, http.StatusUnsupportedMediaType, recorder.Code)
	require.False(t, called)
}
