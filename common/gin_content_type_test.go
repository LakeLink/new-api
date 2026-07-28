package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUnmarshalBodyReusableContentTypeHandling(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name        string
		contentType string
		wantErr     bool
	}{
		{
			name:        "standard JSON",
			contentType: "application/json; charset=utf-8",
		},
		{
			name:        "structured JSON suffix",
			contentType: "application/problem+json",
		},
		{
			name: "missing content type remains JSON compatible",
		},
		{
			name:        "unsupported media type fails closed",
			contentType: "text/plain",
			wantErr:     true,
		},
		{
			name:        "non application JSON suffix fails closed",
			contentType: "text/problem+json",
			wantErr:     true,
		},
		{
			name:        "multipart without boundary fails closed",
			contentType: "multipart/form-data",
			wantErr:     true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			c, _ := gin.CreateTestContext(nil)
			c.Request = httptest.NewRequest(
				http.MethodPost,
				"/v1/chat/completions",
				strings.NewReader(`{"model":"gpt-test"}`),
			)
			if test.contentType != "" {
				c.Request.Header.Set("Content-Type", test.contentType)
			}

			var decoded struct {
				Model string `json:"model"`
			}
			err := UnmarshalBodyReusable(c, &decoded)
			if test.wantErr {
				require.Error(t, err)
				assert.Empty(t, decoded.Model)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "gpt-test", decoded.Model)
			}
			CleanupBodyStorage(c)
		})
	}
}

func TestParseMultipartFormReusableRejectsStaleContentTypeContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(nil)
	c.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/images/edits",
		strings.NewReader("--boundary--\r\n"),
	)
	c.Request.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	c.Set("_original_multipart_ct", 123)

	var err error
	require.NotPanics(t, func() {
		_, err = ParseMultipartFormReusable(c)
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid cached multipart content type")
	CleanupBodyStorage(c)
}
