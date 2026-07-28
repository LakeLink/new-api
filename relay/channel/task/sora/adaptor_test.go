package sora

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateFinalRequestRejectsCapabilitiesLostBySoraModelMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/v1/videos",
		bytes.NewBufferString(`{"model":"sora-2-pro","prompt":"cat","seconds":"8","size":"1792x1024"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")
	info := &relaycommon.RelayInfo{
		ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "sora-2-pro"},
		TaskRelayInfo: &relaycommon.TaskRelayInfo{Action: constant.TaskActionTextGenerate},
	}
	adaptor := &TaskAdaptor{}

	require.Nil(t, adaptor.ValidateRequestAndSetAction(ctx, info))
	info.UpstreamModelName = "sora-2"
	info.IsModelMapped = true
	taskErr := adaptor.ValidateFinalRequest(ctx, info)

	require.NotNil(t, taskErr)
	assert.Equal(t, http.StatusBadRequest, taskErr.StatusCode)
	assert.Equal(t, "invalid_size", taskErr.Code)
}

func TestValidateFinalRequestEnforcesDocumentedSoraDurations(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, test := range []struct {
		seconds   string
		wantError bool
	}{
		{seconds: "4"},
		{seconds: "8"},
		{seconds: "12"},
		{seconds: "5", wantError: true},
		{seconds: "3600", wantError: true},
	} {
		t.Run(test.seconds, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(
				http.MethodPost,
				"/v1/videos",
				bytes.NewBufferString(fmt.Sprintf(
					`{"model":"sora-2","prompt":"cat","seconds":%q}`,
					test.seconds,
				)),
			)
			ctx.Request.Header.Set("Content-Type", "application/json")
			info := &relaycommon.RelayInfo{
				ChannelMeta:   &relaycommon.ChannelMeta{UpstreamModelName: "sora-2"},
				TaskRelayInfo: &relaycommon.TaskRelayInfo{},
			}

			taskErr := (&TaskAdaptor{}).ValidateFinalRequest(ctx, info)
			if test.wantError {
				require.NotNil(t, taskErr)
				assert.Equal(t, "invalid_seconds", taskErr.Code)
				return
			}
			require.Nil(t, taskErr)
		})
	}
}
