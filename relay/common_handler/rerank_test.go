package common_handler

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type trackingResponseBody struct {
	io.Reader
	closed bool
}

func (b *trackingResponseBody) Close() error {
	b.closed = true
	return nil
}

func TestRerankHandlerRejectsOversizedResponseAndClosesBody(t *testing.T) {
	previousLimit := constant.MaxUpstreamResponseBodyMB
	constant.MaxUpstreamResponseBodyMB = 1
	t.Cleanup(func() {
		constant.MaxUpstreamResponseBodyMB = previousLimit
	})

	body := &trackingResponseBody{
		Reader: strings.NewReader(strings.Repeat("x", (1<<20)+1)),
	}
	resp := &http.Response{Body: body}

	usage, apiErr := RerankHandler(nil, nil, resp)

	assert.Nil(t, usage)
	require.NotNil(t, apiErr)
	assert.Equal(t, types.ErrorCodeReadResponseBodyFailed, apiErr.GetErrorCode())
	assert.ErrorContains(t, apiErr, "response body exceeds 1048576 bytes")
	assert.True(t, body.closed)
}

func TestRerankHandlerRejectsOutOfRangeXinferenceDocumentIndex(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name  string
		index int
	}{
		{name: "negative", index: -1},
		{name: "past end", index: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(recorder)
			body := &trackingResponseBody{
				Reader: strings.NewReader(`{"results":[{"index":` + fmt.Sprint(test.index) + `,"relevance_score":0.9,"document":""}]}`),
			}
			resp := &http.Response{Body: body}
			info := &relaycommon.RelayInfo{
				ChannelMeta: &relaycommon.ChannelMeta{ChannelType: constant.ChannelTypeXinference},
				RerankerInfo: &relaycommon.RerankerInfo{
					Documents:       []any{"only document"},
					ReturnDocuments: true,
				},
			}

			var usage any
			var apiErr *types.NewAPIError
			require.NotPanics(t, func() {
				usage, apiErr = RerankHandler(ctx, info, resp)
			})
			assert.Nil(t, usage)
			require.NotNil(t, apiErr)
			assert.Equal(t, types.ErrorCodeBadResponseBody, apiErr.GetErrorCode())
			assert.Equal(t, http.StatusBadGateway, apiErr.StatusCode)
			assert.Empty(t, recorder.Body.String())
			assert.True(t, body.closed)
		})
	}
}
