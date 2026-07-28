package palm

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

func TestPaLMAdaptorMaterializesGenerateMessageProtocol(t *testing.T) {
	n := 2
	topK := 0
	topP := 0.0
	request := &dto.GeneralOpenAIRequest{
		Model: "chat-bison-001",
		Messages: []dto.Message{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "hi"},
		},
		N:    &n,
		TopK: &topK,
		TopP: &topP,
	}

	convertedValue, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)
	require.NoError(t, err)
	converted, ok := convertedValue.(*PaLMChatRequest)
	require.True(t, ok)
	require.Len(t, converted.Prompt.Messages, 2)
	assert.Equal(t, "0", converted.Prompt.Messages[0].Author)
	assert.Equal(t, "1", converted.Prompt.Messages[1].Author)

	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Contains(t, payload, "prompt")
	assert.Equal(t, float64(2), payload["candidateCount"])
	assert.Equal(t, float64(0), payload["topK"])
	assert.Equal(t, float64(0), payload["topP"])
	assert.NotContains(t, payload, "model")
	assert.NotContains(t, payload, "messages")
}

func TestPaLMStreamCancellationClosesUpstreamWithoutStartingReader(t *testing.T) {
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

	apiErr, responseText := palmStreamHandler(ctx, resp)

	require.Nil(t, apiErr)
	assert.Empty(t, responseText)
	assert.False(t, body.read.Load())
	assert.True(t, body.closed.Load())
}
