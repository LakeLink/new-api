package vertex

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	geminitask "github.com/QuantumNous/new-api/relay/channel/task/gemini"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTaskModelListUsesCurrentVertexVeoEndpoints(t *testing.T) {
	assert.Equal(t, []string{
		"veo-3.1-generate-001",
		"veo-3.1-fast-generate-001",
		"veo-3.1-lite-generate-001",
	}, (&TaskAdaptor{}).GetModelList())
}

func TestValidateVertexVeoRequest(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tests := []struct {
		name      string
		model     string
		body      string
		wantError bool
	}{
		{name: "silent video", model: "veo-3.1-generate-001", body: `{"prompt":"cat","duration":4,"metadata":{"generateAudio":false}}`},
		{name: "first-frame image supports four seconds", model: "veo-3.1-fast-generate-001", body: `{"prompt":"cat","duration":4,"images":["aW1hZQ=="]}`},
		{name: "stable standard supports 4k", model: "veo-3.1-generate-001", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":"4k"}}`},
		{name: "stable fast rejects 4k", model: "veo-3.1-fast-generate-001", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":"4k"}}`, wantError: true},
		{name: "Lite rejects 4k", model: "veo-3.1-lite-generate-001", body: `{"prompt":"cat","duration":8,"metadata":{"resolution":"4k"}}`, wantError: true},
		{name: "audio flag must be boolean", model: "veo-3.1-generate-001", body: `{"prompt":"cat","duration":4,"metadata":{"generateAudio":"false"}}`, wantError: true},
		{name: "four outputs supported and billed", model: "veo-3.1-generate-001", body: `{"prompt":"cat","duration":4,"metadata":{"sampleCount":4}}`},
		{name: "more than four outputs rejected", model: "veo-3.1-generate-001", body: `{"prompt":"cat","duration":4,"metadata":{"sampleCount":5}}`, wantError: true},
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

func TestBuildVertexVeoRequestUsesNormalizedBillingInputs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "cat",
		Duration: 6,
		Size:     "1280x720",
		Metadata: map[string]any{
			"durationSeconds": float64(8),
			"resolution":      "1080P",
			"generateAudio":   false,
			"sampleCount":     float64(4),
		},
	})

	body, err := (&TaskAdaptor{}).BuildRequestBody(ctx, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	payloadBytes, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload geminitask.VeoRequestPayload
	require.NoError(t, common.Unmarshal(payloadBytes, &payload))
	require.NotNil(t, payload.Parameters)
	assert.Equal(t, 8, payload.Parameters.DurationSeconds)
	assert.Equal(t, "1080p", payload.Parameters.Resolution)
	assert.Equal(t, 4, payload.Parameters.SampleCount)
	require.NotNil(t, payload.Parameters.GenerateAudio)
	assert.False(t, *payload.Parameters.GenerateAudio)
}

func TestBuildVertexVeoRequestBoundsUnvalidatedMetadataSampleCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/v1/videos", nil)
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "cat",
		Duration: 4,
		Metadata: map[string]any{"sampleCount": float64(geminitask.MaxVeoSampleCount + 1)},
	})

	body, err := (&TaskAdaptor{}).BuildRequestBody(ctx, &relaycommon.RelayInfo{})
	require.NoError(t, err)
	payloadBytes, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload geminitask.VeoRequestPayload
	require.NoError(t, common.Unmarshal(payloadBytes, &payload))
	require.NotNil(t, payload.Parameters)
	assert.Equal(t, 1, payload.Parameters.SampleCount)
}

func TestEstimateVertexVeoBillingIncludesSilentVideoRate(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Duration: 8,
		Metadata: map[string]any{"resolution": "4k", "generateAudio": false},
	})
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "veo-3.1-generate-001"}}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	assert.Equal(t, 8.0, ratios["seconds"])
	assert.Equal(t, 1.5, ratios["resolution"])
	assert.InDelta(t, 2.0/3.0, ratios["audio"], 1e-12)
	assert.Equal(t, 1.0, ratios["outputs"])
}

func TestEstimateVertexVeoBillingMultipliesOutputCount(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Duration: 4,
		Metadata: map[string]any{"sampleCount": float64(4)},
	})
	info := &relaycommon.RelayInfo{ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "veo-3.1-generate-001"}}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	assert.Equal(t, 4.0, ratios["outputs"])
}

func TestVertexTaskContextTypeMismatchFailsClosed(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", "stale-context-value")
	adaptor := &TaskAdaptor{}

	assert.Nil(t, adaptor.EstimateBilling(ctx, &relaycommon.RelayInfo{}))
	body, err := adaptor.BuildRequestBody(ctx, &relaycommon.RelayInfo{})
	require.ErrorContains(t, err, "unexpected task_request type")
	assert.Nil(t, body)
}

func TestValidateFinalRequestRejectsCapabilitiesLostByVertexModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"cat","duration":8,"metadata":{"resolution":"4k"}}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "veo-3.1-generate-001"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = "veo-3.1-fast-generate-001"
	info.IsModelMapped = true
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "invalid_resolution", taskErr.Code)
}
