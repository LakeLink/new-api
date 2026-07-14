package common

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestCleanupMultipartFormsRemovesTemporaryFiles(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("TMPDIR", t.TempDir())
	oldFileLimit := constant.MaxFileDownloadMB
	oldBodyLimit := constant.MaxRequestBodyMB
	constant.MaxFileDownloadMB = 1
	constant.MaxRequestBodyMB = 4
	t.Cleanup(func() {
		constant.MaxFileDownloadMB = oldFileLimit
		constant.MaxRequestBodyMB = oldBodyLimit
	})

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "large.bin")
	require.NoError(t, err)
	_, err = part.Write(bytes.Repeat([]byte("x"), (1<<20)+1))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(body.Bytes()))
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	form, err := ParseMultipartFormReusable(c)
	require.NoError(t, err)
	require.Len(t, form.File["file"], 1)
	file, err := form.File["file"][0].Open()
	require.NoError(t, err)
	namedFile, ok := file.(interface{ Name() string })
	require.True(t, ok)
	tempPath := namedFile.Name()
	require.NoError(t, file.Close())
	_, err = os.Stat(tempPath)
	require.NoError(t, err)

	CleanupMultipartForms(c)

	_, err = os.Stat(tempPath)
	require.ErrorIs(t, err, os.ErrNotExist)
	require.NotPanics(t, func() { CleanupMultipartForms(c) })
	CleanupBodyStorage(c)
}
