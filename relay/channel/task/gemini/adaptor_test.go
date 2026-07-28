package gemini

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskModelListTracksCurrentVeoEndpoints(t *testing.T) {
	models := (&TaskAdaptor{}).GetModelList()
	assert.Equal(t, []string{
		"veo-3.1-generate-preview",
		"veo-3.1-fast-generate-preview",
		"veo-3.1-lite-generate-preview",
	}, models)
}

func TestValidateVeoRequestAgainstCurrentProviderConstraints(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		model     string
		body      string
		wantError bool
	}{
		{name: "standard 720p four seconds", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":4,"metadata":{"resolution":"720p"}}`},
		{name: "standard 4k eight seconds", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":"4k"}}`},
		{name: "unsupported duration", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":5}`, wantError: true},
		{name: "fractional metadata duration", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","metadata":{"durationSeconds":4.5}}`, wantError: true},
		{name: "4k requires eight seconds", model: "veo-3.1-fast-generate-preview", body: `{"prompt":"cat","duration":6,"metadata":{"resolution":"4k"}}`, wantError: true},
		{name: "Lite rejects 4k", model: "veo-3.1-lite-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":"4k"}}`, wantError: true},
		{name: "Lite supports 1080p at eight seconds", model: "veo-3.1-lite-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":"1080p"}}`},
		{name: "first-frame image supports four seconds", model: "veo-3.1-lite-generate-preview", body: `{"prompt":"cat","duration":4,"images":["aW1hZ2U="]}`},
		{name: "unknown resolution", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":"2160p"}}`, wantError: true},
		{name: "non-string resolution", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":1080}}`, wantError: true},
		{name: "invalid aspect ratio", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"aspectRatio":"1:1"}}`, wantError: true},
		{name: "seed above uint32", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"seed":4294967296}}`, wantError: true},
		{name: "multiple outputs unsupported", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"sampleCount":2}}`, wantError: true},
		{name: "Gemini audio cannot be disabled", model: "veo-3.1-generate-preview", body: `{"prompt":"cat","duration":8,"metadata":{"generateAudio":false}}`, wantError: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", bytes.NewBufferString(tt.body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{
				ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: tt.model},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{},
			}

			taskErr := (&TaskAdaptor{}).ValidateRequestAndSetAction(ctx, info)

			if tt.wantError {
				require.NotNil(t, taskErr)
				assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
				return
			}
			require.Nil(t, taskErr)
		})
	}
}

func TestBuildVeoRequestUsesSameNormalizedBillingInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "cat",
		Duration: 6,
		Size:     "1280x720",
		Metadata: map[string]any{"durationSeconds": float64(8), "resolution": "1080P"},
	})

	body, err := (&TaskAdaptor{}).BuildRequestBody(ctx, &relaycommon.RelayInfo{})

	require.NoError(t, err)
	payloadBytes, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload VeoRequestPayload
	require.NoError(t, common.Unmarshal(payloadBytes, &payload))
	require.NotNil(t, payload.Parameters)
	assert.Equal(t, 8, payload.Parameters.DurationSeconds)
	assert.Equal(t, "1080p", payload.Parameters.Resolution)
}

func TestValidateFinalRequestRejectsCapabilitiesLostByGeminiModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"cat","duration":8,"metadata":{"resolution":"4k"}}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "veo-3.1-generate-preview"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = "veo-3.1-lite-generate-preview"
	info.IsModelMapped = true
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "invalid_resolution", taskErr.Code)
}
