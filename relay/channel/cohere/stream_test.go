package cohere

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type disconnectedResponseRecorder struct {
	*httptest.ResponseRecorder
	disconnected chan bool
}

func (r *disconnectedResponseRecorder) CloseNotify() <-chan bool {
	return r.disconnected
}

type trackingReadCloser struct {
	read   atomic.Bool
	closed atomic.Bool
	err    error
}

func (r *trackingReadCloser) Read([]byte) (int, error) {
	r.read.Store(true)
	if r.err != nil {
		return 0, r.err
	}
	return 0, io.EOF
}

func (r *trackingReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func TestCohereStreamCancellationClosesUpstreamWithoutStartingReader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	disconnected := make(chan bool)
	close(disconnected)
	writer := &disconnectedResponseRecorder{
		ResponseRecorder: httptest.NewRecorder(),
		disconnected:     disconnected,
	}
	ctx, _ := gin.CreateTestContext(writer)
	body := &trackingReadCloser{}
	resp := &http.Response{Body: body}
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "command-a-03-2025"}}

	usage, apiErr := cohereStreamHandler(ctx, info, resp)

	require.Nil(t, apiErr)
	require.NotNil(t, usage)
	assert.False(t, body.read.Load())
	assert.True(t, body.closed.Load())
}

func TestCohereBufferedReadErrorClosesUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	body := &trackingReadCloser{err: assert.AnError}
	resp := &http.Response{Body: body}

	usage, apiErr := cohereHandler(ctx, &relaycommon.RelayInfo{}, resp)

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.ErrorIs(t, apiErr.Err, assert.AnError)
	assert.True(t, body.read.Load())
	assert.True(t, body.closed.Load())
}
