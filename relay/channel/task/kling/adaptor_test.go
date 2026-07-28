package kling

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToRequestPayloadLocksKlingModelAliases(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt: "waves breaking over rocks",
		Metadata: map[string]interface{}{
			"model":      "a-different-priced-model",
			"model_name": "another-model",
			"duration":   "10",
		},
	}

	converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v2-master"},
	})

	require.NoError(t, err)
	assert.Equal(t, "kling-v2-master", converted.Model)
	assert.Equal(t, "kling-v2-master", converted.ModelName)
	assert.Equal(t, "10", converted.Duration)
}

func TestConvertToRequestPayloadMapsKlingImageArray(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt: "waves breaking over rocks",
		Images: []string{
			"https://example.com/first.png",
			"https://example.com/last.png",
		},
	}

	converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v2-master"},
	})

	require.NoError(t, err)
	assert.Equal(t, "https://example.com/first.png", converted.Image)
	assert.Equal(t, "https://example.com/last.png", converted.ImageTail)
}

func TestConvertToRequestPayloadValidatesLegacyKlingDuration(t *testing.T) {
	for _, duration := range []string{"", "4", "15", "not-a-number"} {
		t.Run(duration, func(t *testing.T) {
			adaptor := &TaskAdaptor{}
			request := relaycommon.TaskSubmitReq{
				Prompt: "waves breaking over rocks",
				Metadata: map[string]interface{}{
					"duration": duration,
				},
			}

			_, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v2-master"},
			})

			require.Error(t, err)
		})
	}
}

func TestConvertToRequestPayloadValidatesKlingV3Duration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	for _, duration := range []int{3, 15} {
		request := relaycommon.TaskSubmitReq{Prompt: "waves breaking over rocks", Duration: duration}
		converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v3"},
		})
		require.NoError(t, err)
		assert.Equal(t, strconv.Itoa(duration), converted.Duration)
	}
	for _, duration := range []int{1, 2, 16} {
		request := relaycommon.TaskSubmitReq{Prompt: "waves breaking over rocks", Duration: duration}
		_, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
			ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v3"},
		})
		require.Error(t, err)
	}
}

func TestConvertToRequestPayloadRejectsModeForKlingMaster(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Prompt:   "waves breaking over rocks",
		Metadata: map[string]interface{}{"mode": "pro"},
	}

	_, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v2-master"},
	})

	require.ErrorContains(t, err, "mode is not supported")
}

func TestConvertToRequestPayloadPreservesExplicitZeroControls(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Prompt:   "waves breaking over rocks",
		Duration: 5,
		Metadata: map[string]interface{}{
			"cfg_scale": 0,
			"camera_control": map[string]interface{}{
				"type": "simple",
				"config": map[string]interface{}{
					"horizontal": 0,
					"zoom":       0,
				},
			},
		},
	}

	converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v1"},
	})
	require.NoError(t, err)
	data, err := common.Marshal(converted)
	require.NoError(t, err)

	var upstream map[string]interface{}
	require.NoError(t, common.Unmarshal(data, &upstream))
	assert.Contains(t, upstream, "cfg_scale")
	cameraControl, ok := upstream["camera_control"].(map[string]interface{})
	require.True(t, ok)
	config, ok := cameraControl["config"].(map[string]interface{})
	require.True(t, ok)
	assert.Contains(t, config, "horizontal")
	assert.Contains(t, config, "zoom")
}

func TestConvertToRequestPayloadRejectsInvalidCfgScale(t *testing.T) {
	for _, cfgScale := range []float64{-0.1, 1.1} {
		_, err := (&TaskAdaptor{}).convertToRequestPayload(
			&relaycommon.TaskSubmitReq{
				Prompt:   "waves",
				Duration: 5,
				Metadata: map[string]interface{}{"cfg_scale": cfgScale},
			},
			&relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v1"},
			},
		)
		require.ErrorContains(t, err, "cfg_scale")
	}
}

func TestValidateFinalRequestUsesMappedKlingModelBeforePreconsume(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"waves","duration":15}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "kling-v3"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = "kling-v1"
	info.IsModelMapped = true
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
}

func TestBuildRequestBodyRejectsStaleKlingTaskContextType(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", "stale-context-value")

	body, err := (&TaskAdaptor{}).BuildRequestBody(ctx, &relaycommon.RelayInfo{})

	require.ErrorContains(t, err, "invalid request type in context")
	assert.Nil(t, body)
}

func TestEstimateBillingNormalizesKlingDurationAndMode(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "waves",
		Metadata: map[string]interface{}{
			"duration": "10",
			"mode":     "pro",
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v1"},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	assert.Equal(t, map[string]float64{
		"duration": 2,
		"quality":  3.5,
	}, ratios)
}

func TestEstimateBillingDoesNotApplyModeToKlingMasterSKU(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "waves",
		Duration: 10,
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "kling-v2-master"},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	assert.Equal(t, map[string]float64{"duration": 2}, ratios)
}
