package jimeng

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertToRequestPayloadLocksJimengModel(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Duration: 10,
		Metadata: map[string]interface{}{
			"req_key": "a-different-priced-model",
			"frames":  241,
		},
	}

	converted, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "jimeng_vgfm_t2v_l20"},
	})

	require.NoError(t, err)
	assert.Equal(t, "jimeng_vgfm_t2v_l20", converted.ReqKey)
	assert.Equal(t, 241, converted.Frames)
}

func TestConvertToRequestPayloadRejectsUnsupportedJimengDuration(t *testing.T) {
	adaptor := &TaskAdaptor{}
	request := relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat on a river",
		Duration: 6,
	}

	_, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "jimeng_vgfm_t2v_l20"},
	})

	require.ErrorContains(t, err, "duration must be either 5 or 10 seconds")
}

func TestConvertToRequestPayloadBoundsNativeJimengFrames(t *testing.T) {
	for _, frames := range []int{0, 120, 242, 1000000} {
		t.Run(strconv.Itoa(frames), func(t *testing.T) {
			adaptor := &TaskAdaptor{}
			request := relaycommon.TaskSubmitReq{
				Prompt: "a paper boat on a river",
				Metadata: map[string]interface{}{
					"frames": frames,
				},
			}

			_, err := adaptor.convertToRequestPayload(&request, &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "jimeng_vgfm_t2v_l20"},
			})

			require.ErrorContains(t, err, "frames must be either 121")
		})
	}
}

func TestValidateFinalRequestRejectsNativeJimengFramesBeforePreconsume(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"prompt":"a paper boat","metadata":{"frames":120}}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = "jimeng_vgfm_t2v_l20"
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.True(t, taskErr.LocalError)
}

func TestEstimateBillingNormalizesJimengFramesToDefaultDuration(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Set("task_request", relaycommon.TaskSubmitReq{
		Prompt:   "a paper boat",
		Metadata: map[string]interface{}{"frames": 241},
	})
	info := &relaycommon.RelayInfo{
		ChannelMeta: &relaycommon.ChannelMeta{UpstreamModelName: "jimeng_vgfm_t2v_l20"},
	}

	ratios := (&TaskAdaptor{}).EstimateBilling(ctx, info)

	assert.Equal(t, map[string]float64{"duration": 2}, ratios)
}
