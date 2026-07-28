package coze

import (
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/dto"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCozeRequestGeneratesValidDefaultUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("bot_id", "bot-1")

	converted, err := convertCozeChatRequest(context, dto.GeneralOpenAIRequest{
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	})

	require.NoError(t, err)
	assert.NotEmpty(t, converted.UserId)
	_, err = common.Marshal(converted)
	require.NoError(t, err)
}

func TestCozeRequestPreservesExplicitUserAndFalseStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	stream := false

	converted, err := convertCozeChatRequest(context, dto.GeneralOpenAIRequest{
		User:     []byte(`"customer-123"`),
		Stream:   &stream,
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	})

	require.NoError(t, err)
	encoded, err := common.Marshal(converted)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, common.Unmarshal(encoded, &payload))
	assert.Equal(t, "customer-123", payload["user_id"])
	assert.Equal(t, false, payload["stream"])
}

func TestCozeRequestRejectsNonStringUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())

	_, err := convertCozeChatRequest(context, dto.GeneralOpenAIRequest{
		User:     []byte(`123`),
		Messages: []dto.Message{{Role: "user", Content: "hello"}},
	})

	require.ErrorContains(t, err, "JSON string")
}
