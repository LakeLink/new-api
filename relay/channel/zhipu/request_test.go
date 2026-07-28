package zhipu

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
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
}

func (r *trackingReadCloser) Read([]byte) (int, error) {
	r.read.Store(true)
	return 0, io.EOF
}

func (r *trackingReadCloser) Close() error {
	r.closed.Store(true)
	return nil
}

func TestRequestOpenAI2ZhipuPreservesOptionalTopP(t *testing.T) {
	var explicitZero dto.GeneralOpenAIRequest
	require.NoError(t, common.Unmarshal([]byte(
		`{"model":"chatglm_std","messages":[{"role":"user","content":"hello"}],"top_p":0}`,
	), &explicitZero))

	converted := requestOpenAI2Zhipu(explicitZero)
	require.NotNil(t, converted.TopP)
	assert.Zero(t, *converted.TopP)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(encoded), `"top_p":0`)

	omitted := requestOpenAI2Zhipu(dto.GeneralOpenAIRequest{
		Model:    "chatglm_std",
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	})
	assert.Nil(t, omitted.TopP)

	encoded, err = common.Marshal(omitted)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), `"top_p"`)
}

func TestZhipuStreamCancellationClosesUpstreamWithoutStartingReader(t *testing.T) {
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

	usage, apiErr := zhipuStreamHandler(ctx, nil, resp)

	require.Nil(t, apiErr)
	assert.Nil(t, usage)
	assert.False(t, body.read.Load())
	assert.True(t, body.closed.Load())
}
