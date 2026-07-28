package hailuo

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToRequestPayloadLocksHailuoModelAndMapsFrames(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt: "a fox walking through snow",
		Images: []string{
			"https://example.com/first.png",
			"https://example.com/last.png",
		},
		Metadata: map[string]interface{}{
			"model":            "a-different-priced-model",
			"duration":         10,
			"resolution":       Resolution1080P,
			"prompt_optimizer": false,
		},
	}

	converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MiniMax-Hailuo-2.3"},
	})

	require.NoError(t, err)
	assert.Equal(t, "MiniMax-Hailuo-2.3", converted.Model)
	assert.Equal(t, "https://example.com/first.png", converted.FirstFrameImage)
	assert.Equal(t, "https://example.com/last.png", converted.LastFrameImage)
	require.NotNil(t, converted.Duration)
	assert.Equal(t, 10, *converted.Duration)
	require.NotNil(t, converted.PromptOptimizer)
	assert.False(t, *converted.PromptOptimizer)
}

func TestConvertToRequestPayloadRejectsUnsupportedHailuoParameters(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
	}{
		{
			name:     "nil duration",
			metadata: map[string]interface{}{"duration": nil},
		},
		{
			name:     "unsupported duration",
			metadata: map[string]interface{}{"duration": 7},
		},
		{
			name:     "unsupported resolution",
			metadata: map[string]interface{}{"resolution": Resolution512P},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			adaptor := &TaskAdaptor{}
			request := relaycommon.TaskSubmitReq{
				Prompt:   "a fox walking through snow",
				Metadata: test.metadata,
			}

			_, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MiniMax-Hailuo-2.3"},
			})

			require.Error(t, err)
		})
	}
}

func TestValidateFinalRequestRejectsNativeHailuoParametersBeforePreconsume(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"a fox","metadata":{"duration":7}}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = "MiniMax-Hailuo-2.3"
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
}

func TestEstimateBillingUsesDocumentedHailuoVariantPrice(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt: "a fox",
		Metadata: map[string]interface{}{
			"duration":   6,
			"resolution": Resolution1080P,
		},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "MiniMax-Hailuo-2.3"},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	require.Contains(t, ratios, "provider_cost")
	assert.InDelta(t, 0.49/0.28, ratios["provider_cost"], 1e-12)
}
