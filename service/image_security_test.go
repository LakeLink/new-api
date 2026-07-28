package service

import (
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/setting/system_setting"
	"github.com/stretchr/testify/require"
)

type imageTestRoundTripper func(*http.Request) (*http.Response, error)

func (f imageTestRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type countingReadCloser struct {
	reader io.Reader
	read   int
}

func (r *countingReadCloser) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	r.read += n
	return n, err
}

func (r *countingReadCloser) Close() error {
	return nil
}

func TestDecodeBase64ImageDataRejectsOversizedDecodedImage(t *testing.T) {
	originalLimit := constant.MaxFileDownloadMB
	constant.MaxFileDownloadMB = 1
	t.Cleanup(func() { constant.MaxFileDownloadMB = originalLimit })

	oversized := make([]byte, (1<<20)+1)
	_, _, _, err := DecodeBase64ImageData(base64.StdEncoding.EncodeToString(oversized))

	require.ErrorContains(t, err, "image size exceeds")
}

func TestDecodeURLImageDataReadsOnlyImageHeaderBudget(t *testing.T) {
	fetchSetting := system_setting.GetFetchSetting()
	originalFetchSetting := *fetchSetting
	originalHTTPClient := httpClient
	originalProtectedClient := ssrfProtectedHTTPClient
	originalWorkerURL := system_setting.WorkerUrl
	t.Cleanup(func() {
		*fetchSetting = originalFetchSetting
		httpClient = originalHTTPClient
		ssrfProtectedHTTPClient = originalProtectedClient
		system_setting.WorkerUrl = originalWorkerURL
	})
	fetchSetting.EnableSSRFProtection = false
	system_setting.WorkerUrl = ""

	body := &countingReadCloser{reader: strings.NewReader(strings.Repeat("x", 1<<20))}
	client := &http.Client{Transport: imageTestRoundTripper(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Status:     "200 OK",
			Header:     http.Header{"Content-Type": []string{"image/png"}},
			Body:       body,
			Request:    req,
		}, nil
	})}
	httpClient = client
	ssrfProtectedHTTPClient = client

	_, _, err := DecodeUrlImageData("https://images.example/invalid.png")

	require.Error(t, err)
	require.LessOrEqual(t, body.read, 64<<10)
}
