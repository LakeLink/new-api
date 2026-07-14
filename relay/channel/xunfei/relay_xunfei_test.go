package xunfei

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRequestOpenAIToXunfeiPreservesSupportedSystemRoleAndTopK(t *testing.T) {
	topK := 3
	n := 6
	request := dto.GeneralOpenAIRequest{
		Model: "SparkDesk-v3.5",
		Messages: []dto.Message{
			{Role: "system", Content: "answer tersely"},
			{Role: "user", Content: "hello"},
		},
		TopK: &topK,
		N:    &n,
	}

	converted := requestOpenAI2Xunfei(request, "app-id", "generalv3.5")
	require.Len(t, converted.Payload.Message.Text, 2)
	assert.Equal(t, "system", converted.Payload.Message.Text[0].Role)
	assert.Equal(t, "answer tersely", converted.Payload.Message.Text[0].Content)
	assert.Equal(t, 3, converted.Parameter.Chat.TopK, "OpenAI n must not be repurposed as Spark top_k")
}

func TestRequestOpenAIToXunfeiDoesNotFabricateAssistantMessage(t *testing.T) {
	request := dto.GeneralOpenAIRequest{
		Messages: []dto.Message{
			{Role: "system", Content: "answer tersely"},
			{Role: "user", Content: "hello"},
		},
	}

	converted := requestOpenAI2Xunfei(request, "app-id", "lite")
	require.Len(t, converted.Payload.Message.Text, 1)
	assert.Equal(t, "user", converted.Payload.Message.Text[0].Role)
	assert.Equal(t, "answer tersely\n\nhello", converted.Payload.Message.Text[0].Content)
	for _, message := range converted.Payload.Message.Text {
		assert.NotEqual(t, "assistant", message.Role)
		assert.NotEqual(t, "Okay", message.Content)
	}
}

func TestXunfeiAdaptorRejectsOutOfRangeTopK(t *testing.T) {
	for _, topK := range []int{0, 7} {
		request := &dto.GeneralOpenAIRequest{TopK: &topK}
		_, err := (&Adaptor{}).ConvertOpenAIRequest(nil, nil, request)
		require.ErrorContains(t, err, "between 1 and 6")
	}
}

func TestXunfeiResponseErrorUsesProviderHeader(t *testing.T) {
	response := XunfeiChatResponse{}
	response.Header.Code = 10013
	response.Header.Message = "invalid app id"
	response.Header.Sid = "spark-session-1"

	err := xunfeiResponseError(&response)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "10013")
	assert.Contains(t, err.Error(), "invalid app id")
	assert.Contains(t, err.Error(), "spark-session-1")
	assert.NoError(t, xunfeiResponseError(&XunfeiChatResponse{}))
}

func TestXunfeiMakeRequestSurfacesProviderErrorFrame(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade websocket: %v", err)
			return
		}
		defer conn.Close()
		_, _, err = conn.ReadMessage()
		if err != nil {
			t.Errorf("read websocket request: %v", err)
			return
		}

		response := XunfeiChatResponse{}
		response.Header.Code = 10007
		response.Header.Message = "upstream rejected request"
		response.Header.Sid = "spark-session-2"
		body, err := common.Marshal(response)
		if err != nil {
			t.Errorf("marshal websocket response: %v", err)
			return
		}
		if err := conn.WriteMessage(websocket.TextMessage, body); err != nil {
			t.Errorf("write websocket response: %v", err)
		}
	}))
	t.Cleanup(server.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	results, err := xunfeiMakeRequest(ctx, dto.GeneralOpenAIRequest{}, "lite", wsURL, "app-id")
	require.NoError(t, err)

	select {
	case result, ok := <-results:
		require.True(t, ok)
		require.Error(t, result.Err)
		assert.Contains(t, result.Err.Error(), "10007")
		assert.Contains(t, result.Err.Error(), "upstream rejected request")
	case <-ctx.Done():
		t.Fatal("timed out waiting for Xunfei error result")
	}
}
