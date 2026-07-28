package controller

import (
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func videoTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	return c, recorder
}

func TestWriteVideoDataURLRejectsActiveContent(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, recorder := videoTestContext()
	payload := base64.StdEncoding.EncodeToString([]byte("<script>alert(1)</script>"))

	err := writeVideoDataURL(c, "data:text/html;base64,"+payload)

	require.ErrorContains(t, err, "unsupported data URL media type")
	assert.Empty(t, recorder.Body.String())
}

func TestWriteVideoDataURLEnforcesResponseLimit(t *testing.T) {
	gin.SetMode(gin.TestMode)
	originalLimit := constant.MaxUpstreamResponseBodyMB
	constant.MaxUpstreamResponseBodyMB = 1
	t.Cleanup(func() { constant.MaxUpstreamResponseBodyMB = originalLimit })

	c, recorder := videoTestContext()
	payload := base64.StdEncoding.EncodeToString(make([]byte, (1<<20)+1))

	err := writeVideoDataURL(c, "data:video/mp4;base64,"+payload)

	require.ErrorContains(t, err, "response limit")
	assert.Empty(t, recorder.Body.String())
}

func TestWriteVideoDataURLUsesPrivateNonsniffResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, recorder := videoTestContext()
	payload := base64.StdEncoding.EncodeToString([]byte("video-bytes"))

	require.NoError(t, writeVideoDataURL(c, "data:video/mp4;base64,"+payload))
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Equal(t, "video/mp4", recorder.Header().Get("Content-Type"))
	assert.Equal(t, "private, max-age=86400", recorder.Header().Get("Cache-Control"))
	assert.Equal(t, "nosniff", recorder.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "video-bytes", recorder.Body.String())
}

func TestReadVideoTaskResponseRejectsNonSuccessAndOversizedBodies(t *testing.T) {
	originalLimit := constant.MaxUpstreamResponseBodyMB
	constant.MaxUpstreamResponseBodyMB = 1
	t.Cleanup(func() { constant.MaxUpstreamResponseBodyMB = originalLimit })

	tests := []struct {
		name string
		resp *http.Response
		want string
	}{
		{
			name: "non-success",
			resp: &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader(`{"uri":"https://unexpected"}`)),
			},
			want: "status 502",
		},
		{
			name: "oversized",
			resp: &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("x", (1<<20)+1))),
			},
			want: "response body exceeds",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, err := readVideoTaskResponse(test.resp)
			require.ErrorContains(t, err, test.want)
			assert.Nil(t, body)
		})
	}
}

func TestEnsureAPIKeyUsesStructuredQueryAndRejectsCredentialedURL(t *testing.T) {
	videoURL, err := ensureAPIKey(
		"https://media.example/video.mp4?monkey=value&key=stale#download",
		"fresh key",
	)
	require.NoError(t, err)
	assert.Equal(
		t,
		"https://media.example/video.mp4?key=fresh+key&monkey=value#download",
		videoURL,
	)

	_, err = ensureAPIKey("https://user:password@media.example/video.mp4", "key")
	require.ErrorContains(t, err, "credentials")
	assert.NotContains(t, err.Error(), "password")
}
