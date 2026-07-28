package vidu

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToRequestPayloadLocksViduModel(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt: "a train crossing a bridge",
		Metadata: map[string]interface{}{
			"model":    "a-different-priced-model",
			"duration": 10,
		},
	}

	converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
	})

	require.NoError(t, err)
	assert.Equal(t, "viduq2", converted.Model)
	assert.Equal(t, 10, converted.Duration)
	assert.Equal(t, "720p", converted.Resolution)
}

func TestConvertToRequestPayloadBoundsNativeViduDuration(t *testing.T) {
	for _, duration := range []int{-1, 0, 11, 1000000} {
		t.Run(strconv.Itoa(duration), func(t *testing.T) {
			adaptor := &TaskAdaptor{}
			request := relaycommon.TaskSubmitReq{
				Prompt: "a train crossing a bridge",
				Metadata: map[string]interface{}{
					"duration": duration,
				},
			}

			_, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
			})

			require.ErrorContains(t, err, "duration must be between 1 and 10 seconds")
		})
	}
}

func TestConvertToRequestPayloadPreservesExplicitViduZeroValues(t *testing.T) {
	request := relaycommon.TaskSubmitReq{
		Prompt: "a train crossing a bridge",
		Metadata: map[string]interface{}{
			"seed": 0,
			"bgm":  false,
		},
	}

	converted, err := (&TaskAdaptor{}).convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
	})

	require.NoError(t, err)
	require.NotNil(t, converted.Seed)
	assert.Zero(t, *converted.Seed)
	require.NotNil(t, converted.Bgm)
	assert.False(t, *converted.Bgm)
	data, err := common.Marshal(converted)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"seed":0`)
	assert.Contains(t, string(data), `"bgm":false`)
}

func TestBuildRequestBodyPreservesPricedViduReferenceModel(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train crossing a bridge",
		Images: []string{
			"https://example.com/reference-1.png",
			"https://example.com/reference-2.png",
			"https://example.com/reference-3.png",
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2-pro"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionReferenceGenerate,
		},
	}

	body, err := (&TaskAdaptor{}).BuildRequestBody(context, info)

	require.NoError(t, err)
	data, err := io.ReadAll(body)
	require.NoError(t, err)
	var payload requestPayload
	require.NoError(t, common.Unmarshal(data, &payload))
	assert.Equal(t, "viduq2-pro", payload.Model)
}

func TestBuildRequestBodyRejectsStaleViduTaskContextType(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", "stale-context-value")

	body, err := (&TaskAdaptor{}).BuildRequestBody(context, &relaycommon.RelayInfo{})

	require.ErrorContains(t, err, "invalid request type in context")
	assert.Nil(t, body)
}

func TestValidateFinalRequestRejectsMappedViduDurationBeforePreconsume(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"a train","metadata":{"duration":11}}`),
	)
	context.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(context, info))
	info.UpstreamModelName = "viduq2"
	info.IsModelMapped = true
	taskErr := adaptor.ValidateFinalRequest(context, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
}

func TestEstimateBillingUsesDocumentedViduCreditSchedule(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "a train",
		Duration: 10,
		Size:     "1920x1080",
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionTextGenerate,
		},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(context, info)

	// Q2 text-to-video: default 5s/720p costs 35 credits; 10s/1080p costs 110.
	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 110.0/35.0, ratios["provider_cost"], 1e-12)
}

func TestEstimateBillingIncludesViduQ2BgmSurcharge(t *testing.T) {
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a train",
		Images: []string{"https://example.com/frame.png"},
		Metadata: map[string]interface{}{
			"bgm": true,
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "viduq2-turbo"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{
			Action: constant.TaskActionGenerate,
		},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(context, info)

	// Q2-turbo image-to-video defaults to 40 credits; BGM adds 15 credits.
	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 55.0/40.0, ratios["provider_cost"], 1e-12)
}
